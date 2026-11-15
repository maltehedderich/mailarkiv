package collect

import (
	"context"
	"slices"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// tooLarge is one byte over the largest message collection stores.
const tooLarge = 150_000_001

// addFailing adds a source whose first sync fails, and returns the source and
// the failure's category.
func addFailing(t *testing.T, f *fixture, collector *Collector, input SourceInput) (int64, string) {
	t.Helper()
	before := len(f.logEntries(t, "sync failed"))
	added, err := collector.Add(context.Background(), input)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	return added.ID, f.waitForSyncFailure(t, before)
}

func TestMessageSizes(t *testing.T) {
	t.Run("RFC822.SIZE over the limit", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		mail := threeInInbox()
		addMessages(t, double, mail)
		if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Size: tooLarge}); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		collector := f.start(t)
		id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
		inbox := f.folderIDs(t, id)["INBOX"]

		uidValidity, err := double.UIDValidity("INBOX")
		if err != nil {
			t.Fatal(err)
		}
		row := problem{
			UIDValidity: int64(uidValidity),
			UID:         int64(mail[1].uid),
			Kind:        "too_large",
			Size:        nullInt(tooLarge),
			ReceivedAt:  nullInt(mail[1].received.UnixMilli()),
			RecordedAt:  f.clock.Now().UnixMilli(),
		}
		notArchived := map[string]any{
			"level": "WARN", "source_id": float64(id), "folder_id": float64(inbox), "category": "message_too_large",
		}
		check := func(t *testing.T) {
			t.Helper()
			if got := double.BodyFetches("INBOX", mail[1].uid); got != 0 {
				t.Errorf("UID 2 body fetched %d times, want never", got)
			}
			if got := f.problems(t, inbox); !slices.Equal(got, []problem{row}) {
				t.Errorf("problems = %+v, want %+v", got, row)
			}
			if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
				t.Errorf("last_uid = %d, want 3", lastUID)
			}
			if got, want := f.listed(t), hashesOf(mail[0].body, mail[2].body); !slices.Equal(got, want) {
				t.Errorf("listed %v, want UIDs 1 and 3", got)
			}
			assertSourceEvents(t, f, opstatus.MessageTooLarge, id, 1)
			f.assertLogged(t, "message not archived", 1, notArchived)
		}
		check(t)

		collector = f.syncAgain(t, collector)
		waitForSuccess(t, collector, id, f.clock)
		check(t)
	})

	t.Run("a literal announced over the limit", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		mail := threeInInbox()
		addMessages(t, double, mail)
		if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Body: imaptest.BodyCut, Announced: tooLarge}); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		collector := f.start(t)
		id, category := addFailing(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
		if category != "server_error" {
			t.Errorf("the sync failed with %q, want server_error", category)
		}
		inbox := f.folderIDs(t, id)["INBOX"]

		waiting := waitingRow(t, double, mail, f.clock.Now())
		if got := f.problems(t, inbox); !slices.Equal(got, []problem{waiting}) {
			t.Errorf("problems = %+v, want %+v", got, waiting)
		}
		if _, lastUID := f.checkpoint(t, inbox); lastUID != 2 {
			t.Errorf("last_uid = %d, want 2", lastUID)
		}
		if got, want := f.listed(t), hashesOf(mail[0].body); !slices.Equal(got, want) {
			t.Errorf("listed %v, want UID 1", got)
		}
		if temp := f.tempFiles(t); len(temp) != 0 {
			t.Errorf("tmp/ holds %v", temp)
		}
		f.assertLogged(t, "message unavailable", 1, map[string]any{
			"source_id": float64(id), "folder_id": float64(inbox), "category": "message_unavailable",
		})

		// The next sync takes UID 3, then retries UID 2.
		if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{}); err != nil {
			t.Fatal(err)
		}
		collector = f.syncAgain(t, collector)
		waitForSuccess(t, collector, id, f.clock)
		if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
			t.Errorf("listed %v after the next sync, want all three", got)
		}
		if got := f.problems(t, inbox); len(got) != 0 {
			t.Errorf("problems after the next sync = %+v, want none", got)
		}
		if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
			t.Errorf("last_uid after the next sync = %d, want 3", lastUID)
		}
	})

	t.Run("a literal that ends early", func(t *testing.T) {
		double := newDouble(t, imaptest.StartTLS)
		mail := threeInInbox()
		addMessages(t, double, mail)
		cut := imaptest.Fault{Body: imaptest.BodyCut, Announced: int64(len(mail[1].body) + 10)}
		if err := double.SetFault("INBOX", mail[1].uid, cut); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		collector := f.start(t)
		id, category := addFailing(t, f, collector, sourceFor(t, double, "starttls", "INBOX"))
		if category != "server_error" {
			t.Errorf("the sync failed with %q, want server_error", category)
		}
		inbox := f.folderIDs(t, id)["INBOX"]

		if got, want := f.listed(t), hashesOf(mail[0].body); !slices.Equal(got, want) {
			t.Errorf("listed %v, want UID 1", got)
		}
		if blobs := f.blobs(t); len(blobs) != 1 {
			t.Errorf("blobs/ holds %v, want UID 1's original alone", blobs)
		}
		if temp := f.tempFiles(t); len(temp) != 0 {
			t.Errorf("tmp/ holds %v", temp)
		}
		if _, lastUID := f.checkpoint(t, inbox); lastUID != 1 {
			t.Errorf("last_uid = %d, want 1", lastUID)
		}
		if got := f.problems(t, inbox); len(got) != 0 {
			t.Errorf("problems = %+v, want none", got)
		}

		if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{}); err != nil {
			t.Fatal(err)
		}
		collector = f.syncAgain(t, collector)
		waitForSuccess(t, collector, id, f.clock)
		if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
			t.Errorf("listed %v after the next sync, want all three", got)
		}
	})

	t.Run("a literal whose length differs from RFC822.SIZE", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		mail := threeInInbox()
		addMessages(t, double, mail)
		if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Size: int64(len(mail[1].body) + 100)}); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		collector := f.start(t)
		id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
		inbox := f.folderIDs(t, id)["INBOX"]

		if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
			t.Errorf("listed %v, want all three", got)
		}
		if got := f.problems(t, inbox); len(got) != 0 {
			t.Errorf("problems = %+v, want none", got)
		}
		if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
			t.Errorf("last_uid = %d, want 3", lastUID)
		}
	})
}
