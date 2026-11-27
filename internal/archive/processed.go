package archive

import (
	"cmp"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// ProcessingState is where processing stands with a message. A message
// moves from NotProcessed to one of the others once.
type ProcessingState struct{ name string }

var (
	NotProcessed          = ProcessingState{"not_processed"}
	Processed             = ProcessingState{"processed"}
	ProcessedWithProblems = ProcessingState{"processed_with_problems"}
	Unreadable            = ProcessingState{"unreadable"}
)

// String returns the state's name, which the API shares.
func (s ProcessingState) String() string { return s.name }

func parseProcessingState(name string) (ProcessingState, error) {
	for _, state := range []ProcessingState{NotProcessed, Processed, ProcessedWithProblems, Unreadable} {
		if state.name == name {
			return state, nil
		}
	}
	return ProcessingState{}, fmt.Errorf("a message has the unknown processing state %q", name)
}

// Summary is what a row shows of a message that processing read. The values
// arrive cut to the catalog's lengths.
type Summary struct {
	SenderName    *string
	SenderAddress *string
	MoreSenders   int
	Subject       *string
	DateHeader    *string
	// DateReason is nil when the list date is the Date header's time, and
	// otherwise "missing", "unreadable", or "ahead".
	DateReason     *string
	HasAttachments bool
}

// Problem is one kind of problem in one part of a message.
type Problem struct {
	// Position is the part number, or "" for the whole message.
	Position     string
	DeclaredType *string
	Kind         string
}

// ProcessingResult is what processing found in one message.
type ProcessingResult struct {
	State    ProcessingState
	ListDate time.Time
	// Summary is nil for an Unreadable message.
	Summary  *Summary
	Problems []Problem
}

// ProcessedMessage is a message the indexer reads after its cursor.
type ProcessedMessage struct {
	ID              int64
	Seq             int64
	State           ProcessingState
	FirstArchivedAt time.Time
	ListDate        time.Time
	// Summary is nil for an Unreadable message.
	Summary *Summary
}

// RecordProcessed stores processing's result for a message that is not
// processed yet, with the next processed_seq, in one transaction. It reports
// false and changes nothing when the message already has a result.
func (a *Archive) RecordProcessed(ctx context.Context, messageID int64, result ProcessingResult) (bool, error) {
	if result.State == NotProcessed || result.State == (ProcessingState{}) {
		return false, fmt.Errorf("record message %d: %q isn't a result", messageID, result.State.name)
	}
	if (result.State == Unreadable) != (result.Summary == nil) {
		return false, fmt.Errorf("record message %d: only an unreadable message has no summary", messageID)
	}
	changed := false
	err := sqlitedb.InTx(ctx, a.db.Writer, func(tx *sql.Tx) error {
		// The catalog has one writer, so the highest value plus 1 leaves no gaps.
		updated, err := tx.ExecContext(ctx, `
UPDATE message
SET processing_state = ?, list_date = ?,
    processed_seq = (SELECT COALESCE(MAX(processed_seq), 0) + 1 FROM message)
WHERE id = ? AND processing_state = 'not_processed'`,
			result.State.name, result.ListDate.UnixMilli(), messageID)
		if err != nil {
			return err
		}
		if n, err := updated.RowsAffected(); err != nil || n == 0 {
			return err
		}
		changed = true
		if s := result.Summary; s != nil {
			_, err := tx.ExecContext(ctx, `
INSERT INTO message_summary
    (message_id, sender_name, sender_address, more_senders, subject, date_header, date_reason, has_attachments)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				messageID, nullable(s.SenderName), nullable(s.SenderAddress), s.MoreSenders,
				nullable(s.Subject), nullable(s.DateHeader), nullable(s.DateReason), s.HasAttachments)
			if err != nil {
				return err
			}
		}
		for _, problem := range result.Problems {
			_, err := tx.ExecContext(ctx,
				"INSERT INTO message_problem (message_id, position, declared_type, kind) VALUES (?, ?, ?, ?)",
				messageID, problem.Position, nullable(problem.DeclaredType), problem.Kind)
			if err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return false, fmt.Errorf("record message %d's processing: %w", messageID, err)
	}
	return changed, nil
}

// NotProcessed returns up to limit IDs of messages not processed yet, newest
// capture first, leaving out the IDs in skip.
func (a *Archive) NotProcessed(ctx context.Context, limit int, skip map[int64]bool) ([]int64, error) {
	rows, err := a.db.Reader.QueryContext(ctx,
		"SELECT id FROM message WHERE processing_state = 'not_processed' ORDER BY id DESC LIMIT ?",
		limit+len(skip))
	if err != nil {
		return nil, fmt.Errorf("list messages not processed: %w", err)
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() && len(ids) < limit {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("list messages not processed: %w", err)
		}
		if !skip[id] {
			ids = append(ids, id)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list messages not processed: %w", err)
	}
	return ids, nil
}

// CountNotProcessed counts the messages that have no processing result yet.
func (a *Archive) CountNotProcessed(ctx context.Context) (int, error) {
	var count int
	err := a.db.Reader.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM message WHERE processing_state = 'not_processed'").Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count messages not processed: %w", err)
	}
	return count, nil
}

// ProcessingCounts is how many messages wait for processing and how many it
// found problems in.
type ProcessingCounts struct {
	NotProcessed          int
	ProcessedWithProblems int
	Unreadable            int
}

// ProcessingCounts counts the messages in each state the status screen shows.
func (a *Archive) ProcessingCounts(ctx context.Context) (ProcessingCounts, error) {
	var counts ProcessingCounts
	// processed_seq has no gaps, so the highest value counts the processed
	// messages without a scan. Both counts come from one snapshot.
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) - COALESCE(MAX(processed_seq), 0) FROM message").Scan(&counts.NotProcessed); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx,
			"SELECT m.processing_state, COUNT(*) FROM message m WHERE "+withProblems+" GROUP BY m.processing_state")
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var state string
			var n int
			if err := rows.Scan(&state, &n); err != nil {
				return err
			}
			switch state {
			case ProcessedWithProblems.name:
				counts.ProcessedWithProblems = n
			case Unreadable.name:
				counts.Unreadable = n
			}
		}
		return rows.Err()
	})
	if err != nil {
		return ProcessingCounts{}, fmt.Errorf("count messages by processing state: %w", err)
	}
	return counts, nil
}

// HighestProcessedSeq returns the highest processed_seq, or 0 when no message
// is processed.
func (a *Archive) HighestProcessedSeq(ctx context.Context) (int64, error) {
	var highest int64
	err := a.db.Reader.QueryRowContext(ctx, "SELECT COALESCE(MAX(processed_seq), 0) FROM message").Scan(&highest)
	if err != nil {
		return 0, fmt.Errorf("read the highest processed_seq: %w", err)
	}
	return highest, nil
}

// summaryColumns are the columns of message_summary s that summaryRow scans.
const summaryColumns = `s.message_id IS NOT NULL, s.sender_name, s.sender_address, s.more_senders,
       s.subject, s.date_header, s.date_reason, s.has_attachments`

// ProcessedAfter returns up to limit messages whose processed_seq is above
// cursor, in processed_seq order.
func (a *Archive) ProcessedAfter(ctx context.Context, cursor int64, limit int) ([]ProcessedMessage, error) {
	rows, err := a.db.Reader.QueryContext(ctx, `
SELECT m.id, m.processed_seq, m.processing_state, m.first_archived_at, m.list_date, `+summaryColumns+`
FROM message m
LEFT JOIN message_summary s ON s.message_id = m.id
WHERE m.processed_seq > ?
ORDER BY m.processed_seq
LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, fmt.Errorf("read messages processed after %d: %w", cursor, err)
	}
	defer rows.Close()
	var messages []ProcessedMessage
	for rows.Next() {
		var message ProcessedMessage
		var state string
		var firstArchived, listDate int64
		var summary summaryRow
		targets := append([]any{&message.ID, &message.Seq, &state, &firstArchived, &listDate}, summary.targets()...)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("read messages processed after %d: %w", cursor, err)
		}
		if message.State, err = parseProcessingState(state); err != nil {
			return nil, err
		}
		message.FirstArchivedAt = time.UnixMilli(firstArchived).UTC()
		message.ListDate = time.UnixMilli(listDate).UTC()
		message.Summary = summary.summary()
		messages = append(messages, message)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read messages processed after %d: %w", cursor, err)
	}
	return messages, nil
}

// Rows returns the list rows for ids, in the order given. An ID with no
// message is left out.
func (a *Archive) Rows(ctx context.Context, ids []int64) ([]Message, error) {
	messages := make([]Message, 0, len(ids))
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		for _, id := range ids {
			message, err := readMessage(ctx, tx, id)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			messages = append(messages, message)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("read rows: %w", err)
	}
	return messages, nil
}

// summaryRow holds summaryColumns as scanned.
type summaryRow struct {
	present        bool
	senderName     sql.NullString
	senderAddress  sql.NullString
	moreSenders    sql.NullInt64
	subject        sql.NullString
	dateHeader     sql.NullString
	dateReason     sql.NullString
	hasAttachments sql.NullBool
}

func (r *summaryRow) targets() []any {
	return []any{&r.present, &r.senderName, &r.senderAddress, &r.moreSenders,
		&r.subject, &r.dateHeader, &r.dateReason, &r.hasAttachments}
}

func (r *summaryRow) summary() *Summary {
	if !r.present {
		return nil
	}
	return &Summary{
		SenderName:     stringOrNil(r.senderName),
		SenderAddress:  stringOrNil(r.senderAddress),
		MoreSenders:    int(r.moreSenders.Int64),
		Subject:        stringOrNil(r.subject),
		DateHeader:     stringOrNil(r.dateHeader),
		DateReason:     stringOrNil(r.dateReason),
		HasAttachments: r.hasAttachments.Bool,
	}
}

func stringOrNil(s sql.NullString) *string {
	if !s.Valid {
		return nil
	}
	return &s.String
}

// signal wakes whoever waits on c without ever blocking the sender. A
// waiting signal stands for any number of sends.
func signal(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// CaptureSignal fires after a commit that adds a message.
func (a *Archive) CaptureSignal() <-chan struct{} { return a.captured }

// RestoreSignal fires after a capture that restores a missing or damaged
// original.
func (a *Archive) RestoreSignal() <-chan struct{} { return a.restored }

// ReadMessage returns one message's list row and its problems, read together.
// Problems go in part order, with the whole message last, and within a part
// in the order of the problem kinds' table. ErrNotFound means no message has
// the ID.
func (a *Archive) ReadMessage(ctx context.Context, id int64) (Message, []Problem, error) {
	var message Message
	var problems []Problem
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		var err error
		message, err = readMessage(ctx, tx, id)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, "SELECT position, declared_type, kind FROM message_problem WHERE message_id = ?", id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var problem Problem
			var declared sql.NullString
			if err := rows.Scan(&problem.Position, &declared, &problem.Kind); err != nil {
				return err
			}
			problem.DeclaredType = stringOrNil(declared)
			problems = append(problems, problem)
		}
		return rows.Err()
	})
	if errors.Is(err, ErrNotFound) {
		return Message{}, nil, ErrNotFound
	}
	if err != nil {
		return Message{}, nil, fmt.Errorf("read message %d: %w", id, err)
	}
	slices.SortFunc(problems, func(x, y Problem) int {
		if order := comparePositions(x.Position, y.Position); order != 0 {
			return order
		}
		return cmp.Compare(kindOrder[x.Kind], kindOrder[y.Kind])
	})
	return message, problems, nil
}

// kindOrder is the problem kinds' order in the contract's table.
var kindOrder = map[string]int{
	"unknown_charset": 0, "invalid_encoding": 1, "malformed_header": 2, "malformed_structure": 3,
	"cut_short": 4, "too_many_parts": 5, "nested_too_deeply": 6, "body_cut": 7, "took_too_long": 8,
}

// comparePositions orders part numbers as the parts come in the message:
// number by number, a part before the parts inside it. "" is the whole
// message and comes last. The worker writes the numbers, so each piece is
// digits.
func comparePositions(x, y string) int {
	switch {
	case x == y:
		return 0
	case x == "":
		return 1
	case y == "":
		return -1
	}
	xs, ys := strings.Split(x, "."), strings.Split(y, ".")
	for i := range min(len(xs), len(ys)) {
		if order := cmp.Compare(len(xs[i]), len(ys[i])); order != 0 {
			return order
		}
		if order := strings.Compare(xs[i], ys[i]); order != 0 {
			return order
		}
	}
	return cmp.Compare(len(xs), len(ys))
}
