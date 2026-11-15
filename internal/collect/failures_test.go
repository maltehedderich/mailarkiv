package collect

import (
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// assertSyncFailedLog checks the newest "sync failed" line. next is the
// next attempt's time, or nil when none is scheduled.
func assertSyncFailedLog(t *testing.T, f *fixture, id int64, category Category, attempts int, next *time.Time) {
	t.Helper()
	entries := f.logEntries(t, "sync failed")
	if len(entries) == 0 {
		t.Fatal("no sync failed line")
	}
	entry := entries[len(entries)-1]
	for key, want := range map[string]any{
		"level": "WARN", "source_id": float64(id), "category": category.String(), "failed_attempts": float64(attempts),
	} {
		if entry[key] != want {
			t.Errorf("sync failed line has %s = %v, want %v", key, entry[key], want)
		}
	}
	logged, ok := entry["next_attempt_at"].(string)
	switch {
	case next == nil && entry["next_attempt_at"] != nil:
		t.Errorf("sync failed line has next_attempt_at = %v, want none", entry["next_attempt_at"])
	case next != nil && !ok:
		t.Errorf("sync failed line has next_attempt_at = %v, want %v", entry["next_attempt_at"], *next)
	case next != nil:
		at, err := time.Parse(time.RFC3339Nano, logged)
		if err != nil || !at.Equal(*next) {
			t.Errorf("sync failed line has next_attempt_at = %q, want %v", logged, *next)
		}
	}
	for _, field := range []string{"error", "reply"} {
		if _, ok := entry[field]; ok {
			t.Errorf("sync failed line has %s: %v", field, entry)
		}
	}
}

// badExamine answers EXAMINE with BAD and text.
func badExamine(text string) imaptest.Hook {
	return func(command imaptest.Command) error {
		if command.Name == "EXAMINE" {
			return imaptest.Bad("", text)
		}
		return nil
	}
}

func TestFailureRecord(t *testing.T) {
	t.Run("failures in a row, then a success", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		mail := threeInInbox()
		addMessages(t, double, mail[:1])
		f := newFixture(t, double)
		collector := f.start(t)
		added, err := collector.Add(context.Background(), sourceFor(t, double, "tls", "INBOX"))
		if err != nil {
			t.Fatal(err)
		}
		if added.State != Syncing || added.NextSyncAt != nil {
			t.Errorf("Add answered %v with next sync at %v, want syncing with none", added.State, added.NextSyncAt)
		}
		id := added.ID
		waitForSuccess(t, collector, id, f.clock)

		long := "Start of the reply " + strings.Repeat("abcd", 1241) + " end of the reply"
		double.SetHook(badExamine(long))
		collector = f.syncAgain(t, collector)
		f.waitForSyncFailure(t, 0)
		failedAt := f.clock.Now()

		row := f.sourceRow(t, id)
		if row.FailedAttempts != 1 || row.FailingSince != nullInt(failedAt.UnixMilli()) ||
			row.LastFailureCategory != nullString(ServerError.String()) || row.LastFailureAt != nullInt(failedAt.UnixMilli()) {
			t.Errorf("after one failure the row is %+v", row)
		}
		stored := row.LastServerReply.String
		if n := utf8.RuneCountInString(stored); n > 1000 || !strings.HasPrefix(stored, long[:400]) || !strings.HasSuffix(stored, long[len(long)-400:]) {
			t.Errorf("a reply of %d characters was stored as %d characters: %q", len(long), n, stored)
		}
		next := failedAt.Add(time.Minute)
		assertSyncFailedLog(t, f, id, ServerError, 1, &next)
		source := f.get(t, collector, id)
		if source.State != Retrying || source.NextSyncAt == nil || !source.NextSyncAt.Equal(next) {
			t.Errorf("source is %v with next sync at %v, want retrying at %v", source.State, source.NextSyncAt, next)
		}
		if source.Failure == nil || source.Failure.Category != ServerError || !source.Failure.Since.Equal(failedAt) ||
			source.Failure.FailedAttempts == nil || *source.Failure.FailedAttempts != 1 ||
			source.Failure.LastAt == nil || !source.Failure.LastAt.Equal(failedAt) ||
			source.Failure.Reply == nil || *source.Failure.Reply != stored {
			t.Errorf("failure = %+v", source.Failure)
		}

		double.SetHook(badExamine("Mailbox locked"))
		collector = f.syncAgain(t, collector)
		f.waitForSyncFailure(t, 1)
		row = f.sourceRow(t, id)
		if row.FailedAttempts != 2 || row.FailingSince != nullInt(failedAt.UnixMilli()) ||
			row.LastFailureAt != nullInt(f.clock.Now().UnixMilli()) || row.LastServerReply != nullString("Mailbox locked") {
			t.Errorf("after two failures the row is %+v", row)
		}
		next = f.clock.Now().Add(2 * time.Minute)
		assertSyncFailedLog(t, f, id, ServerError, 2, &next)

		double.SetHook(nil)
		addMessages(t, double, mail[1:])
		collector = f.syncAgain(t, collector)
		waitForSuccess(t, collector, id, f.clock)
		want := sourceRow{
			LastSuccessAt:       nullInt(f.clock.Now().UnixMilli()),
			LastSuccessArchived: nullInt(2),
		}
		if row := f.sourceRow(t, id); row != want {
			t.Errorf("after a success the row is %+v, want %+v", row, want)
		}
		source = f.get(t, collector, id)
		if want := f.clock.Now().Add(5 * time.Minute); source.NextSyncAt == nil || !source.NextSyncAt.Equal(want) {
			t.Errorf("waiting with next sync at %v, want %v", source.NextSyncAt, want)
		}
		if source.Failure != nil {
			t.Errorf("failure = %+v after a success, want none", source.Failure)
		}
	})

	t.Run("a rejected sign-in", func(t *testing.T) {
		double := newDouble(t, imaptest.StartTLS)
		f := newFixture(t, double)
		collector := f.start(t)
		id := addSource(t, f, collector, sourceFor(t, double, "starttls", "INBOX"))

		double.SetHook(func(command imaptest.Command) error {
			if command.Name == "LOGIN" {
				return imaptest.No(imap.ResponseCodeAuthenticationFailed, "Invalid credentials")
			}
			return nil
		})
		collector = f.syncAgain(t, collector)
		f.waitForSyncFailure(t, 0)
		at := nullInt(f.clock.Now().UnixMilli())

		row := f.sourceRow(t, id)
		want := sourceRow{
			FailedAttempts:      1,
			FailingSince:        at,
			LastFailureCategory: nullString(SignInRejected.String()),
			LastFailureAt:       at,
			LastServerReply:     nullString("[AUTHENTICATIONFAILED] Invalid credentials"),
			SignInRejectedAt:    at,
			LastSuccessAt:       row.LastSuccessAt,
			LastSuccessArchived: row.LastSuccessArchived,
		}
		if row != want {
			t.Errorf("row = %+v, want %+v", row, want)
		}
		assertSyncFailedLog(t, f, id, SignInRejected, 1, nil)
		if source := f.get(t, collector, id); source.NextSyncAt != nil {
			t.Errorf("next sync at %v while it needs attention, want none", source.NextSyncAt)
		}
	})
}
