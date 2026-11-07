package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// fakeClock stands in for time, the one nondeterministic collaborator here.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

type fixture struct {
	identity *Identity
	db       *sqlitedb.DB
	clock    *fakeClock
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	clock := &fakeClock{now: time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)}
	return &fixture{identity: New(db, clock.Now), db: db, clock: clock}
}

const goodPassword = "correct horse battery"

func (f *fixture) create(t *testing.T, address, password string) {
	t.Helper()
	if err := f.identity.CreateAccount(context.Background(), address, password); err != nil {
		t.Fatalf("CreateAccount(%q): %v", address, err)
	}
}

func (f *fixture) storedAccounts(t *testing.T) map[string]string {
	t.Helper()
	rows, err := f.db.Reader.Query("SELECT email, password_hash FROM account")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	accounts := map[string]string{}
	for rows.Next() {
		var email, hash string
		if err := rows.Scan(&email, &hash); err != nil {
			t.Fatal(err)
		}
		accounts[email] = hash
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return accounts
}

// FR-044: one @ with text on both sides, no whitespace, stored in lowercase.
func TestCreateAccountAddresses(t *testing.T) {
	t.Run("stores the lowercase form", func(t *testing.T) {
		f := newFixture(t)
		f.create(t, "Admin@example.test", goodPassword)

		accounts := f.storedAccounts(t)
		if _, found := accounts["admin@example.test"]; !found || len(accounts) != 1 {
			t.Fatalf("stored accounts = %v, want only admin@example.test", accounts)
		}
	})

	t.Run("refuses a malformed address", func(t *testing.T) {
		f := newFixture(t)
		for _, address := range []string{
			"",
			"admin.example.test",
			"admin@@example.test",
			"admin@mail@example.test",
			"@example.test",
			"admin@",
			"@",
			"ad min@example.test",
			" admin@example.test",
			"admin@example.test ",
			"admin@example.test\n",
			"admin\t@example.test",
			"admin@example test",
		} {
			err := f.identity.CreateAccount(context.Background(), address, goodPassword)
			if !errors.Is(err, ErrInvalidAddress) {
				t.Errorf("CreateAccount(%q) = %v, want ErrInvalidAddress", address, err)
			}
		}
		if accounts := f.storedAccounts(t); len(accounts) != 0 {
			t.Fatalf("stored accounts = %v, want none", accounts)
		}
	})

	t.Run("refuses a second account whatever the capital letters", func(t *testing.T) {
		f := newFixture(t)
		f.create(t, "Admin@example.test", goodPassword)
		before := f.storedAccounts(t)

		err := f.identity.CreateAccount(context.Background(), "ADMIN@Example.Test", "a different password")
		if !errors.Is(err, ErrAccountExists) {
			t.Fatalf("CreateAccount = %v, want ErrAccountExists", err)
		}

		after := f.storedAccounts(t)
		if len(after) != 1 || after["admin@example.test"] != before["admin@example.test"] {
			t.Fatalf("the existing account changed: before %v, after %v", before, after)
		}
		if _, err := f.identity.SignIn(context.Background(), "admin@example.test", goodPassword); err != nil {
			t.Fatalf("the first password no longer signs in: %v", err)
		}
	})
}

// FR-002: at least 12 characters, counted as code points, stored in a form
// that can't be reversed.
func TestCreateAccountPasswords(t *testing.T) {
	tests := []struct {
		name     string
		password string
		want     error
	}{
		{"11 characters", "abcdefghijk", ErrPasswordTooShort},
		{"12 characters", "abcdefghijkl", nil},
		// 11 code points in 22 bytes: counting bytes would let this through.
		{"11 two-byte characters", strings.Repeat("ä", 11), ErrPasswordTooShort},
		{"12 two-byte characters", strings.Repeat("ä", 12), nil},
		{"4096 ASCII bytes", strings.Repeat("a", 4096), nil},
		{"4096 UTF-8 bytes", strings.Repeat("ä", 2048), nil},
		{"4097 ASCII bytes", strings.Repeat("a", 4097), ErrPasswordTooLong},
		{"4098 UTF-8 bytes", strings.Repeat("ä", 2049), ErrPasswordTooLong},
		{"invalid UTF-8", goodPassword + "\xff", ErrPasswordEncoding},
		{"empty", "", ErrPasswordTooShort},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			err := f.identity.CreateAccount(context.Background(), "admin@example.test", tt.password)
			if !errors.Is(err, tt.want) {
				t.Fatalf("CreateAccount = %v, want %v", err, tt.want)
			}
			stored := f.storedAccounts(t)
			if tt.want != nil && len(stored) != 0 {
				t.Fatalf("a refused password stored an account: %v", stored)
			}
		})
	}

	t.Run("stores Argon2id in PHC format without the password", func(t *testing.T) {
		f := newFixture(t)
		f.create(t, "admin@example.test", goodPassword)

		hash := f.storedAccounts(t)["admin@example.test"]
		// RFC 9106's second recommended settings, as ADR 0005 records.
		if !strings.HasPrefix(hash, "$argon2id$v=19$m=65536,t=3,p=4$") {
			t.Errorf("hash = %q, want Argon2id with m=65536,t=3,p=4 in PHC format", hash)
		}
		if parts := strings.Split(hash, "$"); len(parts) != 6 || parts[4] == "" || parts[5] == "" {
			t.Errorf("hash = %q, want a salt and a key after the settings", hash)
		}
		if strings.Contains(hash, goodPassword) {
			t.Errorf("hash %q contains the password", hash)
		}
	})
}

// FR-003: one error for a wrong password, an unknown address, and a
// malformed one. User Story 1 scenarios 5 and 13.
func TestSignIn(t *testing.T) {
	f := newFixture(t)
	f.create(t, "Admin@example.test", goodPassword)

	for _, address := range []string{"admin@example.test", "ADMIN@EXAMPLE.TEST", "Admin@example.test"} {
		if _, err := f.identity.SignIn(context.Background(), address, goodPassword); err != nil {
			t.Errorf("SignIn(%q) = %v, want success", address, err)
		}
	}

	failures := []struct {
		name     string
		address  string
		password string
	}{
		{"wrong password", "admin@example.test", "the wrong password"},
		{"unknown address", "nobody@example.test", goodPassword},
		{"malformed address", "not an address", goodPassword},
		{"empty", "", ""},
	}
	var messages []string
	for _, tt := range failures {
		token, err := f.identity.SignIn(context.Background(), tt.address, tt.password)
		if !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("%s: SignIn = %v, want ErrInvalidCredentials", tt.name, err)
			continue
		}
		if token != "" {
			t.Errorf("%s: SignIn returned a token", tt.name)
		}
		messages = append(messages, err.Error())
	}
	for _, message := range messages {
		if message != messages[0] {
			t.Errorf("errors differ: %q and %q", messages[0], message)
		}
	}
}

func TestSignInFailsWhenTheCatalogFails(t *testing.T) {
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err := f.identity.SignIn(context.Background(), "admin@example.test", goodPassword)
	if err == nil || errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("SignIn = %v, want a catalog error that isn't ErrInvalidCredentials", err)
	}
}

type sessionRow struct {
	tokenHash []byte
	accountID int64
	createdAt int64
	expiresAt int64
}

func (f *fixture) sessions(t *testing.T) []sessionRow {
	t.Helper()
	rows, err := f.db.Reader.Query("SELECT token_hash, account_id, created_at, expires_at FROM session")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var sessions []sessionRow
	for rows.Next() {
		var row sessionRow
		if err := rows.Scan(&row.tokenHash, &row.accountID, &row.createdAt, &row.expiresAt); err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, row)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return sessions
}

// FR-005: the catalog holds only the token's SHA-256, and a session ends 12
// hours after sign-in.
func TestSessions(t *testing.T) {
	f := newFixture(t)
	f.create(t, "Admin@example.test", goodPassword)
	signedInAt := f.clock.Now()

	token, err := f.identity.SignIn(context.Background(), "admin@example.test", goodPassword)
	if err != nil {
		t.Fatal(err)
	}

	sessions := f.sessions(t)
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want 1", len(sessions))
	}
	row := sessions[0]
	want := sha256.Sum256([]byte(token))
	if !bytes.Equal(row.tokenHash, want[:]) {
		t.Errorf("token_hash = %x, want the token's SHA-256 %x", row.tokenHash, want)
	}
	if bytes.Contains(row.tokenHash, []byte(token)) {
		t.Error("the row holds the token")
	}
	if row.createdAt != signedInAt.UnixMilli() {
		t.Errorf("created_at = %d, want %d", row.createdAt, signedInAt.UnixMilli())
	}
	if row.expiresAt != signedInAt.Add(12*time.Hour).UnixMilli() {
		t.Errorf("expires_at = %d, want created_at plus 12 hours, %d", row.expiresAt, signedInAt.Add(12*time.Hour).UnixMilli())
	}

	account, err := f.identity.LookupSession(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupSession = %v", err)
	}
	if account.Email != "admin@example.test" {
		t.Errorf("account email = %q, want admin@example.test", account.Email)
	}

	t.Run("refuses an unknown token", func(t *testing.T) {
		for _, unknown := range []string{"", "garbage", strings.ToUpper(token), token + "x"} {
			if _, err := f.identity.LookupSession(context.Background(), unknown); !errors.Is(err, ErrNoSession) {
				t.Errorf("LookupSession(%q) = %v, want ErrNoSession", unknown, err)
			}
		}
	})

	t.Run("each sign-in deletes expired sessions", func(t *testing.T) {
		f.clock.Advance(12 * time.Hour)
		if _, err := f.identity.LookupSession(context.Background(), token); !errors.Is(err, ErrNoSession) {
			t.Fatalf("LookupSession at 12 hours = %v, want ErrNoSession", err)
		}

		second, err := f.identity.SignIn(context.Background(), "admin@example.test", goodPassword)
		if err != nil {
			t.Fatal(err)
		}
		sessions := f.sessions(t)
		hash := sha256.Sum256([]byte(second))
		if len(sessions) != 1 || !bytes.Equal(sessions[0].tokenHash, hash[:]) {
			t.Fatalf("sessions after the second sign-in = %d, want only the new one", len(sessions))
		}
	})
}

func TestLookupSessionFailsWhenTheCatalogFails(t *testing.T) {
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	token, err := f.identity.SignIn(context.Background(), "admin@example.test", goodPassword)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.db.Close(); err != nil {
		t.Fatal(err)
	}

	_, err = f.identity.LookupSession(context.Background(), token)
	if err == nil || errors.Is(err, ErrNoSession) || errors.Is(err, sql.ErrNoRows) {
		t.Fatalf("LookupSession = %v, want a catalog error that isn't ErrNoSession", err)
	}
}
