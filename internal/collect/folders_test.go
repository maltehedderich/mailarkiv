package collect

import (
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// A chosen folder that the server refuses to open is marked unavailable at
// each sync, and the others are still collected. The sync succeeds. Once
// the folder opens again, the mark goes.
func TestFolderUnavailable(t *testing.T) {
	double := newDouble(t, imaptest.ImplicitTLS)
	addFolders(t, double, "Archiv", "Projekte")
	day := time.Date(2026, 11, 4, 8, 0, 0, 0, time.UTC)
	first := []fixtureMessage{
		{folder: "INBOX", body: countBody("Inbox one"), received: day},
		{folder: "Archiv", body: countBody("Archiv one"), received: day},
		{folder: "Projekte", body: countBody("Projekte one"), received: day},
	}
	addMessages(t, double, first)
	f := newFixture(t, double)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX", "Archiv", "Projekte"))
	archiv := f.folderIDs(t, id)["Archiv"]

	if err := double.DeleteFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	later := []fixtureMessage{
		{folder: "INBOX", body: countBody("Inbox two"), received: day.Add(time.Hour)},
		{folder: "Projekte", body: countBody("Projekte two"), received: day.Add(time.Hour)},
	}
	addMessages(t, double, later)
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)
	refusedAt := f.clock.Now()

	want := hashesOf(first[0].body, first[1].body, first[2].body, later[0].body, later[1].body)
	if got := f.listed(t); !slices.Equal(got, want) {
		t.Errorf("listed %v, want the other folders' new messages too", got)
	}
	if row := f.sourceRow(t, id); row.FailedAttempts != 0 {
		t.Errorf("failed_attempts = %d, want 0", row.FailedAttempts)
	}
	unavailable := map[string]any{
		"level": "WARN", "source_id": float64(id), "folder_id": float64(archiv), "category": "folder_unavailable",
	}
	f.assertLogged(t, "folder unavailable", 1, unavailable)
	assertUnavailableSince(t, f, collector, id, &refusedAt)

	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)
	f.assertLogged(t, "folder unavailable", 2, unavailable)
	assertUnavailableSince(t, f, collector, id, &refusedAt)

	addFolders(t, double, "Archiv")
	again := []fixtureMessage{{folder: "Archiv", body: countBody("Archiv again"), received: day.Add(2 * time.Hour)}}
	addMessages(t, double, again)
	collector = f.syncAgain(t, collector)
	waitForSuccess(t, collector, id, f.clock)
	f.assertLogged(t, "folder unavailable", 2, unavailable)
	assertUnavailableSince(t, f, collector, id, nil)
	if got := f.listed(t); !slices.Contains(got, hashOf(again[0].body)) {
		t.Error("the re-created folder's message isn't listed")
	}
}

// assertUnavailableSince checks Archiv's mark, which is the second folder.
func assertUnavailableSince(t *testing.T, f *fixture, collector *Collector, id int64, want *time.Time) {
	t.Helper()
	got := f.get(t, collector, id).Folders[1]
	if got.Name != "Archiv" {
		t.Fatalf("the second folder is %q, want Archiv", got.Name)
	}
	switch {
	case want == nil && got.UnavailableSince != nil:
		t.Errorf("Archiv unavailable since %v, want available", got.UnavailableSince)
	case want != nil && (got.UnavailableSince == nil || !got.UnavailableSince.Equal(*want)):
		t.Errorf("Archiv unavailable since %v, want %v", got.UnavailableSince, *want)
	}
}
