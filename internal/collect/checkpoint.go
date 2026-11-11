package collect

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// loadMailbox reads where a source signs in, and its sealed password.
func (c *Collector) loadMailbox(ctx context.Context, sourceID int64) (mailbox, []byte, error) {
	var box mailbox
	var security string
	var sealed []byte
	err := c.deps.DB.Reader.QueryRowContext(ctx, `
SELECT host, port, security, user_name, sealed_password
FROM imap_source WHERE id = ? AND removed_at IS NULL`, sourceID).
		Scan(&box.host, &box.port, &security, &box.userName, &sealed)
	if err != nil {
		return mailbox{}, nil, catalogFailure(fmt.Errorf("read source %d: %w", sourceID, err))
	}
	if box.security, err = parseSecurity(security); err != nil {
		return mailbox{}, nil, catalogFailure(err)
	}
	return box, sealed, nil
}

// chosenFolders reads the folders a sync takes, in the order it takes them.
func (c *Collector) chosenFolders(ctx context.Context, sourceID int64) ([]folderRow, error) {
	rows, err := c.deps.DB.Reader.QueryContext(ctx, `
SELECT id, name, uid_validity, last_uid FROM imap_folder
WHERE source_id = ? AND chosen = 1
ORDER BY id`, sourceID)
	if err != nil {
		return nil, catalogFailure(err)
	}
	defer rows.Close()
	var folders []folderRow
	for rows.Next() {
		var folder folderRow
		if err := rows.Scan(&folder.id, &folder.name, &folder.uidValidity, &folder.lastUID); err != nil {
			return nil, catalogFailure(err)
		}
		folders = append(folders, folder)
	}
	if err := rows.Err(); err != nil {
		return nil, catalogFailure(err)
	}
	return folders, nil
}

// storeUIDValidity records a folder's numbering the first time it opens.
func (c *Collector) storeUIDValidity(ctx context.Context, folderID int64, uidValidity uint32) error {
	_, err := c.deps.DB.Writer.ExecContext(ctx,
		"UPDATE imap_folder SET uid_validity = ?, last_uid = 0 WHERE id = ? AND uid_validity IS NULL",
		uidValidity, folderID)
	if err != nil {
		return fmt.Errorf("store folder %d's UIDVALIDITY: %w", folderID, err)
	}
	return nil
}

// recordSuccess records the end of a sync that succeeded.
func (c *Collector) recordSuccess(ctx context.Context, sourceID int64, archived int) error {
	_, err := c.deps.DB.Writer.ExecContext(ctx,
		"UPDATE imap_source SET last_success_at = ?, last_success_archived = ? WHERE id = ?",
		c.deps.Now().UnixMilli(), archived, sourceID)
	if err != nil {
		return fmt.Errorf("record source %d's success: %w", sourceID, err)
	}
	return nil
}

// renumber starts a folder over under a new UIDVALIDITY. The waiting and too
// large rows name UIDs that mean nothing now, so they go. Removed rows stay.
func (c *Collector) renumber(ctx context.Context, folderID int64, uidValidity uint32) error {
	err := sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE imap_folder SET uid_validity = ?, last_uid = 0, renumbered_at = ? WHERE id = ?",
			uidValidity, c.deps.Now().UnixMilli(), folderID); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"DELETE FROM imap_message_problem WHERE folder_id = ? AND kind IN ('waiting_for_retry', 'too_large')",
			folderID)
		return err
	})
	if err != nil {
		return fmt.Errorf("renumber folder %d: %w", folderID, err)
	}
	return nil
}

// waitingUIDs reads the folder's messages waiting for a retry under its
// current numbering, in rising order.
func (c *Collector) waitingUIDs(ctx context.Context, folderID int64, uidValidity uint32) ([]imap.UID, error) {
	rows, err := c.deps.DB.Reader.QueryContext(ctx, `
SELECT uid FROM imap_message_problem
WHERE folder_id = ? AND uid_validity = ? AND kind = 'waiting_for_retry'
ORDER BY uid`, folderID, uidValidity)
	if err != nil {
		return nil, fmt.Errorf("read folder %d's retries: %w", folderID, err)
	}
	defer rows.Close()
	var uids []imap.UID
	for rows.Next() {
		var uid imap.UID
		if err := rows.Scan(&uid); err != nil {
			return nil, fmt.Errorf("read folder %d's retries: %w", folderID, err)
		}
		uids = append(uids, uid)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read folder %d's retries: %w", folderID, err)
	}
	return uids, nil
}

// problemChange is what handling a message does to its problem row.
type problemChange int

const (
	noChange problemChange = iota
	// clearRetry deletes a waiting row, once the message is stored.
	clearRetry
	// markWaiting adds a waiting row. One already there keeps the time it was
	// first recorded.
	markWaiting
	markTooLarge
	// markRemoved turns a waiting row into a removed one.
	markRemoved
)

// settle commits what one message leaves behind. A new message moves the
// checkpoint to its UID in the same transaction as its problem row, so a
// stop can't separate the two.
func (c *Collector) settle(ctx context.Context, m message, change problemChange) error {
	ctx = context.WithoutCancel(ctx)
	now := c.deps.Now().UnixMilli()
	err := sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
		if !m.retry {
			if _, err := tx.ExecContext(ctx,
				"UPDATE imap_folder SET last_uid = ? WHERE id = ? AND uid_validity = ? AND last_uid < ?",
				m.uid, m.folderID, m.uidValidity, m.uid); err != nil {
				return err
			}
		}
		if err := changeProblem(ctx, tx, m, change, now); err != nil {
			return err
		}
		// An injected error rolls the transaction back, as a failed commit would.
		return fault.At("checkpoint-commit")
	})
	if err != nil {
		return catalogFailure(fmt.Errorf("settle UID %d in folder %d: %w", m.uid, m.folderID, err))
	}
	// Only a kill means anything here. The commit is done.
	_ = fault.At("after-checkpoint-commit")
	return nil
}

func changeProblem(ctx context.Context, tx *sql.Tx, m message, change problemChange, now int64) error {
	var received sql.NullInt64
	if m.received != nil {
		received = sql.NullInt64{Int64: m.received.UnixMilli(), Valid: true}
	}
	var err error
	switch change {
	case clearRetry:
		_, err = tx.ExecContext(ctx, `
DELETE FROM imap_message_problem
WHERE folder_id = ? AND uid_validity = ? AND uid = ? AND kind = 'waiting_for_retry'`,
			m.folderID, m.uidValidity, m.uid)
	case markWaiting:
		_, err = tx.ExecContext(ctx, `
INSERT INTO imap_message_problem (folder_id, uid_validity, uid, kind, size, received_at, recorded_at)
VALUES (?, ?, ?, 'waiting_for_retry', ?, ?, ?)
ON CONFLICT (folder_id, uid_validity, uid) DO NOTHING`,
			m.folderID, m.uidValidity, m.uid, m.size, received, now)
	case markTooLarge:
		_, err = tx.ExecContext(ctx, `
INSERT INTO imap_message_problem (folder_id, uid_validity, uid, kind, size, received_at, recorded_at)
VALUES (?, ?, ?, 'too_large', ?, ?, ?)
ON CONFLICT (folder_id, uid_validity, uid) DO UPDATE
SET kind = excluded.kind, size = excluded.size, received_at = excluded.received_at, recorded_at = excluded.recorded_at`,
			m.folderID, m.uidValidity, m.uid, m.size, received, now)
	case markRemoved:
		_, err = tx.ExecContext(ctx, `
UPDATE imap_message_problem SET kind = 'removed', recorded_at = ?
WHERE folder_id = ? AND uid_validity = ? AND uid = ? AND kind = 'waiting_for_retry'`,
			now, m.folderID, m.uidValidity, m.uid)
	}
	return err
}
