// Package identity owns accounts, passwords, and sessions, and the account,
// session, sign_in_failure, and sign_in_block tables. The web server and the
// command line both call it, so they share one set of rules.
package identity

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

const (
	// minPasswordLength is counted in Unicode code points (FR-002).
	minPasswordLength = 12

	// sessionLifetime runs from sign-in and is never extended (FR-005).
	sessionLifetime = 12 * time.Hour

	tokenLength = 32
)

var (
	ErrInvalidAddress   = errors.New("that email address isn't valid")
	ErrPasswordTooShort = errors.New("passwords must have at least 12 characters")
	ErrAccountExists    = errors.New("an account with that email address already exists")

	// ErrInvalidCredentials is the one answer to a wrong password, an unknown
	// address, and a malformed address, so a caller can't tell which (FR-003).
	ErrInvalidCredentials = errors.New("that email address and password don't match")

	// ErrSignInBlocked means the address had 10 failed sign-ins within 15
	// minutes, and the block hasn't ended (FR-004).
	ErrSignInBlocked = errors.New("too many failed sign-ins for this address")

	// ErrNoAccount means no account has the address.
	ErrNoAccount = errors.New("no account has that email address")

	// ErrNoSession means the token names no unexpired session.
	ErrNoSession = errors.New("no valid session")
)

// Account is a signed-in administrator.
type Account struct {
	ID int64
	// Email is the stored, lowercase form.
	Email string
}

// Identity is safe for concurrent use.
type Identity struct {
	db        *sqlitedb.DB
	now       func() time.Time
	passwords *passwords
}

// New returns the module over an open catalog. now is the clock, which tests
// replace.
func New(db *sqlitedb.DB, now func() time.Time) *Identity {
	return &Identity{db: db, now: now, passwords: newPasswords()}
}

// ParseAddress applies the address rule (FR-044): exactly one @, text on both
// sides, and no whitespace. It returns the stored form, which is
// strings.ToLower of the address given, and the only form ever stored.
func ParseAddress(address string) (string, error) {
	local, domain, found := strings.Cut(address, "@")
	if !found || local == "" || domain == "" || strings.Contains(domain, "@") ||
		strings.ContainsFunc(address, unicode.IsSpace) || !utf8.ValidString(address) {
		return "", ErrInvalidAddress
	}
	return strings.ToLower(address), nil
}

// checkPassword applies the password rule (FR-002).
func checkPassword(password string) error {
	if utf8.RuneCountInString(password) < minPasswordLength {
		return ErrPasswordTooShort
	}
	return nil
}

// CreateAccount adds an administrator. A second account for the same address,
// whatever its capital letters, is refused, and the existing one doesn't change.
func (id *Identity) CreateAccount(ctx context.Context, address, password string) error {
	email, err := ParseAddress(address)
	if err != nil {
		return err
	}
	if err := checkPassword(password); err != nil {
		return err
	}
	hash, err := id.passwords.hash(ctx, password)
	if err != nil {
		return err
	}

	result, err := id.db.Writer.ExecContext(ctx,
		`INSERT INTO account (email, password_hash, created_at) VALUES (?, ?, ?)
		 ON CONFLICT (email) DO NOTHING`,
		email, hash, id.now().UnixMilli())
	if err != nil {
		return fmt.Errorf("store the account: %w", err)
	}
	added, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("store the account: %w", err)
	}
	if added == 0 {
		return ErrAccountExists
	}
	return nil
}

// SignIn checks the password and starts a session. It returns the session
// token, which only the caller's cookie holds. Every address, valid or not,
// takes the same steps (FR-003, FR-004).
func (id *Identity) SignIn(ctx context.Context, address, password string) (string, error) {
	email := strings.ToLower(address)
	key := addressKey(email)

	// A blocked address is refused before any password work and without waiting
	// for a slot. The attempt neither counts nor extends the block.
	if err := id.refuseBlocked(ctx, key); err != nil {
		return "", err
	}

	// Guesses sent at once all pass that check, so each looks again once it has a
	// slot, and keeps the slot until its failure is stored. So at most
	// maxConcurrentHashes guesses are checked after the ninth failure.
	if err := id.passwords.acquire(ctx); err != nil {
		return "", err
	}
	defer id.passwords.release()
	if err := id.refuseBlocked(ctx, key); err != nil {
		return "", err
	}

	var accountID int64
	stored := dummyHash
	err := id.db.Reader.QueryRowContext(ctx,
		"SELECT id, password_hash FROM account WHERE email = ?", email).Scan(&accountID, &stored)
	known := err == nil
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", fmt.Errorf("read the account: %w", err)
	}

	// An unknown address still pays for a full check against dummyHash.
	match, err := id.passwords.verify(stored, password)
	if err != nil {
		return "", err
	}
	if !known || !match {
		if err := id.recordFailure(ctx, key); err != nil {
			return "", err
		}
		return "", ErrInvalidCredentials
	}
	return id.startSession(ctx, accountID, stored, key)
}

// startSession stores a new session for the account and deletes the address's
// failed sign-ins. Only the token's SHA-256 reaches the catalog. Expired
// sessions go in the same transaction, so the table can't grow without limit.
//
// checkedHash is the hash the password matched. If the account's password
// changed during the check, the session isn't stored and the answer is
// ErrInvalidCredentials, because setting a password ends every session (FR-005).
func (id *Identity) startSession(ctx context.Context, accountID int64, checkedHash string, key []byte) (string, error) {
	random := make([]byte, tokenLength)
	// crypto/rand.Read never returns an error. It crashes the program instead.
	_, _ = rand.Read(random)
	token := base64.RawURLEncoding.EncodeToString(random)
	tokenHash := sha256.Sum256([]byte(token))
	now := id.now()

	err := sqlitedb.InTx(ctx, id.db.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM session WHERE expires_at <= ?", now.UnixMilli()); err != nil {
			return fmt.Errorf("delete expired sessions: %w", err)
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM sign_in_failure WHERE address_key = ?", key); err != nil {
			return fmt.Errorf("delete the address's failures: %w", err)
		}
		result, err := tx.ExecContext(ctx,
			`INSERT INTO session (token_hash, account_id, created_at, expires_at)
			 SELECT ?, id, ?, ? FROM account WHERE id = ? AND password_hash = ?`,
			tokenHash[:], now.UnixMilli(), now.Add(sessionLifetime).UnixMilli(), accountID, checkedHash)
		if err != nil {
			return err
		}
		added, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if added == 0 {
			return ErrInvalidCredentials
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("store the session: %w", err)
	}
	return token, nil
}

// LookupSession returns the account a token belongs to. A missing, unknown, or
// expired token gives ErrNoSession. Any other error means the catalog failed,
// and the session may still be valid.
func (id *Identity) LookupSession(ctx context.Context, token string) (Account, error) {
	tokenHash := sha256.Sum256([]byte(token))
	var account Account
	err := id.db.Reader.QueryRowContext(ctx,
		`SELECT account.id, account.email
		 FROM session JOIN account ON account.id = session.account_id
		 WHERE session.token_hash = ? AND session.expires_at > ?`,
		tokenHash[:], id.now().UnixMilli()).Scan(&account.ID, &account.Email)
	if errors.Is(err, sql.ErrNoRows) {
		return Account{}, ErrNoSession
	}
	if err != nil {
		return Account{}, fmt.Errorf("read the session: %w", err)
	}
	return account, nil
}
