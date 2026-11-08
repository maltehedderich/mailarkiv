package identity

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// The sign-in limit: 10 failures for one address within 15 minutes block it
// for the next 15 minutes, whether or not the account exists.
const (
	maxFailures   = 10
	failureWindow = 15 * time.Minute
	blockLength   = 15 * time.Minute
)

// addressKey is how the limit stores an address: the SHA-256 of its lowercase
// form, so the tables never hold the address itself.
func addressKey(address string) []byte {
	sum := sha256.Sum256([]byte(strings.ToLower(address)))
	return sum[:]
}

// refuseBlocked returns ErrSignInBlocked if the address has an unexpired block.
func (id *Identity) refuseBlocked(ctx context.Context, key []byte) error {
	var until int64
	err := id.db.Reader.QueryRowContext(ctx,
		"SELECT blocked_until FROM sign_in_block WHERE address_key = ?", key).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("read the sign-in block: %w", err)
	}
	if until > id.now().UnixMilli() {
		return ErrSignInBlocked
	}
	return nil
}

// recordFailure counts one failed sign-in for the address, and blocks the
// address at the tenth within the window. It also deletes old failures and
// expired blocks for every address, so a caller without an account can't grow
// these tables without limit.
//
// The failure counts even if the client has left: the password was checked, so
// leaving must not take the guess off the count.
func (id *Identity) recordFailure(ctx context.Context, key []byte) error {
	ctx = context.WithoutCancel(ctx)
	now := id.now()
	err := sqlitedb.InTx(ctx, id.db.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM sign_in_failure WHERE failed_at <= ?", now.Add(-failureWindow).UnixMilli()); err != nil {
			return fmt.Errorf("delete old failures: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM sign_in_block WHERE blocked_until <= ?", now.UnixMilli()); err != nil {
			return fmt.Errorf("delete expired blocks: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"INSERT INTO sign_in_failure (address_key, failed_at) VALUES (?, ?)", key, now.UnixMilli()); err != nil {
			return fmt.Errorf("add the failure: %w", err)
		}

		var failures int
		if err := tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sign_in_failure WHERE address_key = ?", key).Scan(&failures); err != nil {
			return fmt.Errorf("count failures: %w", err)
		}
		if failures < maxFailures {
			return nil
		}
		// The block replaces the failures that caused it.
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO sign_in_block (address_key, blocked_until) VALUES (?, ?)
			 ON CONFLICT (address_key) DO UPDATE SET blocked_until = excluded.blocked_until`,
			key, now.Add(blockLength).UnixMilli()); err != nil {
			return fmt.Errorf("add the block: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM sign_in_failure WHERE address_key = ?", key); err != nil {
			return fmt.Errorf("delete the address's failures: %w", err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("record a failed sign-in: %w", err)
	}
	return nil
}

// clearLimit deletes the address's failures and block, inside the caller's
// transaction.
func clearLimit(ctx context.Context, tx *sql.Tx, key []byte) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM sign_in_failure WHERE address_key = ?", key); err != nil {
		return fmt.Errorf("delete the address's failures: %w", err)
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM sign_in_block WHERE address_key = ?", key); err != nil {
		return fmt.Errorf("delete the address's block: %w", err)
	}
	return nil
}
