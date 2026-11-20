package search

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// The test binary is also the parse worker and, in a few tests, a search
// program that misbehaves.
func TestMain(m *testing.M) {
	if os.Getenv("MAILARKIV_TEST_PARSE_WORKER") == "1" {
		os.Exit(processing.RunWorker(os.Stdin, os.Stdout))
	}
	if mode := os.Getenv("MAILARKIV_TEST_SEARCH"); mode != "" {
		os.Exit(fakeSearchProgram(mode))
	}
	os.Exit(m.Run())
}

// fakeSearchProgram plays a search program that breaks the contract.
func fakeSearchProgram(mode string) int {
	hello := map[string]any{"type": "hello", "protocol": 1, "tantivy": "0.27.0", "schema_version": 1}
	switch mode {
	case "bad_hello":
		hello["protocol"] = 2
		_ = processing.WriteFrame(os.Stdout, hello)
	case "stderr":
		_ = processing.WriteFrame(os.Stdout, hello)
		for i := range 3 {
			fmt.Fprintf(os.Stderr, "Quartalsbericht line %d\n", i)
		}
		return 0
	default:
		return 1
	}
	_, _ = io.Copy(io.Discard, os.Stdin)
	return 0
}

// fakeSearchCommand starts this test binary as a search program in mode.
func fakeSearchCommand(mode string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(os.Args[0])
		cmd.Env = []string{"MAILARKIV_TEST_SEARCH=" + mode}
		return cmd
	}
}

func testWorkerCommand() *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{"MAILARKIV_TEST_PARSE_WORKER=1"}
	return cmd
}

// searchProgram finds bin/mailarkiv-search from the repository root.
func searchProgram(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
	program := filepath.Join(dir, "bin", "mailarkiv-search")
	if _, err := os.Stat(program); err != nil {
		t.Fatalf("%s is missing: run mise run search:build", program)
	}
	return program
}

type fixture struct {
	t         *testing.T
	dataDir   string
	db        *sqlitedb.DB
	archive   *archive.Archive
	status    *opstatus.Status
	processor *processing.Processor
	module    *Module
	log       *lockedBuffer

	clockMu sync.Mutex
	clock   time.Time
}

// newFixture prepares a data directory and a module that starts program. Its
// clock starts at a fixed time and moves only when a test moves it.
func newFixture(t *testing.T, program string) *fixture {
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
	logger, log := newLogger()
	f := &fixture{t: t, dataDir: dataDir, db: db, log: log, clock: time.Date(2026, 11, 20, 9, 0, 0, 0, time.UTC)}
	f.status = opstatus.New(logger, f.now)
	f.archive, err = archive.Open(dataDir, db, f.status, logger, f.now)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := processing.NewPool(processing.BackgroundWorkers, processing.MessageLimit, testWorkerCommand, logger)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	f.processor = processing.New(f.archive, pool, logger)
	if err := Prepare(context.Background(), dataDir, db); err != nil {
		t.Fatal(err)
	}
	f.module = New(Config{
		DataDir: dataDir, Program: program, DB: db, Archive: f.archive, Processor: f.processor,
		Status: f.status, Logger: logger, Now: f.now,
	})
	return f
}

func (f *fixture) now() time.Time {
	f.clockMu.Lock()
	defer f.clockMu.Unlock()
	return f.clock
}

// background runs fn until the test ends.
func (f *fixture) background(fn func(context.Context)) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()
	f.t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (f *fixture) runProcessing() { f.background(f.processor.Run) }
func (f *fixture) runSearch()     { f.background(f.module.Run) }

// waitFor polls cond every 20 ms for at most 30 s.
func (f *fixture) waitFor(what string, cond func() bool) {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			f.t.Fatalf("waited 30 s for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (f *fixture) waitForIndexing(state IndexingState) {
	f.t.Helper()
	f.waitFor("indexing "+string(state), func() bool { return f.module.State().Indexing == state })
}

// waitForProcessed waits until count messages have a processing result.
func (f *fixture) waitForProcessed(count int64) {
	f.t.Helper()
	f.waitFor(fmt.Sprintf("%d processed messages", count), func() bool {
		highest, err := f.archive.HighestProcessedSeq(context.Background())
		return err == nil && highest == count
	})
}

func (f *fixture) importBytes(original []byte) int64 {
	f.t.Helper()
	result, err := f.archive.Import(context.Background(), bytes.NewReader(original),
		archive.ImportMeta{AccountEmail: "admin@example.test"})
	if err != nil {
		f.t.Fatal(err)
	}
	stored, ok := result.(archive.Stored)
	if !ok {
		f.t.Fatalf("import gave %#v, want Stored", result)
	}
	return stored.Message.ID
}

func (f *fixture) importFixture(name string) int64 {
	f.t.Helper()
	return f.importBytes(plainFixture(f.t, name))
}

// ids searches for words and returns the IDs of the first page's rows.
func (f *fixture) ids(words string) []int64 {
	f.t.Helper()
	result, err := f.module.Search(context.Background(), Query{Words: words, Page: 1})
	if err != nil {
		f.t.Fatalf("search %q: %v", words, err)
	}
	ids := []int64{}
	for _, row := range result.Rows {
		ids = append(ids, row.ID)
	}
	return ids
}

type generationRow struct {
	id     int64
	role   string
	cursor int64
}

func (f *fixture) generations() []generationRow {
	f.t.Helper()
	rows, err := f.db.Reader.Query("SELECT id, role, cursor FROM search_generation ORDER BY id")
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var out []generationRow
	for rows.Next() {
		var row generationRow
		if err := rows.Scan(&row.id, &row.role, &row.cursor); err != nil {
			f.t.Fatal(err)
		}
		out = append(out, row)
	}
	return out
}

func (f *fixture) failures(category opstatus.Category) []opstatus.FailureEvent {
	var out []opstatus.FailureEvent
	for _, event := range f.status.Snapshot().Failures {
		if event.Category == category {
			out = append(out, event)
		}
	}
	return out
}

// logged returns the log entries with this msg.
func (f *fixture) logged(msg string) []map[string]any {
	var out []map[string]any
	for _, entry := range f.log.entries(f.t) {
		if entry["msg"] == msg {
			out = append(out, entry)
		}
	}
	return out
}

func plainFixture(t *testing.T, name string) []byte {
	t.Helper()
	original, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", "plain", name))
	if err != nil {
		t.Fatal(err)
	}
	return original
}

// unreadableMessage doesn't start with a header line, so processing can't
// read its header. An import refuses it, and collection keeps it.
func unreadableMessage() []byte {
	return []byte("Kein Kopf vor Teilwort\r\nFrom: anna@example.test\r\nSubject: Zerlegt\r\n\r\nTeilwort\r\n")
}

// collectBytes stores original as a source's folder returned it.
func (f *fixture) collectBytes(original []byte) int64 {
	f.t.Helper()
	statements := []string{
		`INSERT OR IGNORE INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		 VALUES (1, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0)`,
		`INSERT OR IGNORE INTO imap_folder (id, source_id, name, chosen) VALUES (1, 1, 'INBOX', 1)`,
	}
	for _, statement := range statements {
		if _, err := f.db.Writer.Exec(statement); err != nil {
			f.t.Fatal(err)
		}
	}
	result, err := f.archive.StoreCollected(context.Background(), bytes.NewReader(original),
		archive.CollectedMeta{SourceID: 1, FolderID: 1})
	if err != nil {
		f.t.Fatal(err)
	}
	stored, ok := result.(archive.Stored)
	if !ok {
		f.t.Fatalf("collection gave %#v, want Stored", result)
	}
	return stored.Message.ID
}

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

// entries decodes the JSON log lines written so far.
func (b *lockedBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	var entries []map[string]any
	scanner := bufio.NewScanner(strings.NewReader(b.String()))
	for scanner.Scan() {
		var entry map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &entry); err != nil {
			t.Fatalf("log line %q: %v", scanner.Text(), err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func newLogger() (*slog.Logger, *lockedBuffer) {
	log := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(log, nil)), log
}

func itoa64(n int64) string { return strconv.FormatInt(n, 10) }

// archiveHash reads a message's content hash.
func (f *fixture) archiveHash(id int64) string {
	f.t.Helper()
	var hash string
	if err := f.db.Reader.QueryRow("SELECT content_hash FROM message WHERE id = ?", id).Scan(&hash); err != nil {
		f.t.Fatal(err)
	}
	return hash
}
