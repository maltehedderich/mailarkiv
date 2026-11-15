package archive

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// Folders the collection tests store into. Folder 1 and 2 belong to source
// 1, and folder 3 to source 2.
const (
	sourceAnna   int64 = 1
	sourceOffice int64 = 2
	annaInbox    int64 = 1
	annaArchive  int64 = 2
	officeInbox  int64 = 3
)

// received is an INTERNALDATE, which carries whole seconds.
var received = time.Date(2026, 11, 5, 8, 30, 0, 0, time.UTC)

// addSources writes the rows collect would own, so entries can reference them.
func (f *fixture) addSources(t *testing.T) {
	t.Helper()
	statements := []string{
		`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		 VALUES (1, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0)`,
		`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		 VALUES (2, 'Office', 'imap.example.test', 993, 'tls', 'office@example.test', 'office@example.test', x'00', 0)`,
		`INSERT INTO imap_folder (id, source_id, name, chosen) VALUES (1, 1, 'INBOX', 1), (2, 1, 'Archiv', 1), (3, 2, 'INBOX', 1)`,
	}
	for _, statement := range statements {
		if _, err := f.db.Writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
}

func (f *fixture) collectBytes(t *testing.T, body []byte, meta CollectedMeta) Result {
	t.Helper()
	result, err := f.archive.StoreCollected(context.Background(), bytes.NewReader(body), meta)
	if err != nil {
		t.Fatalf("StoreCollected: %v", err)
	}
	return result
}

func receivedAt(t time.Time) *time.Time { return &t }

func (f *fixture) collectedCount(t *testing.T, sourceID int64) int {
	t.Helper()
	var n int
	err := f.db.Reader.QueryRow(
		"SELECT messages FROM collected_message_count WHERE imap_source_id = ?", sourceID).Scan(&n)
	if err == sql.ErrNoRows {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) latestObservationID(t *testing.T, messageID int64) int64 {
	t.Helper()
	var id int64
	if err := f.db.Reader.QueryRow(
		"SELECT latest_observation_id FROM message WHERE id = ?", messageID).Scan(&id); err != nil {
		t.Fatal(err)
	}
	return id
}

func sameEntry(a, b Entry) bool {
	switch a := a.(type) {
	case ImportEntry:
		b, ok := b.(ImportEntry)
		return ok && a.ObservedAt.Equal(b.ObservedAt) && a.AccountEmail == b.AccountEmail && equalNames(a.FileName, b.FileName)
	case CollectionEntry:
		b, ok := b.(CollectionEntry)
		sameReceived := (a.ReceivedAt == nil) == (b.ReceivedAt == nil) &&
			(a.ReceivedAt == nil || a.ReceivedAt.Equal(*b.ReceivedAt))
		return ok && a.ObservedAt.Equal(b.ObservedAt) && a.SourceID == b.SourceID && a.FolderID == b.FolderID && sameReceived
	default:
		return false
	}
}

func TestStoreCollectedNewBytes(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	body := []byte("From: sender@example.test\r\nSubject: Collected\r\n\r\nBody\r\n")
	hash := hashOf(body)

	result := f.collectBytes(t, body, CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: receivedAt(received)})

	stored, ok := result.(Stored)
	if !ok || stored.Outcome != Archived {
		t.Fatalf("result = %#v, want Stored with Archived", result)
	}
	message := stored.Message
	want := CollectionEntry{ObservedAt: testNow, SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: receivedAt(received)}
	if message.ID <= 0 || message.ContentHash != hash || message.Size != int64(len(body)) ||
		message.ObservationCount != 1 || !sameEntry(message.Latest, want) {
		t.Errorf("message = %+v, want %s, %d bytes, one entry, latest %+v", message, hash, len(body), want)
	}

	got, err := os.ReadFile(filepath.Join(f.dataDir, blobPath(hash)))
	if err != nil || !bytes.Equal(got, body) {
		t.Fatalf("the blob holds %q, %v, want the exact bytes", got, err)
	}
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	f.assertTempEmpty(t)
	if n := f.count(t, "SELECT COUNT(*) FROM message"); n != 1 {
		t.Errorf("message rows = %d, want 1", n)
	}

	var id, sourceID, folderID, observedAt, receivedMillis int64
	var sourceType string
	var accountEmail, fileName sql.NullString
	err = f.db.Reader.QueryRow(`
SELECT id, source_type, observed_at, account_email, file_name, imap_source_id, imap_folder_id, received_at
FROM source_observation`).
		Scan(&id, &sourceType, &observedAt, &accountEmail, &fileName, &sourceID, &folderID, &receivedMillis)
	if err != nil {
		t.Fatal(err)
	}
	if sourceType != "imap_collection" || observedAt != testNow.UnixMilli() || accountEmail.Valid || fileName.Valid ||
		sourceID != sourceAnna || folderID != annaInbox || receivedMillis != received.UnixMilli() {
		t.Errorf("entry = %s, %d, %v, %v, source %d, folder %d, received %d",
			sourceType, observedAt, accountEmail, fileName, sourceID, folderID, receivedMillis)
	}
	if latest := f.latestObservationID(t, message.ID); latest != id {
		t.Errorf("latest_observation_id = %d, want the entry %d", latest, id)
	}
	if n := f.collectedCount(t, sourceAnna); n != 1 {
		t.Errorf("collected_message_count = %d, want 1", n)
	}
	if failures := f.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failure events = %v, want none", failures)
	}
}

func TestStoreCollectedWithNoReceivedTime(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)

	result := f.collectBytes(t, []byte("Subject: no date\r\n\r\n"), CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox})

	stored, ok := result.(Stored)
	if !ok {
		t.Fatalf("result = %#v, want Stored", result)
	}
	if entry, ok := stored.Message.Latest.(CollectionEntry); !ok || entry.ReceivedAt != nil {
		t.Errorf("latest = %+v, want a collection entry with no received time", stored.Message.Latest)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM source_observation WHERE received_at IS NULL"); n != 1 {
		t.Errorf("entries with a NULL received_at = %d, want 1", n)
	}
}

// One entry per folder and message, and the counter counts distinct messages
// per source.
func TestStoreCollectedAgain(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	body := []byte("Subject: twice\r\n\r\nbody\r\n")
	first := f.collectBytes(t, body, CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: receivedAt(received)}).(Stored)
	firstEntry := f.latestObservationID(t, first.Message.ID)

	t.Run("the same folder adds nothing", func(t *testing.T) {
		later := received.Add(time.Hour)
		result := f.collectBytes(t, body, CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: &later})

		stored, ok := result.(Stored)
		if !ok || stored.Outcome != AlreadyArchived || stored.Message.ID != first.Message.ID {
			t.Fatalf("result = %#v, want Stored with AlreadyArchived for message %d", result, first.Message.ID)
		}
		if stored.Message.ObservationCount != 1 || !sameEntry(stored.Message.Latest, first.Message.Latest) {
			t.Errorf("message = %+v, want the first entry only", stored.Message)
		}
		if n := f.count(t, "SELECT COUNT(*) FROM source_observation"); n != 1 {
			t.Errorf("entries = %d, want 1", n)
		}
		if latest := f.latestObservationID(t, first.Message.ID); latest != firstEntry {
			t.Errorf("latest_observation_id = %d, want %d", latest, firstEntry)
		}
		if n := f.collectedCount(t, sourceAnna); n != 1 {
			t.Errorf("collected_message_count = %d, want 1", n)
		}
	})

	t.Run("another folder of the source adds an entry", func(t *testing.T) {
		result := f.collectBytes(t, body, CollectedMeta{SourceID: sourceAnna, FolderID: annaArchive, ReceivedAt: receivedAt(received)})

		stored, ok := result.(Stored)
		want := CollectionEntry{ObservedAt: testNow, SourceID: sourceAnna, FolderID: annaArchive, ReceivedAt: receivedAt(received)}
		if !ok || stored.Message.ObservationCount != 2 || !sameEntry(stored.Message.Latest, want) {
			t.Fatalf("result = %#v, want Stored with two entries, the latest in folder %d", result, annaArchive)
		}
		if latest := f.latestObservationID(t, first.Message.ID); latest == firstEntry {
			t.Error("latest_observation_id still names the first entry")
		}
		if n := f.collectedCount(t, sourceAnna); n != 1 {
			t.Errorf("collected_message_count = %d, want 1", n)
		}
	})

	t.Run("another source counts it too", func(t *testing.T) {
		result := f.collectBytes(t, body, CollectedMeta{SourceID: sourceOffice, FolderID: officeInbox})

		if stored, ok := result.(Stored); !ok || stored.Message.ObservationCount != 3 {
			t.Fatalf("result = %#v, want Stored with three entries", result)
		}
		if n := f.collectedCount(t, sourceOffice); n != 1 {
			t.Errorf("collected_message_count for source 2 = %d, want 1", n)
		}
		if n := f.collectedCount(t, sourceAnna); n != 1 {
			t.Errorf("collected_message_count for source 1 = %d, want 1", n)
		}
		counts, err := f.archive.CollectedCounts(context.Background(), []int64{sourceAnna, sourceOffice, 99})
		if err != nil {
			t.Fatal(err)
		}
		if len(counts) != 3 || counts[sourceAnna] != 1 || counts[sourceOffice] != 1 || counts[99] != 0 {
			t.Errorf("CollectedCounts = %v, want 1, 1, and 0 for a source with none", counts)
		}
	})

	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM message"); n != 1 {
		t.Errorf("message rows = %d, want 1", n)
	}
	f.assertTempEmpty(t)
}

func TestStoreCollectedAfterImport(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	body := []byte("Subject: imported first\r\n\r\nbody\r\n")
	imported := f.importBytes(t, body, name("first.eml")).(Stored)

	result := f.collectBytes(t, body, CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: receivedAt(received)})

	stored, ok := result.(Stored)
	if !ok || stored.Outcome != AlreadyArchived || stored.Message.ID != imported.Message.ID || stored.Message.ObservationCount != 2 {
		t.Fatalf("result = %#v, want Stored with AlreadyArchived, message %d, and two entries", result, imported.Message.ID)
	}
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	history, err := f.archive.History(context.Background(), imported.Message.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []Entry{
		CollectionEntry{ObservedAt: testNow, SourceID: sourceAnna, FolderID: annaInbox, ReceivedAt: receivedAt(received)},
		ImportEntry{ObservedAt: testNow, AccountEmail: account, FileName: name("first.eml")},
	}
	if len(history.Entries) != len(want) {
		t.Fatalf("history = %+v, want %+v", history.Entries, want)
	}
	for i := range want {
		if !sameEntry(history.Entries[i], want[i]) {
			t.Errorf("entry %d = %+v, want %+v", i, history.Entries[i], want[i])
		}
	}
	if n := f.collectedCount(t, sourceAnna); n != 1 {
		t.Errorf("collected_message_count = %d, want 1", n)
	}
}

// Collection keeps whatever the server returns, so the mail header check
// doesn't apply. An empty body is still refused.
func TestStoreCollectedSkipsTheHeaderCheck(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	meta := CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox}

	pdf := []byte("%PDF-1.7\n")
	if stored, ok := f.collectBytes(t, pdf, meta).(Stored); !ok || stored.Outcome != Archived {
		t.Fatalf("%q wasn't stored", pdf)
	}

	result := f.collectBytes(t, nil, meta)
	if refused, ok := result.(Refused); !ok || refused.Reason != EmptyFile {
		t.Fatalf("an empty body gave %#v, want Refused with empty", result)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM message"); n != 1 {
		t.Errorf("message rows = %d, want 1", n)
	}
	f.assertTempEmpty(t)
}

func TestStoreCollectedStorageFailure(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	tmp := filepath.Join(f.dataDir, "tmp")
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	// A plain file where the directory belongs fails even for root.
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result := f.collectBytes(t, []byte("Subject: x\r\n\r\n"), CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox})

	if failed, ok := result.(Failed); !ok || failed.Category != opstatus.StorageWriteFailed {
		t.Fatalf("result = %#v, want Failed with storage_write_failed", result)
	}
	// The source shows the failure, so the status screen doesn't count it twice.
	if failures := f.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failure events = %+v, want none", failures)
	}
	var logged bool
	for line := range strings.Lines(f.log.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line isn't JSON: %q", line)
		}
		if entry["level"] == "ERROR" && entry["msg"] == "collected message not stored" &&
			entry["category"] == "storage_write_failed" && entry["error"] != nil {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no log line names the category and the cause:\n%s", f.log.String())
	}
	f.assertNothingListed(t)
	if n := f.collectedCount(t, sourceAnna); n != 0 {
		t.Errorf("collected_message_count = %d, want 0", n)
	}
}

func TestStoreCollectedRestoresAMissingOriginal(t *testing.T) {
	f := newFixture(t)
	f.addSources(t)
	body := []byte("Subject: restored\r\n\r\nbody\r\n")
	hash := hashOf(body)
	meta := CollectedMeta{SourceID: sourceAnna, FolderID: annaInbox}
	first := f.collectBytes(t, body, meta).(Stored)
	if err := os.Remove(filepath.Join(f.dataDir, blobPath(hash))); err != nil {
		t.Fatal(err)
	}

	result := f.collectBytes(t, body, meta)

	stored, ok := result.(Stored)
	if !ok || stored.Outcome != Restored || stored.Message.ID != first.Message.ID {
		t.Fatalf("result = %#v, want Stored with Restored", result)
	}
	got, err := os.ReadFile(filepath.Join(f.dataDir, blobPath(hash)))
	if err != nil || !bytes.Equal(got, body) {
		t.Errorf("the blob holds %q, %v, want the exact bytes", got, err)
	}
	event := f.assertOneFailure(t, opstatus.StoredOriginalMissing)
	if !event.Restored {
		t.Errorf("event = %+v, want it marked restored", event)
	}
}
