package collect

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// A rejected sign-in stops the source until an administrator acts: no
// retry, and no sync at the next start. Another source keeps its schedule.
func TestSignInRejectedStops(t *testing.T) {
	rejecting := newDouble(t, imaptest.ImplicitTLS)
	healthy := newDoubleFor(t, imaptest.StartTLS, "ben@example.test")
	f := newFixture(t, rejecting, healthy)
	cfg := testConfig
	cfg.after = f.clock.After
	collector := f.startWith(t, cfg)

	other := sourceFor(t, healthy, "starttls", "INBOX")
	other.Name, other.UserName = "Ben", "ben@example.test"
	otherID := addSource(t, f, collector, other)
	id := addSource(t, f, collector, sourceFor(t, rejecting, "tls", "INBOX"))
	f.clock.waitForTimers(t, 2)

	// The stored password no longer works.
	rejecting.SetPassword("changed")
	before := rejecting.Logins()
	failures := len(f.logEntries(t, "sync failed"))
	f.clock.Advance(5 * time.Minute)
	if category := f.waitForSyncFailure(t, failures); category != SignInRejected.String() {
		t.Fatalf("the sync failed with %q, want sign_in_rejected", category)
	}
	waitForSuccess(t, collector, otherID, f.clock)

	check := func(t *testing.T, collector *Collector) {
		t.Helper()
		if n := rejecting.Logins() - before; n != 1 {
			t.Errorf("%d LOGIN commands since the password changed, want 1", n)
		}
		source := f.get(t, collector, id)
		if source.State != NeedsAttention {
			t.Errorf("state = %v, want needs_attention", source.State)
		}
		if source.Failure == nil || source.Failure.Category != SignInRejected {
			t.Errorf("failure = %+v, want sign_in_rejected", source.Failure)
		}
		if source.NextSyncAt != nil {
			t.Errorf("next sync at %v, want none", source.NextSyncAt)
		}
		if row := f.sourceRow(t, id); !row.SignInRejectedAt.Valid {
			t.Error("sign_in_rejected_at isn't set")
		}
	}
	check(t, collector)

	// Three retry waits pass. The other source's next sync, 5 minutes after
	// its last, shows that timers fire.
	f.clock.waitForTimers(t, 1)
	for _, wait := range []time.Duration{time.Minute, 2 * time.Minute, 4 * time.Minute} {
		f.clock.Advance(wait)
	}
	waitForSuccess(t, collector, otherID, f.clock)
	check(t, collector)

	// A new collector syncs the other source at start, and not this one.
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, otherID, f.clock)
	check(t, collector)
}

// NO [UNAVAILABLE] to LOGIN is a server error, which is retried.
func TestSignInUnavailableRetries(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	cfg := testConfig
	cfg.after = f.clock.After
	collector := f.startWith(t, cfg)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
	f.clock.waitForTimers(t, 1)

	double.SetHook(func(command imaptest.Command) error {
		if command.Name == "LOGIN" {
			return imaptest.No(imap.ResponseCodeUnavailable, "Try again later")
		}
		return nil
	})
	before := double.Logins()
	f.clock.Advance(5 * time.Minute)
	if category := f.waitForSyncFailure(t, 0); category != ServerError.String() {
		t.Fatalf("the sync failed with %q, want server_error", category)
	}

	f.clock.waitForTimers(t, 1)
	source := f.get(t, collector, id)
	if source.State != Retrying {
		t.Errorf("state = %v, want retrying", source.State)
	}
	if want := f.clock.Now().Add(time.Minute); source.NextSyncAt == nil || !source.NextSyncAt.Equal(want) {
		t.Errorf("next sync at %v, want %v", source.NextSyncAt, want)
	}
	if row := f.sourceRow(t, id); row.SignInRejectedAt.Valid {
		t.Error("sign_in_rejected_at is set")
	}

	f.clock.Advance(time.Minute)
	f.waitForSyncFailure(t, 1)
	if n := double.Logins() - before; n != 2 {
		t.Errorf("%d LOGIN commands since the server became unavailable, want 2", n)
	}
}
