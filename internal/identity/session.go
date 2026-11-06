package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// SignOut ends the token's session by deleting its row (FR-005). A token with
// no row is already signed out, so it isn't an error.
func (id *Identity) SignOut(ctx context.Context, token string) error {
	tokenHash := sha256.Sum256([]byte(token))
	if _, err := id.db.Writer.ExecContext(ctx, "DELETE FROM session WHERE token_hash = ?", tokenHash[:]); err != nil {
		return fmt.Errorf("delete the session: %w", err)
	}
	return nil
}

// SetPassword replaces an account's password. In the same transaction it ends
// all of that account's sessions and lifts the address's sign-in block and
// failures (FR-004, FR-005).
func (id *Identity) SetPassword(ctx context.Context, address, password string) error {
	email := strings.ToLower(address)
	if err := checkPassword(password); err != nil {
		return err
	}
	// Hashed before the transaction, so the writer isn't held for it.
	hash, err := id.passwords.hash(ctx, password)
	if err != nil {
		return err
	}

	return sqlitedb.InTx(ctx, id.db.Writer, func(tx *sql.Tx) error {
		var accountID int64
		err := tx.QueryRowContext(ctx,
			"UPDATE account SET password_hash = ? WHERE email = ? RETURNING id", hash, email).Scan(&accountID)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNoAccount
		}
		if err != nil {
			return fmt.Errorf("store the password: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM session WHERE account_id = ?", accountID); err != nil {
			return fmt.Errorf("delete the account's sessions: %w", err)
		}
		return clearLimit(ctx, tx, addressKey(email))
	})
}
