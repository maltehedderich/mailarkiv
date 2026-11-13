package collect

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// maxProblemItems is how many messages of each kind a source's page lists.
const maxProblemItems = 100

// ProblemSource is a source the status screen lists. It names no user,
// folder, or server reply.
type ProblemSource struct {
	ID       int64
	Name     string
	Category Category
	// Since is when the problem began.
	Since time.Time
	// FailedAttempts is set for a sync failure only.
	FailedAttempts *int
}

// ProblemCounts counts what needs attention in the folders a source
// collects.
type ProblemCounts struct {
	UnavailableFolders int
	WaitingForRetry    int
	// NotArchived counts messages too large and messages removed before
	// they were archived.
	NotArchived int
}

// ProblemMessage is a message waiting for retry or not archived. It holds
// no message content.
type ProblemMessage struct {
	Folder string
	// Size and ReceivedAt are nil when the server didn't report them.
	Size       *int64
	ReceivedAt *time.Time
	RecordedAt time.Time
	// Category is MessageUnavailable for a message waiting for retry, and
	// MessageTooLarge or MessageRemoved for one not archived.
	Category Category
}

// ProblemList is the newest messages of one kind, with how many there are.
type ProblemList struct {
	Total int
	Items []ProblemMessage
}

type ProblemMessages struct {
	WaitingForRetry ProblemList
	NotArchived     ProblemList
}

// SourceDetail is a source as its own page shows it.
type SourceDetail struct {
	Source
	Messages ProblemMessages
}

// ProblemSources returns each source that is failing, needs attention, or
// has an unavailable folder or a message waiting for retry, by name, then
// ID. A source reports one problem: its sync failure or unreadable password,
// otherwise its earliest unavailable folder, otherwise its earliest waiting
// message. A failure run lasts until a sync succeeds, so a source keeps it
// while its next attempt runs. A paused source reports no sync failure,
// because an administrator stopped it.
func (c *Collector) ProblemSources(ctx context.Context) ([]ProblemSource, error) {
	type problemRow struct {
		id                  int64
		name                string
		paused              bool
		failedAttempts      int
		failingSince        sql.NullInt64
		lastFailureCategory sql.NullString
		unavailableSince    sql.NullInt64
		waitingSince        sql.NullInt64
	}
	var found []problemRow
	rows, err := c.deps.DB.Reader.QueryContext(ctx, `
SELECT s.id, s.name, s.paused_at IS NOT NULL, s.failed_attempts, s.failing_since, s.last_failure_category,
       (SELECT MIN(f.unavailable_since) FROM imap_folder f WHERE f.source_id = s.id AND f.chosen = 1),
       (SELECT MIN(p.recorded_at) FROM imap_message_problem p JOIN imap_folder f ON f.id = p.folder_id
        WHERE f.source_id = s.id AND f.chosen = 1 AND p.kind = 'waiting_for_retry')
FROM imap_source s
WHERE s.removed_at IS NULL
ORDER BY s.name, s.id`)
	if err != nil {
		return nil, fmt.Errorf("read problem sources: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var row problemRow
		if err := rows.Scan(&row.id, &row.name, &row.paused, &row.failedAttempts, &row.failingSince,
			&row.lastFailureCategory, &row.unavailableSince, &row.waitingSince); err != nil {
			return nil, fmt.Errorf("read problem sources: %w", err)
		}
		found = append(found, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read problem sources: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	var sources []ProblemSource
	for _, row := range found {
		source := ProblemSource{ID: row.id, Name: row.name}
		unreadableSince, unreadable := c.unreadable[row.id]
		switch {
		case !row.paused && unreadable:
			source.Category, source.Since = PasswordUnreadable, unreadableSince
		case !row.paused && row.failedAttempts > 0:
			category, err := parseSyncCategory(row.lastFailureCategory.String)
			if err != nil {
				return nil, fmt.Errorf("read source %d: %w", row.id, err)
			}
			n := row.failedAttempts
			source.Category, source.Since, source.FailedAttempts = category, fromMillis(row.failingSince.Int64), &n
		case row.unavailableSince.Valid:
			source.Category, source.Since = FolderUnavailable, fromMillis(row.unavailableSince.Int64)
		case row.waitingSince.Valid:
			source.Category, source.Since = MessageUnavailable, fromMillis(row.waitingSince.Int64)
		default:
			continue
		}
		sources = append(sources, source)
	}
	return sources, nil
}

// Detail returns one source with the newest messages waiting for retry and
// not archived, or ErrNotFound.
func (c *Collector) Detail(ctx context.Context, id int64) (SourceDetail, error) {
	source, err := c.Get(ctx, id)
	if err != nil {
		return SourceDetail{}, err
	}
	detail := SourceDetail{Source: source}
	err = sqlitedb.InTx(ctx, c.deps.DB.Reader, func(tx *sql.Tx) error {
		var err error
		if detail.Messages.WaitingForRetry, err = problemList(ctx, tx, id, "p.kind = 'waiting_for_retry'"); err != nil {
			return err
		}
		detail.Messages.NotArchived, err = problemList(ctx, tx, id, "p.kind IN ('too_large', 'removed')")
		return err
	})
	if err != nil {
		return SourceDetail{}, fmt.Errorf("read source %d's messages: %w", id, err)
	}
	return detail, nil
}

// problemList reads the newest problem messages of the kinds in condition,
// in the folders the source collects, and counts them all.
func problemList(ctx context.Context, tx *sql.Tx, sourceID int64, condition string) (ProblemList, error) {
	rows, err := tx.QueryContext(ctx, `
SELECT f.name, p.kind, p.size, p.received_at, p.recorded_at, COUNT(*) OVER ()
FROM imap_message_problem p
JOIN imap_folder f ON f.id = p.folder_id
WHERE f.source_id = ? AND f.chosen = 1 AND `+condition+`
ORDER BY p.recorded_at DESC, p.folder_id, p.uid
LIMIT ?`, sourceID, maxProblemItems)
	if err != nil {
		return ProblemList{}, err
	}
	defer rows.Close()
	list := ProblemList{Items: []ProblemMessage{}}
	for rows.Next() {
		var item ProblemMessage
		var kind string
		var size, receivedAt sql.NullInt64
		var recordedAt int64
		if err := rows.Scan(&item.Folder, &kind, &size, &receivedAt, &recordedAt, &list.Total); err != nil {
			return ProblemList{}, err
		}
		if item.Category, err = problemCategory(kind); err != nil {
			return ProblemList{}, err
		}
		if size.Valid {
			item.Size = &size.Int64
		}
		item.ReceivedAt = optionalTime(receivedAt)
		item.RecordedAt = fromMillis(recordedAt)
		list.Items = append(list.Items, item)
	}
	return list, rows.Err()
}

// loadProblemCounts counts each source's problem messages, in the folders it
// collects. Its unavailable folders are counted from the folders already
// loaded.
func loadProblemCounts(ctx context.Context, tx *sql.Tx, filter string, args []any, byID map[int64]*Source) error {
	for _, source := range byID {
		for _, folder := range source.Folders {
			if folder.UnavailableSince != nil {
				source.Problems.UnavailableFolders++
			}
		}
	}
	rows, err := tx.QueryContext(ctx, `
SELECT f.source_id, p.kind = 'waiting_for_retry', COUNT(*)
FROM imap_message_problem p
JOIN imap_folder f ON f.id = p.folder_id
JOIN imap_source s ON s.id = f.source_id
WHERE f.chosen = 1 AND s.removed_at IS NULL `+filter+`
GROUP BY f.source_id, p.kind = 'waiting_for_retry'`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID int64
		var waiting bool
		var n int
		if err := rows.Scan(&sourceID, &waiting, &n); err != nil {
			return err
		}
		source, ok := byID[sourceID]
		switch {
		case !ok:
		case waiting:
			source.Problems.WaitingForRetry = n
		default:
			source.Problems.NotArchived = n
		}
	}
	return rows.Err()
}

// problemCategory is the category of a problem row's kind.
func problemCategory(kind string) (Category, error) {
	switch kind {
	case "waiting_for_retry":
		return MessageUnavailable, nil
	case "too_large":
		return MessageTooLarge, nil
	case "removed":
		return MessageRemoved, nil
	}
	return Category{}, fmt.Errorf("unknown problem kind %q", kind)
}

func fromMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }
