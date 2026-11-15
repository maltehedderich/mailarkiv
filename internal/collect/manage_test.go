package collect

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// managed is a fixture whose collector waits on the fake clock, with a
// second, healthy source on its own double. That source's syncs show that
// time passed for every loop.
type managed struct {
	*fixture
	collector *Collector
	double    *imaptest.Server
	other     *imaptest.Server
	otherID   int64
}

// newManaged trusts the certificates of double, the other source's double,
// and more.
func newManaged(t *testing.T, double *imaptest.Server, more ...*imaptest.Server) *managed {
	t.Helper()
	other := newDoubleFor(t, imaptest.StartTLS, "ben@example.test")
	f := newFixture(t, append([]*imaptest.Server{double, other}, more...)...)
	cfg := testConfig
	cfg.after = f.clock.After
	m := &managed{fixture: f, collector: f.startWith(t, cfg), double: double, other: other}
	input := sourceFor(t, other, "starttls", "INBOX")
	input.Name, input.UserName = "Ben", "ben@example.test"
	m.otherID = addSource(t, f, m.collector, input)
	return m
}

// restart stops the collector and starts a new one on the same catalog a
// minute later, and waits for the other source's sync at start.
func (m *managed) restart(t *testing.T) {
	t.Helper()
	m.collector.Stop()
	m.clock.Advance(time.Minute)
	cfg := testConfig
	cfg.after = m.clock.After
	m.collector = m.startWith(t, cfg)
	waitForSuccess(t, m.collector, m.otherID, m.clock)
}

// passPeriods lets n schedule periods pass, and waits for the other
// source's sync at the end of each.
func (m *managed) passPeriods(t *testing.T, n int) {
	t.Helper()
	for range n {
		waitFor(t, "the other source's timer", func() bool {
			source := m.get(t, m.collector, m.otherID)
			return source.State == Waiting && source.NextSyncAt != nil && source.NextSyncAt.After(m.clock.Now())
		})
		m.clock.Advance(successWait)
		waitForSuccess(t, m.collector, m.otherID, m.clock)
	}
}

// heldMail is large enough that half of it is past capture's 4 KiB file
// check, so a stalled body leaves a temp file.
func heldMail(subject string) []byte {
	return append([]byte("From: ben@example.test\r\nSubject: "+subject+"\r\n\r\n"), bytes.Repeat([]byte("Body.\r\n"), 4<<10)...)
}

// collectionEntries returns the collection entries of the message with
// body's hash.
func (f *fixture) collectionEntries(t *testing.T, body []byte) []archive.CollectionEntry {
	t.Helper()
	page, err := f.archive.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ContentHash != hashOf(body) {
			continue
		}
		history, err := f.archive.History(context.Background(), item.ID)
		if err != nil {
			t.Fatal(err)
		}
		var entries []archive.CollectionEntry
		for _, entry := range history.Entries {
			if collected, ok := entry.(archive.CollectionEntry); ok {
				entries = append(entries, collected)
			}
		}
		return entries
	}
	t.Fatalf("no listed message holds %q", body)
	return nil
}

// sealedPassword reads the source's sealed_password column.
func (f *fixture) sealedPassword(t *testing.T, id int64) []byte {
	t.Helper()
	var sealed []byte
	if err := f.db.Reader.QueryRow("SELECT sealed_password FROM imap_source WHERE id = ?", id).Scan(&sealed); err != nil {
		t.Fatal(err)
	}
	return sealed
}

func assertPaused(t *testing.T, source Source, at time.Time) {
	t.Helper()
	if source.State != Paused {
		t.Errorf("state = %v, want paused", source.State)
	}
	if source.PausedAt == nil || !source.PausedAt.Equal(at) {
		t.Errorf("paused at %v, want %v", source.PausedAt, at)
	}
	if source.Progress != nil || source.NextSyncAt != nil || source.Failure != nil {
		t.Errorf("progress %+v, next sync %v, failure %+v, want none while paused",
			source.Progress, source.NextSyncAt, source.Failure)
	}
}

// Pausing during a message's transfer stops the sync before Pause returns.
// Nothing of the message is kept, checkpoints stay, and the source doesn't
// sync again, even after a restart.
func TestPauseStopsTheSync(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	held := heldMail("Held partway through")
	uid, err := double.AddMessage("INBOX", held, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := double.SetFault("INBOX", uid, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
		t.Fatal(err)
	}
	m := newManaged(t, double)
	listedBefore := m.listed(t)
	blobsBefore := m.blobs(t)
	added, err := m.collector.Add(ctx, sourceFor(t, double, "tls", "INBOX"))
	if err != nil {
		t.Fatal(err)
	}
	id := added.ID
	waitFor(t, "a temp file", func() bool { return len(m.tempFiles(t)) > 0 })
	inbox := m.folderIDs(t, id)["INBOX"]
	validity, lastUID := m.checkpoint(t, inbox)

	pausedAt := m.clock.Now()
	paused, err := m.collector.Pause(ctx, id)
	if err != nil {
		t.Fatalf("Pause: %v", err)
	}
	assertPaused(t, paused, pausedAt)
	if files := m.tempFiles(t); len(files) != 0 {
		t.Errorf("tmp/ holds %v after Pause returned, want nothing", files)
	}
	if got := m.listed(t); !slices.Equal(got, listedBefore) {
		t.Errorf("listed %v, want only the other source's %v", got, listedBefore)
	}
	if got := m.blobs(t); !slices.Equal(got, blobsBefore) {
		t.Errorf("blobs %v, want only the other source's %v", got, blobsBefore)
	}
	if gotValidity, gotUID := m.checkpoint(t, inbox); gotValidity != validity || gotUID != lastUID {
		t.Errorf("checkpoint = %v/%d, want %v/%d", gotValidity, gotUID, validity, lastUID)
	}
	// A sync that a deadline ended would have recorded a failure.
	if row := m.sourceRow(t, id); row.FailedAttempts != 0 || row.LastFailureCategory.Valid || row.LastSuccessAt.Valid {
		t.Errorf("row = %+v, want nothing recorded for a stopped sync", row)
	}

	// Pausing again changes nothing.
	m.clock.Advance(time.Minute)
	again, err := m.collector.Pause(ctx, id)
	if err != nil {
		t.Fatalf("second Pause: %v", err)
	}
	assertPaused(t, again, pausedAt)

	logins := double.Logins()
	m.passPeriods(t, 3)
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands over three periods while paused, want 0", n)
	}
	assertPaused(t, m.get(t, m.collector, id), pausedAt)

	m.restart(t)
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands after a restart while paused, want 0", n)
	}
	assertPaused(t, m.get(t, m.collector, id), pausedAt)
}

// Resume clears the pause and syncs at once. Resuming a source that isn't
// paused starts nothing.
func TestResume(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	if _, err := m.collector.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	m.clock.Advance(time.Minute)
	logins := double.Logins()

	resumed, err := m.collector.Resume(ctx, id)
	if err != nil {
		t.Fatalf("Resume: %v", err)
	}
	if resumed.State != Syncing || resumed.PausedAt != nil {
		t.Errorf("Resume answered state %v, paused at %v, want syncing and no pause", resumed.State, resumed.PausedAt)
	}
	waitForSuccess(t, m.collector, id, m.clock)
	if n := double.Logins() - logins; n != 1 {
		t.Errorf("%d LOGIN commands after Resume, want 1", n)
	}

	waiting := m.get(t, m.collector, id)
	again, err := m.collector.Resume(ctx, id)
	if err != nil {
		t.Fatalf("Resume of an active source: %v", err)
	}
	if again.State != Waiting || again.NextSyncAt == nil || !again.NextSyncAt.Equal(*waiting.NextSyncAt) {
		t.Errorf("Resume of an active source answered %v, next sync %v, want waiting until %v",
			again.State, again.NextSyncAt, waiting.NextSyncAt)
	}
	if n := double.Logins() - logins; n != 1 {
		t.Errorf("%d LOGIN commands after resuming an active source, want 1", n)
	}
}

// Sync now starts a sync at once from Waiting, Retrying, and a rejected
// sign-in. It refuses a source that is syncing or paused.
func TestSyncNow(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))

	syncNow := func(t *testing.T) {
		t.Helper()
		synced, err := m.collector.SyncNow(ctx, id)
		if err != nil {
			t.Fatalf("SyncNow: %v", err)
		}
		if synced.State != Syncing {
			t.Errorf("SyncNow answered %v, want syncing", synced.State)
		}
	}

	t.Run("waiting", func(t *testing.T) {
		m.clock.Advance(10 * time.Second)
		logins := double.Logins()
		syncNow(t)
		waitForSuccess(t, m.collector, id, m.clock)
		if n := double.Logins() - logins; n != 1 {
			t.Errorf("%d LOGIN commands, want 1", n)
		}
	})

	t.Run("retrying", func(t *testing.T) {
		double.SetHook(func(command imaptest.Command) error {
			if command.Name == "EXAMINE" {
				return imaptest.Bad("", "Try later")
			}
			return nil
		})
		m.clock.Advance(10 * time.Second)
		failures := len(m.logEntries(t, "sync failed"))
		syncNow(t)
		m.waitForSyncFailure(t, failures)
		if source := m.get(t, m.collector, id); source.State != Retrying {
			t.Fatalf("state = %v, want retrying", source.State)
		}
		double.SetHook(nil)

		// Ten seconds is less than the minute the retry waits.
		m.clock.Advance(10 * time.Second)
		syncNow(t)
		waitForSuccess(t, m.collector, id, m.clock)
	})

	t.Run("sign-in rejected", func(t *testing.T) {
		double.SetPassword("MARKER-PW-changed")
		m.clock.Advance(10 * time.Second)
		failures := len(m.logEntries(t, "sync failed"))
		syncNow(t)
		if category := m.waitForSyncFailure(t, failures); category != SignInRejected.String() {
			t.Fatalf("the sync failed with %q, want sign_in_rejected", category)
		}
		waitFor(t, "needs attention", func() bool { return m.get(t, m.collector, id).State == NeedsAttention })
		double.SetPassword(testPassword)

		m.clock.Advance(10 * time.Second)
		logins := double.Logins()
		syncNow(t)
		if row := m.sourceRow(t, id); row.SignInRejectedAt.Valid {
			t.Error("sign_in_rejected_at is still set after SyncNow returned")
		}
		waitForSuccess(t, m.collector, id, m.clock)
		if n := double.Logins() - logins; n != 1 {
			t.Errorf("%d LOGIN commands, want 1", n)
		}
	})

	t.Run("already syncing", func(t *testing.T) {
		body := countBody("Held")
		uid, err := double.AddMessage("INBOX", body, testNow)
		if err != nil {
			t.Fatal(err)
		}
		hold := double.HoldBody("INBOX", uid)
		m.clock.Advance(10 * time.Second)
		logins := double.Logins()
		syncNow(t)
		<-hold.Reached()

		if _, err := m.collector.SyncNow(ctx, id); !errors.Is(err, ErrAlreadySyncing) {
			t.Errorf("SyncNow during a sync = %v, want ErrAlreadySyncing", err)
		}
		hold.Release()
		source := waitForSuccess(t, m.collector, id, m.clock)
		if want := m.clock.Now().Add(successWait); source.NextSyncAt == nil || !source.NextSyncAt.Equal(want) {
			t.Errorf("next sync at %v, want %v", source.NextSyncAt, want)
		}
		if n := double.Logins() - logins; n != 1 {
			t.Errorf("%d LOGIN commands, want 1", n)
		}
		if got := double.BodyFetches("INBOX", uid); got != 1 {
			t.Errorf("the held message was fetched %d times, want 1", got)
		}
	})

	t.Run("paused", func(t *testing.T) {
		pausedAt := m.clock.Now()
		if _, err := m.collector.Pause(ctx, id); err != nil {
			t.Fatal(err)
		}
		logins := double.Logins()
		if _, err := m.collector.SyncNow(ctx, id); !errors.Is(err, ErrPaused) {
			t.Errorf("SyncNow while paused = %v, want ErrPaused", err)
		}
		assertPaused(t, m.get(t, m.collector, id), pausedAt)
		if n := double.Logins() - logins; n != 0 {
			t.Errorf("%d LOGIN commands, want 0", n)
		}
	})
}

// Without the key nothing can sign in, so Sync now is refused.
func TestSyncNowWithoutKey(t *testing.T) {
	f := newFixture(t)
	deps := f.deps()
	id := insertSource(t, deps, SourceInput{
		Name: "Anna", Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: testUser, Password: testPassword, Folders: []string{"INBOX"},
	})
	deps.Key = nil
	collector, err := open(context.Background(), deps, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	collector.Start()
	t.Cleanup(collector.Stop)
	if _, err := collector.SyncNow(context.Background(), id); !errors.Is(err, ErrNoKey) {
		t.Errorf("SyncNow without the key = %v, want ErrNoKey", err)
	}
}

// Every action on a source that doesn't exist answers ErrNotFound.
func TestManageUnknownSource(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	collector := f.start(t)
	const unknown = 42
	if _, err := collector.Pause(ctx, unknown); !errors.Is(err, ErrNotFound) {
		t.Errorf("Pause = %v, want ErrNotFound", err)
	}
	if _, err := collector.Resume(ctx, unknown); !errors.Is(err, ErrNotFound) {
		t.Errorf("Resume = %v, want ErrNotFound", err)
	}
	if _, err := collector.SyncNow(ctx, unknown); !errors.Is(err, ErrNotFound) {
		t.Errorf("SyncNow = %v, want ErrNotFound", err)
	}
	if err := collector.Remove(ctx, unknown); !errors.Is(err, ErrNotFound) {
		t.Errorf("Remove = %v, want ErrNotFound", err)
	}
	if _, err := collector.Edit(ctx, unknown, EditInput{Name: new("Neu")}); !errors.Is(err, ErrNotFound) {
		t.Errorf("Edit = %v, want ErrNotFound", err)
	}
	if _, err := collector.CheckSource(ctx, unknown, MailboxChange{}); !errors.Is(err, ErrNotFound) {
		t.Errorf("CheckSource = %v, want ErrNotFound", err)
	}
}

// An added folder syncs at once and collects every message in it. A removed
// folder stops being collected, and its messages stay listed. Added back,
// it goes on from its checkpoint. A folder never chosen is never opened.
func TestAddAndRemoveFolders(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	addFolders(t, double, "Archiv", "Spam")
	day := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	mail := []fixtureMessage{
		{folder: "INBOX", body: countBody("Inbox"), received: day},
		{folder: "Archiv", body: countBody("Archiv eins"), received: day},
		{folder: "Archiv", body: countBody("Archiv zwei"), received: day},
		{folder: "Spam", body: countBody("Spam"), received: day},
	}
	addMessages(t, double, mail)
	m := newManaged(t, double)
	otherListed := m.listed(t)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	withOthers := func(bodies ...[]byte) []string {
		hashes := append(hashesOf(bodies...), otherListed...)
		slices.Sort(hashes)
		return hashes
	}

	// Added: syncs at once and collects all of Archiv.
	m.clock.Advance(10 * time.Second)
	logins := double.Logins()
	edited, err := m.collector.Edit(ctx, id, EditInput{Folders: &[]string{"INBOX", "Archiv"}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.State != Syncing {
		t.Errorf("Edit answered %v, want syncing", edited.State)
	}
	waitForSuccess(t, m.collector, id, m.clock)
	if n := double.Logins() - logins; n != 1 {
		t.Errorf("%d LOGIN commands after adding a folder, want 1", n)
	}
	if got, want := m.listed(t), withOthers(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
		t.Errorf("listed %v, want INBOX and both of Archiv", got)
	}
	archiv := m.folderIDs(t, id)["Archiv"]
	validity, lastUID := m.checkpoint(t, archiv)

	// Removed: chosen is 0, and nothing new in Archiv is collected.
	removed, err := m.collector.Edit(ctx, id, EditInput{Folders: &[]string{"INBOX"}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if names := folderNames(removed.Folders); !slices.Equal(names, []string{"INBOX"}) {
		t.Errorf("folders = %v, want INBOX", names)
	}
	var chosen int
	if err := m.db.Reader.QueryRow("SELECT chosen FROM imap_folder WHERE id = ?", archiv).Scan(&chosen); err != nil {
		t.Fatal(err)
	}
	if chosen != 0 {
		t.Errorf("chosen = %d, want 0", chosen)
	}
	late := []fixtureMessage{
		{folder: "Archiv", body: countBody("Archiv drei"), received: day},
		{folder: "Archiv", body: countBody("Archiv vier"), received: day},
	}
	addMessages(t, double, late)
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForSuccess(t, m.collector, id, m.clock)
	if got, want := m.listed(t), withOthers(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
		t.Errorf("listed %v, want Archiv's first two kept and nothing new", got)
	}
	if gotValidity, gotUID := m.checkpoint(t, archiv); gotValidity != validity || gotUID != lastUID {
		t.Errorf("Archiv's checkpoint = %v/%d, want %v/%d", gotValidity, gotUID, validity, lastUID)
	}

	// Added back: the same row, from its checkpoint.
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.Edit(ctx, id, EditInput{Folders: &[]string{"INBOX", "Archiv"}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	source := waitForSuccess(t, m.collector, id, m.clock)
	if names := folderNames(source.Folders); !slices.Equal(names, []string{"INBOX", "Archiv"}) {
		t.Errorf("folders = %v, want INBOX, Archiv", names)
	}
	if got := m.folderIDs(t, id)["Archiv"]; got != archiv {
		t.Errorf("Archiv's row is %d, want %d", got, archiv)
	}
	want := withOthers(mail[0].body, mail[1].body, mail[2].body, late[0].body, late[1].body)
	if got := m.listed(t); !slices.Equal(got, want) {
		t.Errorf("listed %v, want Archiv's later two added", got)
	}
	for _, message := range append(mail[1:3:3], late...) {
		if n := double.BodyFetches("Archiv", message.uid); n != 1 {
			t.Errorf("Archiv UID %d was fetched %d times, want 1", message.uid, n)
		}
	}

	// While paused, an added folder waits for Resume.
	if _, err := m.collector.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	logins = double.Logins()
	paused, err := m.collector.Edit(ctx, id, EditInput{Folders: &[]string{"INBOX", "Archiv", "Leer"}})
	if err != nil {
		t.Fatalf("Edit while paused: %v", err)
	}
	if paused.State != Paused {
		t.Errorf("Edit while paused answered %v, want paused", paused.State)
	}
	m.passPeriods(t, 1)
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands after adding a folder while paused, want 0", n)
	}

	for _, command := range double.Commands() {
		if command.Name == "EXAMINE" && command.Mailbox == "Spam" {
			t.Errorf("Spam was opened, though never chosen: %+v", command)
		}
	}
}

func folderNames(folders []Folder) []string {
	names := make([]string, len(folders))
	for i, folder := range folders {
		names[i] = folder.Name
	}
	return names
}

// A folder renamed on the server shows as unavailable. Added under its new
// name, its messages gain entries for that folder and no new copy.
func TestRenamedFolderAddedUnderItsNewName(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	addFolders(t, double, "Projekte")
	day := time.Date(2026, 11, 2, 8, 0, 0, 0, time.UTC)
	mail := []fixtureMessage{
		{folder: "Projekte", body: countBody("Projekt eins"), received: day},
		{folder: "Projekte", body: countBody("Projekt zwei"), received: day},
	}
	addMessages(t, double, mail)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX", "Projekte"))
	blobs := m.blobs(t)

	// The server renames Projekte to Projekte 2026.
	if err := double.DeleteFolder("Projekte"); err != nil {
		t.Fatal(err)
	}
	addFolders(t, double, "Projekte 2026")
	for _, message := range mail {
		if _, err := double.AddMessage("Projekte 2026", message.body, message.received); err != nil {
			t.Fatal(err)
		}
	}
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	source := waitForSuccess(t, m.collector, id, m.clock)
	if source.Folders[1].Name != "Projekte" || source.Folders[1].UnavailableSince == nil {
		t.Fatalf("folders = %+v, want Projekte unavailable", source.Folders)
	}

	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.Edit(ctx, id, EditInput{Folders: &[]string{"INBOX", "Projekte", "Projekte 2026"}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	waitForSuccess(t, m.collector, id, m.clock)
	folders := m.folderIDs(t, id)
	for _, message := range mail {
		entries := m.collectionEntries(t, message.body)
		var inFolders []int64
		for _, entry := range entries {
			inFolders = append(inFolders, entry.FolderID)
		}
		slices.Sort(inFolders)
		if want := []int64{folders["Projekte"], folders["Projekte 2026"]}; !slices.Equal(inFolders, want) {
			t.Errorf("%q has entries in folders %v, want %v", message.body, inFolders, want)
		}
	}
	if got := m.blobs(t); !slices.Equal(got, blobs) {
		t.Errorf("blobs = %v, want %v: no new copy", got, blobs)
	}
}

// Removing a source stops its sync, then in one step removes it and its
// password and problem rows. History still names it, and the same mailbox
// can be added again.
func TestRemoveSource(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	mail := threeInInbox()
	addMessages(t, double, mail)
	if err := double.SetFault("INBOX", mail[1].uid, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	m := newManaged(t, double)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, m.fixture, m.collector, input)
	inbox := m.folderIDs(t, id)["INBOX"]
	if got := m.problems(t, inbox); len(got) != 1 {
		t.Fatalf("problems = %+v, want the message waiting for retry", got)
	}

	held := heldMail("Held at removal")
	heldUID, err := double.AddMessage("INBOX", held, testNow)
	if err != nil {
		t.Fatal(err)
	}
	if err := double.SetFault("INBOX", heldUID, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
		t.Fatal(err)
	}
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "a temp file", func() bool { return len(m.tempFiles(t)) > 0 })
	listed := m.listed(t)

	removedAt := m.clock.Now()
	if err := m.collector.Remove(ctx, id); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if files := m.tempFiles(t); len(files) != 0 {
		t.Errorf("tmp/ holds %v after Remove returned, want nothing", files)
	}
	if got := m.listed(t); !slices.Equal(got, listed) {
		t.Errorf("listed %v, want %v: messages stay", got, listed)
	}
	if _, err := m.collector.Get(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("Get after Remove = %v, want ErrNotFound", err)
	}
	sources, err := m.collector.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(sources) != 1 || sources[0].ID != m.otherID {
		t.Errorf("List = %+v, want only the other source", sources)
	}
	var removed int64
	var sealed []byte
	if err := m.db.Reader.QueryRow("SELECT removed_at, sealed_password FROM imap_source WHERE id = ?", id).
		Scan(&removed, &sealed); err != nil {
		t.Fatal(err)
	}
	if removed != removedAt.UnixMilli() || sealed != nil {
		t.Errorf("removed_at = %d, sealed_password = %x, want %d and NULL", removed, sealed, removedAt.UnixMilli())
	}
	if got := m.problems(t, inbox); len(got) != 0 {
		t.Errorf("problems = %+v, want none", got)
	}
	if row := m.sourceRow(t, id); row.FailedAttempts != 0 {
		t.Errorf("failed_attempts = %d, want 0: a stopped sync isn't a failure", row.FailedAttempts)
	}

	entries := m.collectionEntries(t, mail[0].body)
	if len(entries) != 1 {
		t.Fatalf("entries = %+v, want one", entries)
	}
	labels, err := NewLabeler(m.db).Labels(ctx, []int64{entries[0].FolderID})
	if err != nil {
		t.Fatal(err)
	}
	host := input.Host
	want := Label{Folder: "INBOX", Source: SourceLabel{ID: id, Name: "Anna", Host: host, UserName: testUser, Removed: true}}
	if got := labels[entries[0].FolderID]; got != want {
		t.Errorf("label = %+v, want %+v", got, want)
	}

	logins := double.Logins()
	m.passPeriods(t, 1)
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands after removal, want 0", n)
	}
	if err := m.collector.Remove(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("second Remove = %v, want ErrNotFound", err)
	}
	if _, err := m.collector.SyncNow(ctx, id); !errors.Is(err, ErrNotFound) {
		t.Errorf("SyncNow after Remove = %v, want ErrNotFound", err)
	}

	// The same mailbox can be added again, as a new source.
	if err := double.SetFault("INBOX", heldUID, imaptest.Fault{}); err != nil {
		t.Fatal(err)
	}
	again, err := m.collector.Add(ctx, input)
	if err != nil {
		t.Fatalf("Add after Remove: %v", err)
	}
	if again.ID == id {
		t.Errorf("the mailbox came back under its old ID %d", id)
	}
}

// A second source for the same mailbox is refused with the first one's ID,
// whatever the letters' case, before any connection.
func TestDuplicateSource(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	collector := f.start(t)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, f, collector, input)
	byName := insertSource(t, f.deps(), SourceInput{
		Name: "Server", Host: "localhost", Port: input.Port, Security: "tls",
		UserName: "ben@example.test", Password: testPassword, Folders: []string{"INBOX"},
	})
	commands := len(double.Commands())

	for _, tt := range []struct {
		name     string
		host     string
		userName string
		want     int64
	}{
		{"user name", input.Host, "ANNA@Example.TEST", id},
		{"host", "LocalHost", "Ben@example.test", byName},
	} {
		t.Run(tt.name, func(t *testing.T) {
			duplicate := input
			duplicate.Name, duplicate.Host, duplicate.UserName = "Zweite", tt.host, tt.userName
			_, err := collector.Add(ctx, duplicate)
			var exists *SourceExistsError
			if !errors.As(err, &exists) || exists.ID != tt.want {
				t.Errorf("Add = %v, want source_exists with ID %d", err, tt.want)
			}
		})
	}
	if got := double.Commands(); len(got) != commands {
		t.Errorf("the double saw %+v after the duplicates, want nothing", got[commands:])
	}
	if n := f.sourceCount(t); n != 2 {
		t.Errorf("%d sources, want 2", n)
	}
}

// A duplicate that gets past the first check is refused when saved, with
// the same error.
func TestDuplicateSourceAtSave(t *testing.T) {
	f := newFixture(t)
	input := SourceInput{
		Name: "Anna", Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: testUser, Password: testPassword, Folders: []string{"INBOX"},
	}
	id := insertSource(t, f.deps(), input)
	input.UserName = "Anna@Example.Test"
	source, err := parseInput(input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = (&Collector{deps: f.deps()}).save(context.Background(), source)
	var exists *SourceExistsError
	if !errors.As(err, &exists) || exists.ID != id {
		t.Errorf("save = %v, want source_exists with ID %d", err, id)
	}
}
