package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// adminRow is what an administrator's action needs from a source's row.
type adminRow struct {
	paused         bool
	rejected       bool
	failedAttempts int
}

// readAdminRow reads an active source's row, or returns ErrNotFound.
func (c *Collector) readAdminRow(ctx context.Context, id int64) (adminRow, error) {
	var row adminRow
	err := c.deps.DB.Reader.QueryRowContext(ctx, `
SELECT paused_at IS NOT NULL, sign_in_rejected_at IS NOT NULL, failed_attempts
FROM imap_source WHERE id = ? AND removed_at IS NULL`, id).Scan(&row.paused, &row.rejected, &row.failedAttempts)
	if errors.Is(err, sql.ErrNoRows) {
		return adminRow{}, ErrNotFound
	}
	if err != nil {
		return adminRow{}, fmt.Errorf("read source %d: %w", id, err)
	}
	return row, nil
}

// Pause stops a running sync and waits for it to end, then marks the source
// paused. A paused source doesn't sync, even after a restart, until Resume.
// Pausing a paused source changes nothing.
func (c *Collector) Pause(ctx context.Context, id int64) (Source, error) {
	c.manage.Lock()
	defer c.manage.Unlock()
	if _, err := c.readAdminRow(ctx, id); err != nil {
		return Source{}, err
	}
	c.stopRunner(id)
	if _, err := c.deps.DB.Writer.ExecContext(ctx,
		"UPDATE imap_source SET paused_at = COALESCE(paused_at, ?) WHERE id = ?", c.deps.Now().UnixMilli(), id); err != nil {
		return Source{}, fmt.Errorf("pause source %d: %w", id, err)
	}
	return c.Get(ctx, id)
}

// Resume clears the pause and syncs at once, unless the source needs an
// administrator for another reason. Resuming a source that isn't paused
// starts nothing.
func (c *Collector) Resume(ctx context.Context, id int64) (Source, error) {
	c.manage.Lock()
	defer c.manage.Unlock()
	row, err := c.readAdminRow(ctx, id)
	if err != nil {
		return Source{}, err
	}
	if row.paused {
		if _, err := c.deps.DB.Writer.ExecContext(ctx, "UPDATE imap_source SET paused_at = NULL WHERE id = ?", id); err != nil {
			return Source{}, fmt.Errorf("resume source %d: %w", id, err)
		}
		if !row.rejected && !c.isUnreadable(id) {
			c.startRunner(id, row.failedAttempts)
		}
	}
	return c.Get(ctx, id)
}

// SyncNow clears a rejected sign-in and the unreadable mark, and syncs at
// once. It returns ErrPaused, ErrAlreadySyncing, or ErrNoKey, and starts
// nothing then.
func (c *Collector) SyncNow(ctx context.Context, id int64) (Source, error) {
	c.manage.Lock()
	defer c.manage.Unlock()
	row, err := c.readAdminRow(ctx, id)
	if err != nil {
		return Source{}, err
	}
	if row.paused {
		return Source{}, ErrPaused
	}
	c.mu.Lock()
	r, running := c.runners[id]
	syncing := running && r.syncing
	c.mu.Unlock()
	if syncing {
		return Source{}, ErrAlreadySyncing
	}
	if c.deps.Key == nil {
		return Source{}, ErrNoKey
	}

	if running {
		c.mu.Lock()
		c.requestSync(r)
		c.mu.Unlock()
		return c.Get(ctx, id)
	}
	if row.rejected {
		if _, err := c.deps.DB.Writer.ExecContext(ctx,
			"UPDATE imap_source SET sign_in_rejected_at = NULL WHERE id = ?", id); err != nil {
			return Source{}, fmt.Errorf("clear source %d's rejected sign-in: %w", id, err)
		}
	}
	// The sync opens the password again, and marks it unreadable if it still
	// doesn't open.
	c.clearUnreadable(id)
	c.startRunner(id, row.failedAttempts)
	return c.Get(ctx, id)
}

// Remove stops a running sync and waits for it to end. Then, in one step,
// it marks the source removed, deletes its password, and deletes its problem
// rows. Its folders and history entries stay, so the history still names
// it.
func (c *Collector) Remove(ctx context.Context, id int64) error {
	c.manage.Lock()
	defer c.manage.Unlock()
	if _, err := c.readAdminRow(ctx, id); err != nil {
		return err
	}
	c.stopRunner(id)
	c.clearUnreadable(id)
	err := sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"UPDATE imap_source SET removed_at = ?, sealed_password = NULL WHERE id = ?",
			c.deps.Now().UnixMilli(), id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"DELETE FROM imap_message_problem WHERE folder_id IN (SELECT id FROM imap_folder WHERE source_id = ?)", id)
		return err
	})
	if err != nil {
		return fmt.Errorf("remove source %d: %w", id, err)
	}
	return nil
}

func (c *Collector) isUnreadable(id int64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, unreadable := c.unreadable[id]
	return unreadable
}

func (c *Collector) clearUnreadable(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.unreadable, id)
}

// setFolders makes folders the source's chosen folders. A removed folder
// keeps its row and checkpoint, so its messages stay listed, and adding it
// back continues from the checkpoint. A new folder's row comes after every
// other, so the source syncs it last. It reports whether any folder became
// chosen.
func setFolders(ctx context.Context, tx *sql.Tx, sourceID int64, folders []string) (added bool, err error) {
	rows, err := tx.QueryContext(ctx, "SELECT name FROM imap_folder WHERE source_id = ? AND chosen = 1", sourceID)
	if err != nil {
		return false, err
	}
	chosen := map[string]bool{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return false, err
		}
		chosen[name] = true
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return false, err
	}

	if _, err := tx.ExecContext(ctx, "UPDATE imap_folder SET chosen = 0 WHERE source_id = ?", sourceID); err != nil {
		return false, err
	}
	for _, name := range folders {
		if _, err := tx.ExecContext(ctx, `
INSERT INTO imap_folder (source_id, name, chosen) VALUES (?, ?, 1)
ON CONFLICT (source_id, name) DO UPDATE SET chosen = 1`, sourceID, name); err != nil {
			return false, err
		}
		added = added || !chosen[name]
	}
	return added, nil
}
