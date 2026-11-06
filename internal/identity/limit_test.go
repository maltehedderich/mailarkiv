package identity

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"
)

const wrongPassword = "the wrong password"

// keyOf is the data model's address_key: the SHA-256 of the lowercased
// submitted address.
func keyOf(address string) []byte {
	sum := sha256.Sum256([]byte(strings.ToLower(address)))
	return sum[:]
}

func (f *fixture) signIn(address, password string) error {
	_, err := f.identity.SignIn(context.Background(), address, password)
	return err
}

// fail makes n sign-ins with a wrong password, and each must be refused as
// invalid credentials.
func (f *fixture) fail(t *testing.T, address string, n int) {
	t.Helper()
	for i := range n {
		if err := f.signIn(address, wrongPassword); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("failure %d for %q: SignIn = %v, want ErrInvalidCredentials", i+1, address, err)
		}
	}
}

// seedFailures stores n failures for the address, as if they had happened at
// the given time. A real failure costs a full Argon2id hash, so these tests set
// up counts and times here. TestSignInLimitAnswersAlike in internal/web and
// TestAccountSetPassword in cmd/mailarkiv make ten real failures end to end.
func (f *fixture) seedFailures(t *testing.T, address string, n int, at time.Time) {
	t.Helper()
	for range n {
		_, err := f.db.Writer.Exec("INSERT INTO sign_in_failure (address_key, failed_at) VALUES (?, ?)", keyOf(address), at.UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
	}
}

// seedBlock stores a block for the address, ending at the given time.
func (f *fixture) seedBlock(t *testing.T, address string, until time.Time) {
	t.Helper()
	_, err := f.db.Writer.Exec("INSERT INTO sign_in_block (address_key, blocked_until) VALUES (?, ?)", keyOf(address), until.UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) failureCount(t *testing.T, address string) int {
	t.Helper()
	var count int
	err := f.db.Reader.QueryRow("SELECT COUNT(*) FROM sign_in_failure WHERE address_key = ?", keyOf(address)).Scan(&count)
	if err != nil {
		t.Fatal(err)
	}
	return count
}

// blockedUntil returns the address's block row, if there is one.
func (f *fixture) blockedUntil(t *testing.T, address string) (time.Time, bool) {
	t.Helper()
	var until int64
	err := f.db.Reader.QueryRow("SELECT blocked_until FROM sign_in_block WHERE address_key = ?", keyOf(address)).Scan(&until)
	if errors.Is(err, sql.ErrNoRows) {
		return time.Time{}, false
	}
	if err != nil {
		t.Fatal(err)
	}
	return time.UnixMilli(until).UTC(), true
}

// FR-004, User Story 2 scenario 4: the tenth failure within 15 minutes blocks
// the address for 15 minutes, even with the correct password, and the same way
// whether or not the account exists.
func TestSignInLimit(t *testing.T) {
	for _, tt := range []struct {
		name  string
		known bool
	}{
		{"known address", true},
		{"unknown address", false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			f := newFixture(t)
			if tt.known {
				f.create(t, "Admin@example.test", goodPassword)
			}

			// Eight failures, then the ninth and tenth with other capital letters, all
			// within 15 minutes. Every form of the address shares one count.
			f.seedFailures(t, "admin@example.test", 8, f.clock.Now())
			f.clock.Advance(14 * time.Minute)
			f.fail(t, "Admin@Example.TEST", 1)
			if _, found := f.blockedUntil(t, "admin@example.test"); found {
				t.Fatal("blocked after nine failures")
			}
			f.clock.Advance(59 * time.Second)
			tenth := f.clock.Now()
			// The tenth failure is still an ordinary refusal. The next attempt is the
			// one that's blocked.
			f.fail(t, "admin@example.test", 1)

			until, found := f.blockedUntil(t, "admin@example.test")
			if !found {
				t.Fatal("no block after ten failures")
			}
			if want := tenth.Add(15 * time.Minute); !until.Equal(want) {
				t.Errorf("blocked_until = %v, want 15 minutes after the tenth failure, %v", until, want)
			}
			if got := f.failureCount(t, "admin@example.test"); got != 0 {
				t.Errorf("failures after the block = %d, want 0: the block replaces them", got)
			}

			// The correct password doesn't help.
			if err := f.signIn("admin@example.test", goodPassword); !errors.Is(err, ErrSignInBlocked) {
				t.Fatalf("SignIn with the correct password while blocked = %v, want ErrSignInBlocked", err)
			}

			// Blocked attempts neither count nor extend the block.
			f.clock.Advance(5 * time.Minute)
			for range 3 {
				if err := f.signIn("ADMIN@example.test", wrongPassword); !errors.Is(err, ErrSignInBlocked) {
					t.Fatalf("SignIn while blocked = %v, want ErrSignInBlocked", err)
				}
			}
			if after, _ := f.blockedUntil(t, "admin@example.test"); !after.Equal(until) {
				t.Errorf("blocked_until moved from %v to %v", until, after)
			}
			if got := f.failureCount(t, "admin@example.test"); got != 0 {
				t.Errorf("failures while blocked = %d, want 0", got)
			}

			// The block lasts 15 minutes and no longer.
			f.clock.Advance(until.Sub(f.clock.Now()) - time.Millisecond)
			if err := f.signIn("admin@example.test", goodPassword); !errors.Is(err, ErrSignInBlocked) {
				t.Fatalf("SignIn 1 ms before the block ends = %v, want ErrSignInBlocked", err)
			}
			f.clock.Advance(time.Millisecond)
			f.fail(t, "admin@example.test", 1)
			if got := f.failureCount(t, "admin@example.test"); got != 1 {
				t.Errorf("failures after the block = %d, want 1", got)
			}
			if _, found := f.blockedUntil(t, "admin@example.test"); found {
				t.Error("the expired block is still stored")
			}

			if tt.known {
				if err := f.signIn("admin@example.test", goodPassword); err != nil {
					t.Fatalf("SignIn after the block = %v, want success", err)
				}
			}
		})
	}
}

// FR-004: only failures within the last 15 minutes count, and older ones are
// deleted for every address.
func TestFailuresOlderThanTheWindowDontCount(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)

	start := f.clock.Now()
	f.seedFailures(t, "other@example.test", 1, start)
	f.seedFailures(t, "admin@example.test", 5, start)
	f.seedFailures(t, "admin@example.test", 4, start.Add(10*time.Minute))
	// The first five are now more than 15 minutes old.
	f.clock.Advance(15*time.Minute + time.Second)
	f.fail(t, "admin@example.test", 1)

	if _, found := f.blockedUntil(t, "admin@example.test"); found {
		t.Fatal("blocked after ten failures, five of them older than 15 minutes")
	}
	if got := f.failureCount(t, "admin@example.test"); got != 5 {
		t.Errorf("stored failures for admin = %d, want the 5 within 15 minutes", got)
	}
	if got := f.failureCount(t, "other@example.test"); got != 0 {
		t.Errorf("stored failures for another address = %d, want 0: old failures go for every address", got)
	}
	if err := f.signIn("admin@example.test", goodPassword); err != nil {
		t.Fatalf("SignIn = %v, want success", err)
	}
}

// Sign-in step 3: success deletes the address's failures, and only its own.
func TestSuccessClearsFailures(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)

	f.seedFailures(t, "other@example.test", 1, f.clock.Now())
	f.seedFailures(t, "admin@example.test", 3, f.clock.Now())
	if err := f.signIn("Admin@example.test", goodPassword); err != nil {
		t.Fatalf("SignIn = %v, want success", err)
	}

	if got := f.failureCount(t, "admin@example.test"); got != 0 {
		t.Errorf("failures for admin after success = %d, want 0", got)
	}
	if got := f.failureCount(t, "other@example.test"); got != 1 {
		t.Errorf("failures for another address = %d, want 1", got)
	}
}

// FR-004 and FR-029: the limit stores only the SHA-256 of the lowercased
// address, never the address.
func TestLimitStoresNoAddress(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	const address = "Marker.Address@Example.Test"

	f.fail(t, address, 1)

	rows, err := f.db.Reader.Query("SELECT address_key, failed_at FROM sign_in_failure")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var keys [][]byte
	for rows.Next() {
		var key []byte
		var failedAt int64
		if err := rows.Scan(&key, &failedAt); err != nil {
			t.Fatal(err)
		}
		keys = append(keys, key)
		if failedAt != f.clock.Now().UnixMilli() {
			t.Errorf("failed_at = %d, want %d", failedAt, f.clock.Now().UnixMilli())
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(keys) != 1 || !bytes.Equal(keys[0], keyOf(address)) {
		t.Fatalf("address keys = %x, want only %x", keys, keyOf(address))
	}
	if bytes.Contains(bytes.ToLower(keys[0]), []byte("marker")) {
		t.Fatal("the key holds the address")
	}
}

// FR-004, risk 13: guesses sent at once can't all pass the block check before
// the tenth failure is stored. Two hashes run at a time, so at most two guesses
// are checked once nine failures exist, and the rest are refused.
func TestBlockHoldsAgainstABurst(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	f.create(t, "admin@example.test", goodPassword)
	f.seedFailures(t, "admin@example.test", 9, f.clock.Now())

	const burst = 8
	results := make(chan error, burst)
	for range burst {
		go func() { results <- f.signIn("admin@example.test", wrongPassword) }()
	}

	var checked, blocked int
	for range burst {
		switch err := <-results; {
		case errors.Is(err, ErrInvalidCredentials):
			checked++
		case errors.Is(err, ErrSignInBlocked):
			blocked++
		default:
			t.Errorf("SignIn = %v, want ErrInvalidCredentials or ErrSignInBlocked", err)
		}
	}
	if checked < 1 || checked > maxConcurrentHashes {
		t.Errorf("%d guesses were checked and %d refused, want at most %d checked", checked, blocked, maxConcurrentHashes)
	}
}

// Risk 13: once a password has been checked, a client that leaves can't keep a
// wrong guess from counting. Leaving cancels the request's context.
func TestFailureCountsAfterTheClientLeaves(t *testing.T) {
	t.Parallel()
	f := newFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if err := f.identity.recordFailure(ctx, keyOf("admin@example.test")); err != nil {
		t.Fatalf("recordFailure = %v", err)
	}
	if got := f.failureCount(t, "admin@example.test"); got != 1 {
		t.Fatalf("failures = %d, want 1", got)
	}
}
