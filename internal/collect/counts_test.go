package collect

import (
	"bytes"
	"context"
	"database/sql"
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

func countBody(subject string) []byte {
	return []byte("From: ben@example.test\r\nSubject: " + subject + "\r\n\r\nBody\r\n")
}

// addSource adds a source and waits for its first sync to succeed.
func addSource(t *testing.T, f *fixture, collector *Collector, input SourceInput) int64 {
	t.Helper()
	added, err := collector.Add(context.Background(), input)
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitForSuccess(t, collector, added.ID, f.clock)
	return added.ID
}

// countScene is the state a count case leaves behind.
type countScene struct {
	f         *fixture
	collector *Collector
	sources   []int64
}

type countCase struct {
	name string
	run  func(t *testing.T) countScene
	// messages, blobs, and entries count the whole archive. entries includes
	// imports.
	messages, blobs, entries int
	// collected is each source's count, in the order run returns them.
	collected []int
}

var countCases = []countCase{
	{
		name: "a second sync with no new mail",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			mail := []fixtureMessage{{folder: "INBOX", body: countBody("One")}, {folder: "INBOX", body: countBody("Two")}}
			addMessages(t, double, mail)
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
			fetches := fetchCommands(double)

			collector = f.syncAgain(t, collector)
			waitForSuccess(t, collector, id, f.clock)
			for _, message := range mail {
				if got := double.BodyFetches("INBOX", message.uid); got != 1 {
					t.Errorf("UID %d body fetched %d times, want once", message.uid, got)
				}
			}
			if got := fetchCommands(double); got != fetches {
				t.Errorf("the second sync sent %d UID FETCH commands, want none", got-fetches)
			}
			return countScene{f, collector, []int64{id}}
		},
		messages: 2, blobs: 2, entries: 2, collected: []int{2},
	},
	{
		name: "a renumbered folder that gained a message",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			addMessages(t, double, []fixtureMessage{
				{folder: "INBOX", body: countBody("One")},
				{folder: "INBOX", body: countBody("Two")},
				{folder: "INBOX", body: countBody("Three")},
			})
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
			inbox := f.folderIDs(t, id)["INBOX"]
			before, _ := f.checkpoint(t, inbox)

			renumbered, err := double.Renumber("INBOX")
			if err != nil {
				t.Fatal(err)
			}
			fourth, err := double.AddMessage("INBOX", countBody("Four"), time.Time{})
			if err != nil {
				t.Fatal(err)
			}
			collector = f.syncAgain(t, collector)
			source := waitForSuccess(t, collector, id, f.clock)

			for _, uid := range []imap.UID{renumbered[1], renumbered[2], renumbered[3], fourth} {
				if got := double.BodyFetches("INBOX", uid); got != 1 {
					t.Errorf("new UID %d body fetched %d times, want once", uid, got)
				}
			}
			uidValidity, lastUID := f.checkpoint(t, inbox)
			want, err := double.UIDValidity("INBOX")
			if err != nil {
				t.Fatal(err)
			}
			if !uidValidity.Valid || uidValidity.Int64 != int64(want) || uidValidity == before || lastUID != 4 {
				t.Errorf("checkpoint = %v, %d, want the new UIDVALIDITY %d (was %v) and UID 4", uidValidity, lastUID, want, before)
			}
			var renumberedAt sql.NullInt64
			if err := f.db.Reader.QueryRow("SELECT renumbered_at FROM imap_folder WHERE id = ?", inbox).Scan(&renumberedAt); err != nil {
				t.Fatal(err)
			}
			if renumberedAt.Int64 != f.clock.Now().UnixMilli() {
				t.Errorf("renumbered_at = %v, want %d", renumberedAt, f.clock.Now().UnixMilli())
			}
			if len(source.Folders) != 1 || source.Folders[0].RenumberedAt == nil || !source.Folders[0].RenumberedAt.Equal(f.clock.Now()) {
				t.Errorf("folders = %+v, want INBOX renumbered at %v", source.Folders, f.clock.Now())
			}
			return countScene{f, collector, []int64{id}}
		},
		messages: 4, blobs: 4, entries: 4, collected: []int{4},
	},
	{
		name: "two copies in one folder",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			body := countBody("Twice")
			addMessages(t, double, []fixtureMessage{{folder: "INBOX", body: body}, {folder: "INBOX", body: body}})
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
			return countScene{f, collector, []int64{id}}
		},
		messages: 1, blobs: 1, entries: 1, collected: []int{1},
	},
	{
		name: "the same bytes in INBOX and Archiv",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			addFolders(t, double, "Archiv")
			body := countBody("Both")
			addMessages(t, double, []fixtureMessage{{folder: "INBOX", body: body}, {folder: "Archiv", body: body}})
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX", "Archiv"))
			return countScene{f, collector, []int64{id}}
		},
		messages: 1, blobs: 1, entries: 2, collected: []int{1},
	},
	{
		name: "bytes imported by hand first",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			body := countBody("Imported")
			addMessages(t, double, []fixtureMessage{{folder: "INBOX", body: body}})
			f := newFixture(t, double)
			imported, err := f.archive.Import(context.Background(), bytes.NewReader(body), archive.ImportMeta{AccountEmail: "admin@example.test"})
			if err != nil {
				t.Fatal(err)
			}
			if _, ok := imported.(archive.Stored); !ok {
				t.Fatalf("import = %#v, want Stored", imported)
			}
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
			return countScene{f, collector, []int64{id}}
		},
		messages: 1, blobs: 1, entries: 2, collected: []int{1},
	},
	{
		name: "a message moved between chosen folders",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			addFolders(t, double, "Archiv")
			body := countBody("Moved")
			mail := []fixtureMessage{{folder: "INBOX", body: body}}
			addMessages(t, double, mail)
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX", "Archiv"))

			if err := double.DeleteMessage("INBOX", mail[0].uid); err != nil {
				t.Fatal(err)
			}
			if _, err := double.AddMessage("Archiv", body, time.Time{}); err != nil {
				t.Fatal(err)
			}
			collector = f.syncAgain(t, collector)
			waitForSuccess(t, collector, id, f.clock)

			folders := f.folderIDs(t, id)
			for _, name := range []string{"INBOX", "Archiv"} {
				var n int
				if err := f.db.Reader.QueryRow(
					"SELECT COUNT(*) FROM source_observation WHERE imap_folder_id = ?", folders[name]).Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n != 1 {
					t.Errorf("%s has %d entries, want 1", name, n)
				}
			}
			return countScene{f, collector, []int64{id}}
		},
		messages: 1, blobs: 1, entries: 2, collected: []int{1},
	},
	{
		name: "messages deleted from the mailbox",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			mail := []fixtureMessage{{folder: "INBOX", body: countBody("Kept one")}, {folder: "INBOX", body: countBody("Kept two")}}
			addMessages(t, double, mail)
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))

			for _, message := range mail {
				if err := double.DeleteMessage("INBOX", message.uid); err != nil {
					t.Fatal(err)
				}
			}
			collector = f.syncAgain(t, collector)
			waitForSuccess(t, collector, id, f.clock)
			if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body); !slices.Equal(got, want) {
				t.Errorf("listed %v, want %v", got, want)
			}
			return countScene{f, collector, []int64{id}}
		},
		messages: 2, blobs: 2, entries: 2, collected: []int{2},
	},
	{
		name: "a message with no INTERNALDATE",
		run: func(t *testing.T) countScene {
			double := newDouble(t, imaptest.ImplicitTLS)
			mail := []fixtureMessage{{folder: "INBOX", body: countBody("Undated")}}
			addMessages(t, double, mail)
			if err := double.SetFault("INBOX", mail[0].uid, imaptest.Fault{NoInternalDate: true}); err != nil {
				t.Fatal(err)
			}
			f := newFixture(t, double)
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))

			var receivedAt sql.NullInt64
			if err := f.db.Reader.QueryRow(
				"SELECT received_at FROM source_observation WHERE imap_source_id = ?", id).Scan(&receivedAt); err != nil {
				t.Fatal(err)
			}
			if receivedAt.Valid {
				t.Errorf("received_at = %d, want NULL", receivedAt.Int64)
			}
			return countScene{f, collector, []int64{id}}
		},
		messages: 1, blobs: 1, entries: 1, collected: []int{1},
	},
	{
		name: "two sources sharing bytes",
		run: func(t *testing.T) countScene {
			anna := newDouble(t, imaptest.ImplicitTLS)
			office := newDoubleFor(t, imaptest.ImplicitTLS, "office@example.test")
			body := countBody("Shared")
			addMessages(t, anna, []fixtureMessage{{folder: "INBOX", body: body}})
			addMessages(t, office, []fixtureMessage{{folder: "INBOX", body: body}})
			f := newFixture(t, anna, office)
			collector := f.start(t)
			first := addSource(t, f, collector, sourceFor(t, anna, "tls", "INBOX"))
			input := sourceFor(t, office, "tls", "INBOX")
			input.Name, input.UserName = "Office", "office@example.test"
			second := addSource(t, f, collector, input)
			return countScene{f, collector, []int64{first, second}}
		},
		messages: 1, blobs: 1, entries: 2, collected: []int{1, 1},
	},
}

func TestEntryAndCopyCounts(t *testing.T) {
	for _, tt := range countCases {
		t.Run(tt.name, func(t *testing.T) {
			scene := tt.run(t)
			f := scene.f
			page, err := f.archive.List(context.Background(), 1)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total != tt.messages {
				t.Errorf("the archive lists %d messages, want %d", page.Total, tt.messages)
			}
			if blobs := f.blobs(t); len(blobs) != tt.blobs {
				t.Errorf("blobs/ holds %d files, want %d", len(blobs), tt.blobs)
			}
			var entries int
			if err := f.db.Reader.QueryRow("SELECT COUNT(*) FROM source_observation").Scan(&entries); err != nil {
				t.Fatal(err)
			}
			if entries != tt.entries {
				t.Errorf("%d entries, want %d", entries, tt.entries)
			}
			if temp := f.tempFiles(t); len(temp) != 0 {
				t.Errorf("tmp/ holds %v", temp)
			}
		})
	}
}

func TestCollectedCounts(t *testing.T) {
	ctx := context.Background()
	for _, tt := range countCases {
		t.Run(tt.name, func(t *testing.T) {
			scene := tt.run(t)
			counts, err := scene.f.archive.CollectedCounts(ctx, scene.sources)
			if err != nil {
				t.Fatal(err)
			}
			for i, id := range scene.sources {
				var distinct int
				if err := scene.f.db.Reader.QueryRow(
					"SELECT COUNT(DISTINCT message_id) FROM source_observation WHERE imap_source_id = ?", id).Scan(&distinct); err != nil {
					t.Fatal(err)
				}
				source, err := scene.collector.Get(ctx, id)
				if err != nil {
					t.Fatal(err)
				}
				if source.Collected != tt.collected[i] || counts[id] != tt.collected[i] || distinct != tt.collected[i] {
					t.Errorf("source %d collected %d, counter %d, distinct messages %d, want %d",
						id, source.Collected, counts[id], distinct, tt.collected[i])
				}
			}
		})
	}
}
