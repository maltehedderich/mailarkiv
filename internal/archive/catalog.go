package archive

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// PageSize is the number of messages on one list page.
const PageSize = 50

// Page is one page of the message list.
type Page struct {
	Items []Message
	// Total counts every message, so a page past the end still reports it.
	Total int
}

// entryColumns are the columns of source_observation o that entryRow scans,
// in its order.
const entryColumns = `o.source_type, o.observed_at, o.account_email, o.file_name,
       o.imap_source_id, o.imap_folder_id, o.received_at`

// messageColumns are the columns scanMessage reads, from messageTables.
const messageColumns = `m.id, m.content_hash, m.size,
       (SELECT COUNT(*) FROM source_observation c WHERE c.message_id = m.id),
       m.processing_state, m.list_date, ` + summaryColumns + `,
       ` + entryColumns

// messageTables joins a message to its latest entry as o and its summary, if
// it has one, as s.
const messageTables = `message m
JOIN source_observation o ON o.id = m.latest_observation_id
LEFT JOIN message_summary s ON s.message_id = m.id`

// listOrder is the list's order, newest first, which message_list_order and
// message_with_problems serve. The ID makes it total, so pages never repeat
// or skip a message.
const listOrder = "ORDER BY m.list_date DESC, m.first_archived_at DESC, m.id DESC"

// withProblems matches message_with_problems' condition, so SQLite can use it.
const withProblems = "m.processing_state IN ('processed_with_problems', 'unreadable')"

// List returns page number page, counting from 1. A page past the end has no
// items and the real total.
func (a *Archive) List(ctx context.Context, page int) (Page, error) {
	result, err := a.listPage(ctx, page, "1")
	if err != nil {
		return Page{}, fmt.Errorf("list messages: %w", err)
	}
	return result, nil
}

// ProblemList is List with only the messages processed with problems or that
// couldn't be read.
func (a *Archive) ProblemList(ctx context.Context, page int) (Page, error) {
	result, err := a.listPage(ctx, page, withProblems)
	if err != nil {
		return Page{}, fmt.Errorf("list processing problems: %w", err)
	}
	return result, nil
}

// listPage reads one page of the messages that match where, a constant
// condition on message m.
func (a *Archive) listPage(ctx context.Context, page int, where string) (Page, error) {
	if page < 1 {
		return Page{}, fmt.Errorf("page %d: pages count from 1", page)
	}
	result := Page{Items: []Message{}}
	// A page so far past the end that its offset overflows still lists nothing.
	offset := math.MaxInt
	if page-1 <= math.MaxInt/PageSize {
		offset = (page - 1) * PageSize
	}

	// One read transaction, so the items and the total come from one snapshot.
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT `+messageColumns+`
FROM `+messageTables+`
WHERE `+where+`
`+listOrder+`
LIMIT ? OFFSET ?`, PageSize, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			message, err := scanMessage(rows)
			if err != nil {
				return err
			}
			result.Items = append(result.Items, message)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM message m WHERE "+where).Scan(&result.Total)
	})
	if err != nil {
		return Page{}, err
	}
	return result, nil
}

// readMessage reads one message with its latest entry, as the list shows it.
func readMessage(ctx context.Context, tx *sql.Tx, id int64) (Message, error) {
	row := tx.QueryRowContext(ctx, `
SELECT `+messageColumns+`
FROM `+messageTables+`
WHERE m.id = ?`, id)
	return scanMessage(row)
}

type scanner interface{ Scan(dest ...any) error }

func scanMessage(row scanner) (Message, error) {
	var message Message
	var state string
	var listDate int64
	var summary summaryRow
	var entry entryRow
	targets := []any{&message.ID, &message.ContentHash, &message.Size, &message.ObservationCount, &state, &listDate}
	targets = append(append(targets, summary.targets()...), entry.targets()...)
	if err := row.Scan(targets...); err != nil {
		return Message{}, err
	}
	var err error
	if message.Processing, err = parseProcessingState(state); err != nil {
		return Message{}, err
	}
	message.ListDate = time.UnixMilli(listDate).UTC()
	message.Summary = summary.summary()
	message.ListDateIsArchiveTime = message.Summary == nil || message.Summary.DateReason != nil
	if message.Latest, err = entry.entry(); err != nil {
		return Message{}, err
	}
	return message, nil
}

// entryRow holds entryColumns as scanned.
type entryRow struct {
	sourceType   string
	observedAt   int64
	accountEmail sql.NullString
	fileName     sql.NullString
	sourceID     sql.NullInt64
	folderID     sql.NullInt64
	receivedAt   sql.NullInt64
}

func (r *entryRow) targets() []any {
	return []any{&r.sourceType, &r.observedAt, &r.accountEmail, &r.fileName, &r.sourceID, &r.folderID, &r.receivedAt}
}

// entry builds the entry the row's source type names. The table's CHECK
// constraints guarantee the columns each kind needs.
func (r *entryRow) entry() (Entry, error) {
	observedAt := time.UnixMilli(r.observedAt).UTC()
	switch r.sourceType {
	case "manual_import":
		entry := ImportEntry{ObservedAt: observedAt, AccountEmail: r.accountEmail.String}
		if r.fileName.Valid {
			entry.FileName = &r.fileName.String
		}
		return entry, nil
	case "imap_collection":
		entry := CollectionEntry{ObservedAt: observedAt, SourceID: r.sourceID.Int64, FolderID: r.folderID.Int64}
		if r.receivedAt.Valid {
			received := time.UnixMilli(r.receivedAt.Int64).UTC()
			entry.ReceivedAt = &received
		}
		return entry, nil
	default:
		return nil, fmt.Errorf("an entry has the unknown source type %q", r.sourceType)
	}
}
