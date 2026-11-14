package collect

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// fixtureMessage is one synthetic message on the double.
type fixtureMessage struct {
	folder   string
	body     []byte
	received time.Time
	uid      imap.UID
}

func hashOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// addMessages puts each message on the double and records its UID.
func addMessages(t *testing.T, double *imaptest.Server, messages []fixtureMessage) {
	t.Helper()
	for i := range messages {
		uid, err := double.AddMessage(messages[i].folder, messages[i].body, messages[i].received)
		if err != nil {
			t.Fatalf("add %q: %v", messages[i].body, err)
		}
		messages[i].uid = uid
	}
}

// firstSyncMail holds a message with each kind of line ending in INBOX, two
// in a folder with a non-ASCII name, and nothing in Leer.
func firstSyncMail() []fixtureMessage {
	day := time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC)
	return []fixtureMessage{
		{folder: "INBOX", body: []byte("From: ben@example.test\r\nSubject: CRLF\r\n\r\nBody\r\n"), received: day},
		{folder: "INBOX", body: []byte("From: ben@example.test\r\nSubject: LF in the body\r\n\r\nLine one\nLine two\n"), received: day.Add(time.Hour)},
		{folder: "INBOX", body: []byte("From: ben@example.test\r\nSubject: No final line break\r\n\r\nBody"), received: day.Add(2 * time.Hour)},
		{folder: "Entwürfe", body: []byte("From: anna@example.test\r\nSubject: Entwurf eins\r\n\r\nText\r\n"), received: day.Add(3 * time.Hour)},
		{folder: "Entwürfe", body: []byte("From: anna@example.test\r\nSubject: Entwurf zwei\r\n\r\nText\r\n"), received: day.Add(4 * time.Hour)},
	}
}

func addFolders(t *testing.T, double *imaptest.Server, names ...string) {
	t.Helper()
	for _, name := range names {
		if err := double.AddFolder(name); err != nil {
			t.Fatal(err)
		}
	}
}

func readOriginal(t *testing.T, files *archive.Archive, id int64) []byte {
	t.Helper()
	original, err := files.OpenVerified(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	defer original.File.Close()
	body, err := io.ReadAll(original.File)
	if err != nil {
		t.Fatal(err)
	}
	return body
}

func TestFirstSync(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	addFolders(t, double, "Entwürfe", "Leer")
	mail := firstSyncMail()
	addMessages(t, double, mail)
	f := newFixture(t, double)
	collector := f.start(t)

	added, err := collector.Add(ctx, sourceFor(t, double, "tls", "INBOX", "Entwürfe", "Leer"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	source := waitForSuccess(t, collector, added.ID, f.clock)

	if source.LastSuccess.Archived != len(mail) {
		t.Errorf("last success archived %d, want %d", source.LastSuccess.Archived, len(mail))
	}
	if source.Collected != len(mail) {
		t.Errorf("collected = %d, want %d", source.Collected, len(mail))
	}
	page, err := f.archive.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != len(mail) {
		t.Fatalf("the archive lists %d messages, want %d", page.Total, len(mail))
	}

	folders := f.folderIDs(t, added.ID)
	byHash := map[string]fixtureMessage{}
	for _, message := range mail {
		byHash[hashOf(message.body)] = message
	}
	for _, item := range page.Items {
		message, ok := byHash[item.ContentHash]
		if !ok {
			t.Errorf("message %d isn't one the double holds", item.ID)
			continue
		}
		if body := readOriginal(t, f.archive, item.ID); !bytes.Equal(body, message.body) {
			t.Errorf("message %d holds %q, want %q", item.ID, body, message.body)
		}
		history, err := f.archive.History(ctx, item.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(history.Entries) != 1 {
			t.Fatalf("message %d has %d entries, want 1", item.ID, len(history.Entries))
		}
		entry, ok := history.Entries[0].(archive.CollectionEntry)
		if !ok || entry.SourceID != added.ID || entry.FolderID != folders[message.folder] ||
			entry.ReceivedAt == nil || entry.ReceivedAt.UnixMilli() != message.received.UnixMilli() ||
			!entry.ObservedAt.Equal(testNow) {
			t.Errorf("message %d entry = %+v, want source %d, folder %s (%d), received %v, observed %v",
				item.ID, history.Entries[0], added.ID, message.folder, folders[message.folder], message.received, testNow)
		}
	}

	for _, name := range []string{"INBOX", "Entwürfe", "Leer"} {
		var uidValidity, lastUID int64
		if err := f.db.Reader.QueryRow(
			"SELECT uid_validity, last_uid FROM imap_folder WHERE id = ?", folders[name]).Scan(&uidValidity, &lastUID); err != nil {
			t.Fatalf("folder %s: %v", name, err)
		}
		want, err := double.UIDValidity(name)
		if err != nil {
			t.Fatal(err)
		}
		var highest imap.UID
		for _, message := range mail {
			if message.folder == name {
				highest = max(highest, message.uid)
			}
		}
		if uidValidity != int64(want) || lastUID != int64(highest) {
			t.Errorf("folder %s: uid_validity %d, last_uid %d, want %d and %d", name, uidValidity, lastUID, want, highest)
		}
	}

	var successAt, successArchived int64
	if err := f.db.Reader.QueryRow(
		"SELECT last_success_at, last_success_archived FROM imap_source WHERE id = ?", added.ID).Scan(&successAt, &successArchived); err != nil {
		t.Fatal(err)
	}
	if successAt != testNow.UnixMilli() || successArchived != int64(len(mail)) {
		t.Errorf("last success = %d with %d archived, want %d with %d", successAt, successArchived, testNow.UnixMilli(), len(mail))
	}

	labels, err := NewLabeler(f.db).Labels(ctx, []int64{folders["INBOX"], folders["Entwürfe"]})
	if err != nil {
		t.Fatal(err)
	}
	wantSource := SourceLabel{ID: added.ID, Name: "Anna", Host: "127.0.0.1", UserName: testUser}
	want := map[int64]Label{
		folders["INBOX"]:    {Folder: "INBOX", Source: wantSource},
		folders["Entwürfe"]: {Folder: "Entwürfe", Source: wantSource},
	}
	if !reflect.DeepEqual(labels, want) {
		t.Errorf("labels = %+v, want %+v", labels, want)
	}

	t.Run("a new collector syncs at start", func(t *testing.T) {
		collector.Stop()
		before := logins(double)
		f.clock.Advance(time.Minute)

		restarted := f.start(t)
		source := waitForSuccess(t, restarted, added.ID, f.clock)

		if got := logins(double); got != before+1 {
			t.Errorf("LOGIN count = %d, want %d", got, before+1)
		}
		if source.LastSuccess.Archived != 0 {
			t.Errorf("the sync at start archived %d, want 0", source.LastSuccess.Archived)
		}
		if page, err := f.archive.List(ctx, 1); err != nil || page.Total != len(mail) {
			t.Errorf("the archive lists %d messages, %v, want %d", page.Total, err, len(mail))
		}
	})
}

// Bytes imported first keep one list entry and one blob, and gain a
// collection entry.
func TestCollectImportedBytes(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	body := []byte("From: ben@example.test\r\nSubject: Imported first\r\n\r\nBody\r\n")
	received := time.Date(2026, 11, 2, 10, 0, 0, 0, time.UTC)
	addMessages(t, double, []fixtureMessage{{folder: "INBOX", body: body, received: received}})
	f := newFixture(t, double)
	imported, err := f.archive.Import(ctx, bytes.NewReader(body), archive.ImportMeta{AccountEmail: "admin@example.test"})
	if err != nil {
		t.Fatal(err)
	}
	stored, ok := imported.(archive.Stored)
	if !ok {
		t.Fatalf("import = %#v, want Stored", imported)
	}
	collector := f.start(t)

	added, err := collector.Add(ctx, sourceFor(t, double, "tls", "INBOX"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	waitForSuccess(t, collector, added.ID, f.clock)

	page, err := f.archive.List(ctx, 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || page.Items[0].ID != stored.Message.ID || page.Items[0].ObservationCount != 2 {
		t.Fatalf("list = %+v, want message %d alone with two entries", page, stored.Message.ID)
	}
	if blobs := f.blobs(t); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	history, err := f.archive.History(ctx, stored.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(history.Entries) != 2 {
		t.Fatalf("history = %+v, want two entries", history.Entries)
	}
	if _, ok := history.Entries[0].(archive.CollectionEntry); !ok {
		t.Errorf("newest entry = %#v, want the collection", history.Entries[0])
	}
	if _, ok := history.Entries[1].(archive.ImportEntry); !ok {
		t.Errorf("oldest entry = %#v, want the import", history.Entries[1])
	}
}

func TestStoredPasswordIsSealed(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	collector := f.start(t)

	added, err := collector.Add(ctx, sourceFor(t, double, "tls", "INBOX"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}

	var sealed []byte
	var host, userName string
	if err := f.db.Reader.QueryRow(
		"SELECT sealed_password, host, user_name FROM imap_source WHERE id = ?", added.ID).Scan(&sealed, &host, &userName); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte(testPassword)) {
		t.Error("the stored password holds the plaintext")
	}
	opened, err := f.key.Open(sealed, host, userName)
	if err != nil || opened != testPassword {
		t.Errorf("Open = %q, %v, want the password", opened, err)
	}
	waitForSuccess(t, collector, added.ID, f.clock)
}

// Collection reads a mailbox without changing it: no command that can change
// one, and no flag set.
func TestCollectionChangesNothing(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.StartTLS)
	addFolders(t, double, "Entwürfe", "Leer")
	mail := firstSyncMail()
	addMessages(t, double, mail)
	forbidden := []string{"SELECT", "STORE", "COPY", "MOVE", "EXPUNGE", "APPEND", "CREATE", "DELETE", "RENAME", "SUBSCRIBE", "UNSUBSCRIBE"}
	double.SetHook(func(command imaptest.Command) error {
		if slices.Contains(forbidden, strings.TrimPrefix(command.Name, "UID ")) {
			t.Errorf("collection sent %s %q", command.Name, command.Mailbox)
			return imaptest.No("", "refused by the test")
		}
		return nil
	})
	before, err := double.Flags()
	if err != nil {
		t.Fatal(err)
	}
	f := newFixture(t, double)
	collector := f.start(t)

	added, err := collector.Add(ctx, sourceFor(t, double, "starttls", "INBOX", "Entwürfe", "Leer"))
	if err != nil {
		t.Fatalf("Add: %v", err)
	}
	source := waitForSuccess(t, collector, added.ID, f.clock)

	if source.LastSuccess.Archived != len(mail) {
		t.Errorf("archived %d, want %d", source.LastSuccess.Archived, len(mail))
	}
	after, err := double.Flags()
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(after, before) {
		t.Errorf("flags changed from %v to %v", before, after)
	}
	for folder, messages := range after {
		for uid, flags := range messages {
			if slices.Contains(flags, imap.FlagSeen) {
				t.Errorf("%s UID %d is marked \\Seen", folder, uid)
			}
		}
	}
	var examined int
	for _, command := range double.Commands() {
		if command.Name == "EXAMINE" {
			examined++
		}
	}
	if examined != 3 {
		t.Errorf("EXAMINE sent %d times, want once per folder", examined)
	}
}
