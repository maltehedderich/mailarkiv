package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Edit changes a source's name, port, security, password, and folders
// together. A new port, security, or password signs in with the new values
// first, within the check limit, and nothing changes if that fails. Host and
// user name can't change. It returns an *InvalidFieldsError, ErrNotFound,
// ErrNoKey, or a *CheckFailedError.
//
// A saved password clears a rejected sign-in and the unreadable mark. It,
// and an added folder, sync at once unless the source is paused. A sync
// that is running goes on with the old values, and the next one starts when
// it ends.
func (c *Collector) Edit(ctx context.Context, id int64, in EditInput) (Source, error) {
	change, err := parseEdit(in)
	if err != nil {
		return Source{}, err
	}
	stored, err := c.loadStored(ctx, id)
	if err != nil {
		return Source{}, err
	}
	box := stored.box
	if signsIn(stored.box, change.change) {
		if box, err = c.mailboxFor(stored, change.change); err != nil {
			return Source{}, err
		}
		check, cancel := context.WithTimeout(ctx, c.cfg.check)
		session, err := signIn(check, c.cfg, c.deps.Roots, box)
		cancel()
		if err != nil {
			return Source{}, checkFailed(err)
		}
		_ = session.logout()
	}

	c.manage.Lock()
	defer c.manage.Unlock()
	var row adminRow
	var added bool
	err = sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, `
SELECT paused_at IS NOT NULL, sign_in_rejected_at IS NOT NULL, failed_attempts
FROM imap_source WHERE id = ? AND removed_at IS NULL`, id).Scan(&row.paused, &row.rejected, &row.failedAttempts)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}
		// A nil interface binds as NULL, which keeps the stored password.
		var sealed any
		if change.change.password != nil {
			sealed = c.deps.Key.Seal(*change.change.password, box.host, box.userName)
			row.rejected = false
		}
		if _, err := tx.ExecContext(ctx, `
UPDATE imap_source SET name = COALESCE(?, name), port = ?, security = ?,
       sealed_password = COALESCE(?, sealed_password),
       sign_in_rejected_at = CASE WHEN ? IS NULL THEN sign_in_rejected_at END
WHERE id = ?`, change.name, box.port, box.security.String(), sealed, sealed, id); err != nil {
			return err
		}
		if change.folders != nil {
			if added, err = setFolders(ctx, tx, id, change.folders); err != nil {
				return err
			}
		}
		return nil
	})
	if errors.Is(err, ErrNotFound) {
		return Source{}, err
	}
	if err != nil {
		return Source{}, fmt.Errorf("edit source %d: %w", id, err)
	}

	saved := change.change.password != nil
	if saved {
		c.clearUnreadable(id)
	}
	if !row.paused && (saved || added) {
		c.mu.Lock()
		r, running := c.runners[id]
		wake := func() {}
		if running {
			wake = c.requestSync(r)
		}
		_, unreadable := c.unreadable[id]
		c.mu.Unlock()
		defer wake()
		if !running && !row.rejected && !unreadable {
			defer c.startRunner(id, row.failedAttempts)()
		}
	}
	return c.Get(ctx, id)
}

// signsIn reports whether change needs a sign-in: a password, or a port or
// security other than the stored one.
func signsIn(stored mailbox, change mailboxChange) bool {
	return change.password != nil ||
		change.port != nil && *change.port != stored.port ||
		change.security != nil && *change.security != stored.security
}
