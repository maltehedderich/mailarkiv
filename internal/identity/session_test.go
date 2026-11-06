package identity

import (
	"context"
	"crypto/sha256"
	"errors"
	"testing"
	"time"
)

const newPassword = "a brand new password"

func (f *fixture) startSession(t *testing.T, address, password string) string {
	t.Helper()
	token, err := f.identity.SignIn(context.Background(), address, password)
	if err != nil {
		t.Fatalf("SignIn(%q) = %v", address, err)
	}
	return token
}

func (f *fixture) assertNoSession(t *testing.T, token, why string) {
	t.Helper()
	if _, err := f.identity.LookupSession(context.Background(), token); !errors.Is(err, ErrNoSession) {
		t.Fatalf("LookupSession %s = %v, want ErrNoSession", why, err)
	}
}

func (f *fixture) assertSession(t *testing.T, token, email string) {
	t.Helper()
	account, err := f.identity.LookupSession(context.Background(), token)
	if err != nil {
		t.Fatalf("LookupSession = %v, want %s's session", err, email)
	}
	if account.Email != email {
		t.Fatalf("session belongs to %q, want %q", account.Email, email)
	}
}

// FR-005: sign-out deletes the session's row and no other.
func TestSignOut(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	first := f.startSession(t, "admin@example.test", goodPassword)
	second := f.startSession(t, "admin@example.test", goodPassword)

	if err := f.identity.SignOut(context.Background(), first); err != nil {
		t.Fatalf("SignOut = %v", err)
	}

	f.assertNoSession(t, first, "after sign-out")
	f.assertSession(t, second, "admin@example.test")
	sessions := f.sessions(t)
	secondHash := sha256.Sum256([]byte(second))
	if len(sessions) != 1 || string(sessions[0].tokenHash) != string(secondHash[:]) {
		t.Fatalf("sessions = %d, want only the second", len(sessions))
	}

	for _, token := range []string{first, "never issued", ""} {
		if err := f.identity.SignOut(context.Background(), token); err != nil {
			t.Errorf("SignOut(%q) with no row = %v, want nil", token, err)
		}
	}
}

// FR-005: a session ends 12 hours after sign-in, and using it doesn't extend it.
func TestSessionEndsAfterTwelveHours(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	token := f.startSession(t, "admin@example.test", goodPassword)
	expiresAt := f.sessions(t)[0].expiresAt

	for _, step := range []time.Duration{time.Hour, 6 * time.Hour, 5*time.Hour - time.Millisecond} {
		f.clock.Advance(step)
		f.assertSession(t, token, "admin@example.test")
	}
	if got := f.sessions(t)[0].expiresAt; got != expiresAt {
		t.Fatalf("expires_at moved from %d to %d after use", expiresAt, got)
	}

	f.clock.Advance(time.Millisecond)
	f.assertNoSession(t, token, "at 12 hours")
}

// FR-004, FR-005, User Story 2 scenario 7: setting a password ends that
// account's sessions and lifts its address's block and failures.
func TestSetPassword(t *testing.T) {
	t.Run("ends the account's sessions and lifts its block", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.create(t, "Admin@example.test", goodPassword)
		f.create(t, "other@example.test", goodPassword)
		admin := f.startSession(t, "admin@example.test", goodPassword)
		other := f.startSession(t, "other@example.test", goodPassword)
		f.seedFailures(t, "other@example.test", 1, f.clock.Now())
		f.seedBlock(t, "admin@example.test", f.clock.Now().Add(15*time.Minute))
		if err := f.signIn("admin@example.test", goodPassword); !errors.Is(err, ErrSignInBlocked) {
			t.Fatalf("SignIn before the new password = %v, want ErrSignInBlocked", err)
		}

		if err := f.identity.SetPassword(context.Background(), "ADMIN@Example.test", newPassword); err != nil {
			t.Fatalf("SetPassword = %v", err)
		}

		f.assertNoSession(t, admin, "after a new password")
		f.assertSession(t, other, "other@example.test")
		if _, found := f.blockedUntil(t, "admin@example.test"); found {
			t.Error("the block is still stored")
		}
		if got := f.failureCount(t, "other@example.test"); got != 1 {
			t.Errorf("failures for another address = %d, want 1", got)
		}
		if err := f.signIn("admin@example.test", goodPassword); !errors.Is(err, ErrInvalidCredentials) {
			t.Errorf("SignIn with the old password = %v, want ErrInvalidCredentials", err)
		}
		if err := f.signIn("admin@example.test", newPassword); err != nil {
			t.Fatalf("SignIn with the new password = %v, want success at once", err)
		}
		// The failure with the old password above is gone again after success.
		if got := f.failureCount(t, "admin@example.test"); got != 0 {
			t.Errorf("failures for admin = %d, want 0", got)
		}
	})

	t.Run("deletes the address's failures", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.create(t, "admin@example.test", goodPassword)
		f.seedFailures(t, "admin@example.test", 3, f.clock.Now())

		if err := f.identity.SetPassword(context.Background(), "Admin@example.test", newPassword); err != nil {
			t.Fatalf("SetPassword = %v", err)
		}
		if got := f.failureCount(t, "admin@example.test"); got != 0 {
			t.Errorf("failures = %d, want 0", got)
		}
	})

	t.Run("refuses a short password and changes nothing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.create(t, "admin@example.test", goodPassword)
		session := f.startSession(t, "admin@example.test", goodPassword)
		before := f.storedAccounts(t)

		err := f.identity.SetPassword(context.Background(), "admin@example.test", "elevenchars")
		if !errors.Is(err, ErrPasswordTooShort) {
			t.Fatalf("SetPassword = %v, want ErrPasswordTooShort", err)
		}
		if after := f.storedAccounts(t); after["admin@example.test"] != before["admin@example.test"] {
			t.Error("the hash changed")
		}
		f.assertSession(t, session, "admin@example.test")
	})

	t.Run("refuses an unknown account and changes nothing", func(t *testing.T) {
		t.Parallel()
		f := newFixture(t)
		f.create(t, "admin@example.test", goodPassword)
		session := f.startSession(t, "admin@example.test", goodPassword)
		f.seedFailures(t, "nobody@example.test", 1, f.clock.Now())

		err := f.identity.SetPassword(context.Background(), "nobody@example.test", newPassword)
		if !errors.Is(err, ErrNoAccount) {
			t.Fatalf("SetPassword = %v, want ErrNoAccount", err)
		}
		// The transaction rolled back, so the address's failure is still there.
		if got := f.failureCount(t, "nobody@example.test"); got != 1 {
			t.Errorf("failures for the unknown address = %d, want 1", got)
		}
		f.assertSession(t, session, "admin@example.test")
		if accounts := f.storedAccounts(t); len(accounts) != 1 {
			t.Errorf("accounts = %v, want only admin", accounts)
		}
	})
}

// FR-005: a sign-in that checked the old password while a new one was being set
// can't start a session that outlives the change.
func TestSessionCantStartFromAReplacedPassword(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	var accountID int64
	var checkedHash string
	err := f.db.Reader.QueryRow("SELECT id, password_hash FROM account").Scan(&accountID, &checkedHash)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.identity.SetPassword(context.Background(), "admin@example.test", newPassword); err != nil {
		t.Fatalf("SetPassword = %v", err)
	}

	_, err = f.identity.startSession(context.Background(), accountID, checkedHash, keyOf("admin@example.test"))
	if !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("startSession with the old hash = %v, want ErrInvalidCredentials", err)
	}
	if sessions := f.sessions(t); len(sessions) != 0 {
		t.Fatalf("sessions = %d, want none", len(sessions))
	}
}
