package collect

import (
	"context"
	"fmt"

	"github.com/emersion/go-imap/v2"
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

// moveCheckpoint records that every UID up to uid is archived. It never
// moves the checkpoint back.
func (c *Collector) moveCheckpoint(ctx context.Context, folderID int64, uid imap.UID) error {
	_, err := c.deps.DB.Writer.ExecContext(ctx,
		"UPDATE imap_folder SET last_uid = ? WHERE id = ? AND last_uid < ?", uid, folderID, uid)
	if err != nil {
		return fmt.Errorf("move folder %d's checkpoint: %w", folderID, err)
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
