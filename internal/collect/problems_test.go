package collect

import (
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// threeInInbox holds three messages for INBOX, received an hour apart.
func threeInInbox() []fixtureMessage {
	day := time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)
	return []fixtureMessage{
		{folder: "INBOX", body: countBody("One"), received: day},
		{folder: "INBOX", body: countBody("Two"), received: day.Add(time.Hour)},
		{folder: "INBOX", body: countBody("Three"), received: day.Add(2 * time.Hour)},
	}
}

// waitingRow is the retry row for mail[1], recorded at the clock's time.
func waitingRow(t *testing.T, double *imaptest.Server, mail []fixtureMessage, at time.Time) problem {
	t.Helper()
	uidValidity, err := double.UIDValidity("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	return problem{
		UIDValidity: int64(uidValidity),
		UID:         int64(mail[1].uid),
		Kind:        "waiting_for_retry",
		Size:        nullInt(int64(len(mail[1].body))),
		ReceivedAt:  nullInt(mail[1].received.UnixMilli()),
		RecordedAt:  at.UnixMilli(),
	}
}

func TestMessageUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name  string
		fault imaptest.Fault
	}{
		{"NIL body", imaptest.Fault{Body: imaptest.BodyNil}},
		{"no data", imaptest.Fault{Body: imaptest.BodyMissing}},
		{"empty body", imaptest.Fault{Body: imaptest.BodyEmpty}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			double := newDouble(t, imaptest.ImplicitTLS)
			mail := threeInInbox()
			addMessages(t, double, mail)
			if err := double.SetFault("INBOX", mail[1].uid, tt.fault); err != nil {
				t.Fatal(err)
			}
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
			inbox := f.folderIDs(t, id)["INBOX"]
			firstAt := f.clock.Now()

			if got, want := f.listed(t), hashesOf(mail[0].body, mail[2].body); !slices.Equal(got, want) {
				t.Errorf("listed %v, want UIDs 1 and 3", got)
			}
			waiting := waitingRow(t, double, mail, firstAt)
			if got := f.problems(t, inbox); !slices.Equal(got, []problem{waiting}) {
				t.Errorf("problems = %+v, want %+v", got, waiting)
			}
			if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
				t.Errorf("last_uid = %d, want 3", lastUID)
			}
			unavailable := map[string]any{
				"level": "WARN", "source_id": float64(id), "folder_id": float64(inbox), "category": "message_unavailable",
			}
			f.assertLogged(t, "message unavailable", 1, unavailable)

			// Still failing, the next sync tries again and keeps the row.
			collector = f.syncAgain(t, collector)
			waitForSuccess(t, collector, id, f.clock)
			if got := double.BodyFetches("INBOX", mail[1].uid); got != 2 {
				t.Errorf("UID 2 body fetched %d times, want twice", got)
			}
			if got := f.problems(t, inbox); !slices.Equal(got, []problem{waiting}) {
				t.Errorf("problems after a failed retry = %+v, want %+v", got, waiting)
			}
			f.assertLogged(t, "message unavailable", 2, unavailable)

			// Once it returns, the retry stores it and deletes the row.
			if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{}); err != nil {
				t.Fatal(err)
			}
			collector = f.syncAgain(t, collector)
			source := waitForSuccess(t, collector, id, f.clock)
			if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
				t.Errorf("listed %v after the retry, want all three", got)
			}
			if got := f.problems(t, inbox); len(got) != 0 {
				t.Errorf("problems after a successful retry = %+v, want none", got)
			}
			if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
				t.Errorf("last_uid after the retry = %d, want 3", lastUID)
			}
			if source.LastSuccess.Archived != 1 || source.Collected != 3 {
				t.Errorf("the retry archived %d and collected is %d, want 1 and 3", source.LastSuccess.Archived, source.Collected)
			}
			f.assertLogged(t, "message unavailable", 2, unavailable)
		})
	}
}

func TestMessageRemovedWhileWaiting(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	mail := threeInInbox()
	addMessages(t, double, mail)
	if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, double)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
	inbox := f.folderIDs(t, id)["INBOX"]
	waiting := waitingRow(t, double, mail, f.clock.Now())

	if err := double.DeleteMessage("INBOX", mail[1].uid); err != nil {
		t.Fatal(err)
	}
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)

	removed := waiting
	removed.Kind = "removed"
	removed.RecordedAt = f.clock.Now().UnixMilli()
	if got := f.problems(t, inbox); !slices.Equal(got, []problem{removed}) {
		t.Errorf("problems = %+v, want %+v", got, removed)
	}
	assertSourceEvents(t, f, opstatus.MessageRemoved, id, 1)
	notArchived := map[string]any{
		"level": "WARN", "source_id": float64(id), "folder_id": float64(inbox), "category": "message_removed",
	}
	f.assertLogged(t, "message not archived", 1, notArchived)

	// A removed message is never fetched or recorded again.
	fetches := fetchCommands(double)
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)
	if got := fetchCommands(double); got != fetches {
		t.Errorf("the next sync sent %d UID FETCH commands, want none", got-fetches)
	}
	if got := f.problems(t, inbox); !slices.Equal(got, []problem{removed}) {
		t.Errorf("problems after the next sync = %+v, want %+v", got, removed)
	}
	assertSourceEvents(t, f, opstatus.MessageRemoved, id, 1)
	f.assertLogged(t, "message not archived", 1, notArchived)
}

// A message that the search found but that is gone by its fetch was never
// waiting, so it leaves no record.
func TestMessageGoneAtFetch(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	mail := threeInInbox()
	addMessages(t, double, mail)
	hold := double.HoldBody("INBOX", mail[1].uid)
	go func() {
		<-hold.Reached()
		if err := double.DeleteMessage("INBOX", mail[1].uid); err != nil {
			t.Error(err)
		}
		hold.Release()
	}()
	f := newFixture(t, double)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
	inbox := f.folderIDs(t, id)["INBOX"]

	if got, want := f.listed(t), hashesOf(mail[0].body, mail[2].body); !slices.Equal(got, want) {
		t.Errorf("listed %v, want UIDs 1 and 3", got)
	}
	if got := f.problems(t, inbox); len(got) != 0 {
		t.Errorf("problems = %+v, want none", got)
	}
	if _, lastUID := f.checkpoint(t, inbox); lastUID != 3 {
		t.Errorf("last_uid = %d, want 3", lastUID)
	}
	f.assertLogged(t, "message unavailable", 0, nil)
}

func TestRenumberingDropsProblems(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	mail := threeInInbox()
	addMessages(t, double, mail)
	if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	if err := double.SetFault("INBOX", mail[2].uid, imaptest.Fault{Size: tooLarge}); err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, double)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
	inbox := f.folderIDs(t, id)["INBOX"]
	before := f.problems(t, inbox)
	if len(before) != 2 || before[0].Kind != "waiting_for_retry" || before[1].Kind != "too_large" {
		t.Fatalf("problems = %+v, want UID 2 waiting and UID 3 too large", before)
	}

	// The faults follow the messages to their new UIDs.
	renumbered, err := double.Renumber("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)

	uidValidity, err := double.UIDValidity("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	now := f.clock.Now().UnixMilli()
	want := []problem{
		{UIDValidity: int64(uidValidity), UID: int64(renumbered[3]), Kind: "too_large",
			Size: nullInt(tooLarge), ReceivedAt: nullInt(mail[2].received.UnixMilli()), RecordedAt: now},
		{UIDValidity: int64(uidValidity), UID: int64(renumbered[2]), Kind: "waiting_for_retry",
			Size: nullInt(int64(len(mail[1].body))), ReceivedAt: nullInt(mail[1].received.UnixMilli()), RecordedAt: now},
	}
	if got := f.problems(t, inbox); !slices.Equal(got, want) {
		t.Errorf("problems = %+v, want only the new numbering's %+v", got, want)
	}
}

// assertSourceEvents checks that the status lists want events of category,
// each naming the source.
func assertSourceEvents(t *testing.T, f *fixture, category opstatus.Category, sourceID int64, want int) {
	t.Helper()
	var n int
	for _, event := range f.status.Snapshot().Failures {
		if event.Category != category {
			continue
		}
		n++
		if event.SourceID == nil || *event.SourceID != sourceID || event.MessageID != nil || event.ContentHash != nil {
			t.Errorf("event = %+v, want source %d and no message", event, sourceID)
		}
	}
	if n != want {
		t.Errorf("%d %s events, want %d", n, category, want)
	}
}
