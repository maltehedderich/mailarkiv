package archive

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// CollectedMeta is what a collection records besides the bytes. Archive can't
// check that the folder belongs to the source, so collect must.
type CollectedMeta struct {
	SourceID   int64
	FolderID   int64
	ReceivedAt *time.Time // Nil when the server reported no time.
}

// StoreCollected stores one message a source's folder returned, in the same
// capture order as Import. It skips the mail header check, because collection
// keeps whatever the server returns, and refuses only an empty body. A folder
// gets at most one entry per message.
//
// A failure is logged with its category and cause, and records no failure
// event, because the source reports it. A restored original still records
// its event.
func (a *Archive) StoreCollected(ctx context.Context, body io.Reader, meta CollectedMeta) (Result, error) {
	head, err := io.ReadAll(io.LimitReader(body, headSize))
	if err != nil {
		return Dropped{}, nil
	}
	if len(head) == 0 {
		return Refused{Reason: EmptyFile}, nil
	}
	return a.capture(context.WithoutCancel(ctx), head, body, a.collecting(meta))
}

func (a *Archive) collecting(meta CollectedMeta) captureKind {
	var receivedAt sql.NullInt64
	if meta.ReceivedAt != nil {
		receivedAt = sql.NullInt64{Int64: meta.ReceivedAt.UnixMilli(), Valid: true}
	}
	return captureKind{
		record: func(ctx context.Context, tx *sql.Tx, messageID, observedAt int64) error {
			var seen bool
			if err := tx.QueryRowContext(ctx,
				"SELECT EXISTS (SELECT 1 FROM source_observation WHERE imap_source_id = ? AND message_id = ?)",
				meta.SourceID, messageID).Scan(&seen); err != nil {
				return err
			}
			inserted, err := tx.ExecContext(ctx,
				`INSERT INTO source_observation (message_id, source_type, observed_at, imap_source_id, imap_folder_id, received_at)
				 VALUES (?, 'imap_collection', ?, ?, ?, ?)
				 ON CONFLICT (imap_folder_id, message_id) WHERE imap_folder_id IS NOT NULL DO NOTHING`,
				messageID, observedAt, meta.SourceID, meta.FolderID, receivedAt)
			if err != nil {
				return err
			}
			added, err := inserted.RowsAffected()
			if err != nil {
				return err
			}
			if added == 1 {
				// LastInsertId is only this row's when the insert added it.
				observationID, err := inserted.LastInsertId()
				if err != nil {
					return err
				}
				if _, err := tx.ExecContext(ctx,
					"UPDATE message SET latest_observation_id = ? WHERE id = ?", observationID, messageID); err != nil {
					return err
				}
			}
			if seen {
				return nil
			}
			_, err = tx.ExecContext(ctx,
				`INSERT INTO collected_message_count (imap_source_id, messages) VALUES (?, 1)
				 ON CONFLICT (imap_source_id) DO UPDATE SET messages = messages + 1`,
				meta.SourceID)
			return err
		},
		fail: func(category opstatus.Category, _ string, cause error) Failed {
			a.logger.Error("collected message not stored", "category", category.String(), "error", cause.Error())
			return Failed{Category: category}
		},
	}
}

// CollectedCounts returns how many distinct messages each source collected.
// Every ID asked for is in the map, with 0 for a source that collected none.
func (a *Archive) CollectedCounts(ctx context.Context, sourceIDs []int64) (map[int64]int, error) {
	counts := make(map[int64]int, len(sourceIDs))
	if len(sourceIDs) == 0 {
		return counts, nil
	}
	args := make([]any, len(sourceIDs))
	for i, id := range sourceIDs {
		counts[id] = 0
		args[i] = id
	}
	rows, err := a.db.Reader.QueryContext(ctx,
		"SELECT imap_source_id, messages FROM collected_message_count WHERE imap_source_id IN (?"+
			strings.Repeat(", ?", len(sourceIDs)-1)+")", args...)
	if err != nil {
		return nil, fmt.Errorf("read collected counts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("read collected counts: %w", err)
		}
		counts[id] = n
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read collected counts: %w", err)
	}
	return counts, nil
}
