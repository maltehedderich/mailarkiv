package collect

import (
	"context"
	"errors"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
)

// assertUnreadable checks that the source needs attention because its
// password can't be read, found at since.
func assertUnreadable(t *testing.T, source Source, since time.Time) {
	t.Helper()
	if source.State != NeedsAttention || source.NextSyncAt != nil {
		t.Errorf("state = %v, next sync at %v, want needs_attention with none", source.State, source.NextSyncAt)
	}
	want := Failure{Category: PasswordUnreadable, Since: since}
	if source.Failure == nil || !source.Failure.Since.Equal(since) || source.Failure.Category != PasswordUnreadable ||
		source.Failure.FailedAttempts != nil || source.Failure.LastAt != nil || source.Failure.Reply != nil {
		t.Errorf("failure = %+v, want %+v", source.Failure, want)
	}
}

// Without a key no stored password opens. Each source needs attention from
// the start, none syncs, and the log says so once per source. A start with
// the key syncs them again.
func TestOpenWithoutKey(t *testing.T) {
	anna := newDouble(t, imaptest.ImplicitTLS)
	ben := newDoubleFor(t, imaptest.ImplicitTLS, "ben@example.test")
	f := newFixture(t, anna, ben)
	other := sourceFor(t, ben, "tls", "INBOX")
	other.Name, other.UserName = "Ben", "ben@example.test"
	ids := []int64{
		insertSource(t, f.deps(), sourceFor(t, anna, "tls", "INBOX")),
		insertSource(t, f.deps(), other),
	}

	deps := f.deps()
	deps.Key = nil
	collector, err := open(context.Background(), deps, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	collector.Start()
	for _, id := range ids {
		assertUnreadable(t, f.get(t, collector, id), testNow)
	}
	collector.Stop()

	f.assertLogged(t, "password unreadable", len(ids), map[string]any{"category": PasswordUnreadable.String()})
	var logged []int64
	for _, entry := range f.logEntries(t, "password unreadable") {
		id, _ := entry["source_id"].(float64)
		logged = append(logged, int64(id))
	}
	slices.Sort(logged)
	if !slices.Equal(logged, ids) {
		t.Errorf("password unreadable lines name sources %v, want %v", logged, ids)
	}
	for _, double := range []*imaptest.Server{anna, ben} {
		if commands := double.Commands(); len(commands) != 0 {
			t.Errorf("the double saw %+v", commands)
		}
	}
	for _, id := range ids {
		if row := f.sourceRow(t, id); row.FailedAttempts != 0 || row.LastFailureCategory.Valid {
			t.Errorf("source %d row = %+v, want no failed attempt", id, row)
		}
	}

	// The check runs again at each start.
	f.clock.Advance(time.Minute)
	collector = f.start(t)
	for _, id := range ids {
		waitForSuccess(t, collector, id, f.clock)
	}
}

// Add without a key refuses before it connects, and saves nothing.
func TestAddWithoutKey(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	deps := f.deps()
	deps.Key = nil
	collector, err := open(context.Background(), deps, testConfig)
	if err != nil {
		t.Fatal(err)
	}

	_, err = collector.Add(context.Background(), sourceFor(t, double, "tls", "INBOX"))

	if !errors.Is(err, ErrNoKey) {
		t.Errorf("Add = %v, want ErrNoKey", err)
	}
	if commands := double.Commands(); len(commands) != 0 {
		t.Errorf("the double saw %+v", commands)
	}
	var n int
	if err := f.db.Reader.QueryRow("SELECT COUNT(*) FROM imap_source").Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("%d sources were saved, want none", n)
	}
}

// A stored password that stops opening before a sign-in makes the source
// unreadable. That isn't a failed attempt, and nothing signs in.
func TestPasswordUnreadableAtSignIn(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	cfg := testConfig
	cfg.after = f.clock.After
	collector := f.startWith(t, cfg)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, f, collector, input)
	f.clock.waitForTimers(t, 1)

	// Another key sealed the password in the row now.
	path := filepath.Join(t.TempDir(), "other-key")
	if err := passwordkey.Create(path); err != nil {
		t.Fatal(err)
	}
	other, err := passwordkey.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Writer.Exec("UPDATE imap_source SET sealed_password = ? WHERE id = ?",
		other.Seal(input.Password, input.Host, input.UserName), id); err != nil {
		t.Fatal(err)
	}
	before := double.Logins()
	f.clock.Advance(5 * time.Minute)
	waitFor(t, "the password unreadable line", func() bool {
		return len(f.logEntries(t, "password unreadable")) == 1
	})

	assertUnreadable(t, f.get(t, collector, id), f.clock.Now())
	f.assertLogged(t, "password unreadable", 1,
		map[string]any{"source_id": float64(id), "category": PasswordUnreadable.String()})
	f.assertLogged(t, "sync failed", 0, nil)
	if row := f.sourceRow(t, id); row.FailedAttempts != 0 || row.FailingSince.Valid || row.LastFailureCategory.Valid {
		t.Errorf("row = %+v, want no failed attempt", row)
	}
	if n := double.Logins() - before; n != 0 {
		t.Errorf("%d LOGIN commands after the password stopped opening, want 0", n)
	}
}
