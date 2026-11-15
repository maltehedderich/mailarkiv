package collect

import (
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// heldStep is a body fetch the double holds, with the progress the running
// sync must show while it waits there.
type heldStep struct {
	folder          string
	uid             imap.UID
	found, archived int
}

// syncEntries counts the source's entries from the sync running at the
// clock's time.
func (f *fixture) syncEntries(t *testing.T, id int64) int {
	t.Helper()
	var n int
	if err := f.db.Reader.QueryRow(
		"SELECT COUNT(*) FROM source_observation WHERE imap_source_id = ? AND observed_at = ?",
		id, f.clock.Now().UnixMilli()).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// walkHolds waits at each held fetch in turn, checks the source's progress
// and this sync's entries there, and releases it.
func walkHolds(t *testing.T, f *fixture, collector *Collector, id int64, holds []*imaptest.Hold, steps []heldStep) {
	t.Helper()
	for i, step := range steps {
		select {
		case <-holds[i].Reached():
		case <-time.After(10 * time.Second):
			t.Fatalf("the sync never fetched %s UID %d", step.folder, step.uid)
		}
		source := f.get(t, collector, id)
		if source.State != Syncing || source.Progress == nil {
			t.Fatalf("at %s UID %d the source is %v with progress %v, want syncing", step.folder, step.uid, source.State, source.Progress)
		}
		if got := *source.Progress; got != (Progress{Archived: step.archived, Found: step.found}) {
			t.Errorf("at %s UID %d progress = %+v, want archived %d, found %d", step.folder, step.uid, got, step.archived, step.found)
		}
		if entries := f.syncEntries(t, id); entries != step.archived {
			t.Errorf("at %s UID %d the sync has %d entries, want %d", step.folder, step.uid, entries, step.archived)
		}
		holds[i].Release()
	}
}

func holdAll(double *imaptest.Server, steps []heldStep) []*imaptest.Hold {
	holds := make([]*imaptest.Hold, len(steps))
	for i, step := range steps {
		holds[i] = double.HoldBody(step.folder, step.uid)
	}
	return holds
}

// The archived count rises only after a message's entry is committed, and
// the found count is each opened folder's new UIDs plus its messages
// waiting for retry. A sync that times out partway through a literal keeps
// the count of what it stored and records the failure.
func TestProgressCountsDurable(t *testing.T) {
	t.Run("at each body fetch", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		addFolders(t, double, "Archiv")
		first := []fixtureMessage{
			{folder: "INBOX", body: countBody("M1")},
			{folder: "INBOX", body: countBody("M2")},
			{folder: "INBOX", body: countBody("M3")},
			{folder: "Archiv", body: countBody("A1")},
		}
		addMessages(t, double, first)
		m1, m2, m3, a1 := first[0].uid, first[1].uid, first[2].uid, first[3].uid
		if err := double.SetFault("INBOX", m2, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		id := insertSource(t, f.deps(), sourceFor(t, double, "tls", "INBOX", "Archiv"))

		// M2 comes back with no body, so it waits for retry and isn't
		// archived.
		steps := []heldStep{
			{"INBOX", m1, 3, 0},
			{"INBOX", m2, 3, 1},
			{"INBOX", m3, 3, 1},
			{"Archiv", a1, 4, 2},
		}
		holds := holdAll(double, steps)
		collector := f.start(t)
		walkHolds(t, f, collector, id, holds, steps)
		source := waitForSuccess(t, collector, id, f.clock)
		if source.LastSuccess.Archived != 3 || source.Collected != 3 {
			t.Errorf("first sync archived %d, collected %d, want 3 and 3", source.LastSuccess.Archived, source.Collected)
		}

		// The second sync handles INBOX's new message, then M2's retry, then
		// Archiv's new messages.
		if err := double.SetFault("INBOX", m2, imaptest.Fault{}); err != nil {
			t.Fatal(err)
		}
		second := []fixtureMessage{
			{folder: "INBOX", body: countBody("M4")},
			{folder: "Archiv", body: countBody("A2")},
			{folder: "Archiv", body: countBody("A3")},
		}
		addMessages(t, double, second)
		m4, a2, a3 := second[0].uid, second[1].uid, second[2].uid
		steps = []heldStep{
			{"INBOX", m4, 2, 0},
			{"INBOX", m2, 2, 1},
			{"Archiv", a2, 4, 2},
			{"Archiv", a3, 4, 3},
		}
		holds = holdAll(double, steps)
		collector = f.syncAgain(t, collector)
		walkHolds(t, f, collector, id, holds, steps)
		source = waitForSuccess(t, collector, id, f.clock)
		if source.LastSuccess.Archived != 4 || source.Collected != 7 || source.Progress != nil {
			t.Errorf("second sync archived %d, collected %d, progress %v, want 4, 7, and none",
				source.LastSuccess.Archived, source.Collected, source.Progress)
		}
	})

	t.Run("a timeout partway through a literal", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		// Archive reads a message's first 4 KiB before it writes to tmp/, so
		// the stalled message's first half must be longer.
		mail := []fixtureMessage{
			{folder: "INBOX", body: countBody("One")},
			{folder: "INBOX", body: countBody("Two")},
			{folder: "INBOX", body: sizedBody("Three", 64<<10)},
		}
		addMessages(t, double, mail)
		if err := double.SetFault("INBOX", mail[2].uid, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
			t.Fatal(err)
		}
		f := newFixture(t, double)
		id := insertSource(t, f.deps(), sourceFor(t, double, "tls", "INBOX"))
		collector := f.start(t)

		// Each message passes through tmp/. Once the third one's fetch has
		// begun, the first two are done, so a file there is its half literal.
		waitFor(t, "the stalled literal in tmp/", func() bool {
			return double.BodyFetches("INBOX", mail[2].uid) > 0 && len(f.tempFiles(t)) > 0
		})
		source := f.get(t, collector, id)
		if source.State != Syncing || source.Progress == nil || source.Progress.Archived != 2 {
			t.Errorf("mid-literal the source is %v with progress %+v, want syncing with 2 archived", source.State, source.Progress)
		}
		if entries := f.syncEntries(t, id); entries != 2 {
			t.Errorf("mid-literal the sync has %d entries, want 2", entries)
		}

		waitFor(t, "the sync to fail", func() bool { return f.get(t, collector, id).State == Retrying })
		source = f.get(t, collector, id)
		now := f.clock.Now()
		if source.Failure == nil || source.Failure.Category != TimedOut ||
			source.Failure.LastAt == nil || !source.Failure.LastAt.Equal(now) {
			t.Errorf("failure = %+v, want timed_out at %v", source.Failure, now)
		}
		if source.Progress != nil || source.LastSuccess != nil || source.Collected != 2 {
			t.Errorf("progress %+v, last success %+v, collected %d, want none, none, and 2",
				source.Progress, source.LastSuccess, source.Collected)
		}
		row := f.sourceRow(t, id)
		if row.LastFailureCategory != nullString(TimedOut.String()) || row.LastFailureAt != nullInt(now.UnixMilli()) {
			t.Errorf("last failure = %v at %v, want timed_out at %d", row.LastFailureCategory, row.LastFailureAt, now.UnixMilli())
		}
		if entries := f.syncEntries(t, id); entries != 2 {
			t.Errorf("the failed sync left %d entries, want 2", entries)
		}
		if temp := f.tempFiles(t); len(temp) != 0 {
			t.Errorf("tmp/ holds %v", temp)
		}
	})
}
