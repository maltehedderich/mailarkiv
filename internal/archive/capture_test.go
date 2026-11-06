package archive

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

var testNow = time.Date(2026, 11, 6, 15, 50, 52, 123_000_000, time.UTC)

type fixture struct {
	archive *Archive
	db      *sqlitedb.DB
	status  *opstatus.Status
	dataDir string
	log     *lockedBuffer
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dataDir := filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })

	log := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(log, nil))
	clock := func() time.Time { return testNow }
	status := opstatus.New(logger, clock)
	archive, err := Open(dataDir, db, status, logger, clock)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{archive: archive, db: db, status: status, dataDir: dataDir, log: log}
}

const account = "admin@example.test"

func name(s string) *string { return &s }

func (f *fixture) importBytes(t *testing.T, body []byte, fileName *string) Result {
	t.Helper()
	result, err := f.archive.Import(context.Background(), bytes.NewReader(body), ImportMeta{AccountEmail: account, FileName: fileName})
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	return result
}

func hashOf(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

// files lists every regular file under dir, relative to it.
func files(t *testing.T, dir string) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(dir, path)
			if err != nil {
				return err
			}
			found = append(found, filepath.ToSlash(relative))
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(found)
	return found
}

func (f *fixture) assertTempEmpty(t *testing.T) {
	t.Helper()
	if left := files(t, filepath.Join(f.dataDir, "tmp")); len(left) != 0 {
		t.Errorf("tmp/ holds %v, want nothing", left)
	}
}

func (f *fixture) count(t *testing.T, query string) int {
	t.Helper()
	var n int
	if err := f.db.Reader.QueryRow(query).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func (f *fixture) assertNothingStored(t *testing.T) {
	t.Helper()
	f.assertTempEmpty(t)
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 0 {
		t.Errorf("blobs/ holds %v, want nothing", blobs)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM message"); n != 0 {
		t.Errorf("message rows = %d, want 0", n)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM source_observation"); n != 0 {
		t.Errorf("source_observation rows = %d, want 0", n)
	}
}

func blobPath(hash string) string {
	return "blobs/" + hash[0:2] + "/" + hash[2:4] + "/" + hash
}

func TestImportNewBytes(t *testing.T) {
	f := newFixture(t)
	body := []byte("From: sender@example.test\r\nSubject: Invoice\r\n\r\nBody\r\n")
	hash := hashOf(body)

	result := f.importBytes(t, body, name("invoice.eml"))

	stored, ok := result.(Stored)
	if !ok || stored.Outcome != Archived {
		t.Fatalf("result = %#v, want Stored with Archived", result)
	}
	want := Message{
		ID:          stored.Message.ID,
		ContentHash: hash,
		Size:        int64(len(body)),
		FileName:    name("invoice.eml"),
		ImportedAt:  testNow,
		ImportCount: 1,
	}
	if stored.Message.ID <= 0 || !equalMessages(stored.Message, want) {
		t.Errorf("message = %+v, want %+v", stored.Message, want)
	}

	got, err := os.ReadFile(filepath.Join(f.dataDir, blobPath(hash)))
	if err != nil {
		t.Fatalf("the blob isn't at blobs/ab/cd/<hash>: %v", err)
	}
	if !bytes.Equal(got, body) {
		t.Errorf("the blob holds %q, want the exact bytes", got)
	}
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	f.assertTempEmpty(t)

	if n := f.count(t, "SELECT COUNT(*) FROM message"); n != 1 {
		t.Errorf("message rows = %d, want 1", n)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM message WHERE latest_observation_id IS NULL"); n != 0 {
		t.Errorf("%d committed message rows have a NULL latest_observation_id", n)
	}
	var sourceType, accountEmail string
	var fileName sql.NullString
	var observedAt int64
	err = f.db.Reader.QueryRow(
		"SELECT source_type, observed_at, account_email, file_name FROM source_observation").
		Scan(&sourceType, &observedAt, &accountEmail, &fileName)
	if err != nil {
		t.Fatal(err)
	}
	if sourceType != "manual_import" || observedAt != testNow.UnixMilli() || accountEmail != account ||
		fileName.String != "invoice.eml" {
		t.Errorf("observation = %s, %d, %s, %v", sourceType, observedAt, accountEmail, fileName)
	}

	if failures := f.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failure events = %v, want none", failures)
	}
}

func equalMessages(a, b Message) bool {
	sameName := (a.FileName == nil) == (b.FileName == nil) && (a.FileName == nil || *a.FileName == *b.FileName)
	return a.ID == b.ID && a.ContentHash == b.ContentHash && a.Size == b.Size && sameName &&
		a.ImportedAt.Equal(b.ImportedAt) && a.ImportCount == b.ImportCount
}

func TestImportWithNoFileName(t *testing.T) {
	f := newFixture(t)
	result := f.importBytes(t, []byte("Subject: x\n"), nil)

	stored, ok := result.(Stored)
	if !ok || stored.Message.FileName != nil {
		t.Fatalf("result = %#v, want Stored with no file name", result)
	}
	if n := f.count(t, "SELECT COUNT(*) FROM source_observation WHERE file_name IS NULL"); n != 1 {
		t.Errorf("observations with a NULL file_name = %d, want 1", n)
	}
}

func TestImportSameBytesAgain(t *testing.T) {
	f := newFixture(t)
	body := []byte("Subject: twice\n\nbody\n")
	first := f.importBytes(t, body, name("first.eml")).(Stored)

	result := f.importBytes(t, body, name("second.eml"))

	stored, ok := result.(Stored)
	if !ok || stored.Outcome != AlreadyArchived {
		t.Fatalf("result = %#v, want Stored with AlreadyArchived", result)
	}
	if stored.Message.ID != first.Message.ID || stored.Message.ImportCount != 2 || *stored.Message.FileName != "second.eml" {
		t.Errorf("message = %+v, want the first ID, a count of 2, and the latest name", stored.Message)
	}
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	f.assertTempEmpty(t)
}

// FR-021: a supplied name is text in the catalog and never reaches a path.
func TestHostileFileNames(t *testing.T) {
	f := newFixture(t)
	names := []string{
		"../../etc/passwd",
		"<img src=x onerror=alert(1)>.eml",
		"invoice\u202egpj.eml",
		"/absolute/path.eml",
	}
	var wantFiles []string
	for i, hostile := range names {
		body := []byte("Subject: hostile " + string(rune('a'+i)) + "\n")
		f.importBytes(t, body, name(hostile))
		wantFiles = append(wantFiles, blobPath(hashOf(body)))
	}

	var stored []string
	rows, err := f.db.Reader.Query("SELECT file_name FROM source_observation ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var fileName string
		if err := rows.Scan(&fileName); err != nil {
			t.Fatal(err)
		}
		stored = append(stored, fileName)
	}
	if !slices.Equal(stored, names) {
		t.Errorf("stored names = %q, want %q", stored, names)
	}

	slices.Sort(wantFiles)
	if got := files(t, filepath.Join(f.dataDir, "blobs")); !slices.Equal(got, stripBlobs(wantFiles)) {
		t.Errorf("blobs/ holds %v, want only the hash-named files %v", got, wantFiles)
	}
	parent := filepath.Dir(f.dataDir)
	for _, entry := range files(t, parent) {
		if !strings.HasPrefix(entry, "data/") {
			t.Errorf("a file appeared outside the data directory: %s", entry)
		}
	}
}

func stripBlobs(paths []string) []string {
	stripped := make([]string, len(paths))
	for i, path := range paths {
		stripped[i] = strings.TrimPrefix(path, "blobs/")
	}
	return stripped
}

// endTracker reports whether the reader reached the end of its bytes.
type endTracker struct {
	reader  io.Reader
	reached bool
}

func (e *endTracker) Read(p []byte) (int, error) {
	n, err := e.reader.Read(p)
	if errors.Is(err, io.EOF) {
		e.reached = true
	}
	return n, err
}

// Refused files store nothing. The server reads the rest of the body first, so
// the browser gets the answer instead of a reset (research.md).
func TestRefusedFileStoresNothing(t *testing.T) {
	tests := []struct {
		name string
		body []byte
		want Reason
	}{
		{"empty", nil, EmptyFile},
		{"PDF", append([]byte("%PDF-1.7\n"), bytes.Repeat([]byte{0xab}, 200_000)...), NoMailHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			body := &endTracker{reader: bytes.NewReader(tt.body)}

			result, err := f.archive.Import(context.Background(), body, ImportMeta{AccountEmail: account, FileName: name("x.eml")})
			if err != nil {
				t.Fatal(err)
			}
			if refused, ok := result.(Refused); !ok || refused.Reason != tt.want {
				t.Fatalf("result = %#v, want Refused with %v", result, tt.want)
			}
			if !body.reached {
				t.Error("the rest of the body wasn't read")
			}
			f.assertNothingStored(t)
			if failures := f.status.Snapshot().Failures; len(failures) != 0 {
				t.Errorf("failure events = %v, want none", failures)
			}
		})
	}
}

// generated is a mail-shaped body of exactly size bytes that is never held in
// memory whole.
type generated struct {
	remaining int64
	head      []byte
}

func newGenerated(size int64) *generated {
	return &generated{remaining: size, head: []byte("Subject: large\r\n\r\n")}
}

func (g *generated) Read(p []byte) (int, error) {
	if g.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > g.remaining {
		p = p[:g.remaining]
	}
	n := copy(p, g.head)
	g.head = g.head[n:]
	for i := n; i < len(p); i++ {
		p[i] = 'x'
	}
	g.remaining -= int64(len(p))
	return len(p), nil
}

func TestOverLimitBodyIsRefused(t *testing.T) {
	f := newFixture(t)
	result, err := f.archive.Import(context.Background(), newGenerated(MaxSize+1), ImportMeta{AccountEmail: account})
	if err != nil {
		t.Fatal(err)
	}
	if refused, ok := result.(Refused); !ok || refused.Reason != TooLarge {
		t.Fatalf("result = %#v, want Refused with TooLarge", result)
	}
	f.assertNothingStored(t)
}

// failingReader gives some bytes, then an error, as a dropped connection does.
type failingReader struct {
	body io.Reader
}

var errConnectionDropped = errors.New("connection dropped")

func (r *failingReader) Read(p []byte) (int, error) {
	n, err := r.body.Read(p)
	if errors.Is(err, io.EOF) {
		return n, errConnectionDropped
	}
	return n, err
}

// A body that ends early is no failure event: the administrator who sent it
// sees Unconfirmed (FR-038).
func TestDroppedBodyLeavesNothing(t *testing.T) {
	for _, size := range []int{100, 300_000} {
		f := newFixture(t)
		body := append([]byte("Subject: dropped\r\n\r\n"), bytes.Repeat([]byte("y"), size)...)

		result, err := f.archive.Import(context.Background(), &failingReader{bytes.NewReader(body)}, ImportMeta{AccountEmail: account})
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := result.(Dropped); !ok {
			t.Fatalf("result = %#v, want Dropped", result)
		}
		f.assertNothingStored(t)
		if failures := f.status.Snapshot().Failures; len(failures) != 0 {
			t.Errorf("failure events = %v, want none", failures)
		}
	}
}

func (f *fixture) assertOneFailure(t *testing.T, want opstatus.Category) opstatus.FailureEvent {
	t.Helper()
	failures := f.status.Snapshot().Failures
	if len(failures) != 1 || failures[0].Category != want {
		t.Fatalf("failure events = %+v, want one %v", failures, want)
	}
	var logged bool
	for line := range strings.Lines(f.log.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line isn't JSON: %q", line)
		}
		if entry["category"] == want.String() {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no log line has category %v:\n%s", want, f.log.String())
	}
	return failures[0]
}

func (f *fixture) assertNothingListed(t *testing.T) {
	t.Helper()
	page, err := f.archive.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 0 || len(page.Items) != 0 {
		t.Errorf("list = %+v, want nothing", page)
	}
}

func TestStorageWriteFailure(t *testing.T) {
	f := newFixture(t)
	tmp := filepath.Join(f.dataDir, "tmp")
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	// A plain file where the directory belongs fails even for root.
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	result := f.importBytes(t, []byte("Subject: x\n"), name("x.eml"))

	if failed, ok := result.(Failed); !ok || failed.Category != opstatus.StorageWriteFailed {
		t.Fatalf("result = %#v, want Failed with storage_write_failed", result)
	}
	f.assertOneFailure(t, opstatus.StorageWriteFailed)
	f.assertNothingListed(t)
	if blobs := files(t, filepath.Join(f.dataDir, "blobs")); len(blobs) != 0 {
		t.Errorf("blobs/ holds %v, want nothing", blobs)
	}
}

// The original was stored, so it stays, and the next start lists it as
// unreferenced (FR-038, Catalog write failed).
func TestCatalogWriteFailure(t *testing.T) {
	f := newFixture(t)
	if err := f.db.Writer.Close(); err != nil {
		t.Fatal(err)
	}
	body := []byte("Subject: catalog\n")
	hash := hashOf(body)

	result := f.importBytes(t, body, name("x.eml"))

	if failed, ok := result.(Failed); !ok || failed.Category != opstatus.CatalogWriteFailed {
		t.Fatalf("result = %#v, want Failed with catalog_write_failed", result)
	}
	event := f.assertOneFailure(t, opstatus.CatalogWriteFailed)
	if event.ContentHash == nil || *event.ContentHash != hash {
		t.Errorf("event content hash = %v, want %s", event.ContentHash, hash)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, blobPath(hash))); err != nil {
		t.Errorf("the blob wasn't kept: %v", err)
	}
	f.assertTempEmpty(t)
	f.assertNothingListed(t)
}
