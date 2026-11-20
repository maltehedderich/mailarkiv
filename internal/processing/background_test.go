package processing

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

type processingFixture struct {
	t         *testing.T
	dataDir   string
	db        *sqlitedb.DB
	archive   *archive.Archive
	status    *opstatus.Status
	pool      *Pool
	processor *Processor
	log       *lockedBuffer
}

func newProcessingFixture(t *testing.T) *processingFixture {
	t.Helper()
	return newProcessingFixtureWith(t, 2, testLimit, testWorkerCommand)
}

func newProcessingFixtureWith(t *testing.T, workers int, limit time.Duration, command func() *exec.Cmd) *processingFixture {
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
	status := opstatus.New(logger, time.Now)
	a, err := archive.Open(dataDir, db, status, logger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	pool, err := NewPool(workers, limit, command, logger)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return &processingFixture{
		t: t, dataDir: dataDir, db: db, archive: a, status: status, pool: pool,
		processor: New(a, pool, logger), log: log,
	}
}

func (f *processingFixture) importBytes(original []byte) archive.Stored {
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
	return stored
}

func (f *processingFixture) importFixture(name string) int64 {
	f.t.Helper()
	return f.importBytes(plainFixture(f.t, name)).Message.ID
}

type processingRow struct {
	state string
	seq   int64
}

func (f *processingFixture) processing(id int64) processingRow {
	f.t.Helper()
	var row processingRow
	var seq *int64
	err := f.db.Reader.QueryRow("SELECT processing_state, processed_seq FROM message WHERE id = ?", id).Scan(&row.state, &seq)
	if err != nil {
		f.t.Fatal(err)
	}
	if seq != nil {
		row.seq = *seq
	}
	return row
}

func (f *processingFixture) row(id int64) archive.Message {
	f.t.Helper()
	rows, err := f.archive.Rows(context.Background(), []int64{id})
	if err != nil || len(rows) != 1 {
		f.t.Fatalf("Rows(%d) = %v, %v", id, rows, err)
	}
	return rows[0]
}

func (f *processingFixture) runOnce(processor *Processor) int {
	f.t.Helper()
	taken, err := processor.runOnce(context.Background())
	if err != nil {
		f.t.Fatalf("runOnce: %v", err)
	}
	return taken
}

// run starts processing in the background until the test ends.
func (f *processingFixture) run() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.processor.Run(ctx)
	}()
	f.t.Cleanup(func() {
		cancel()
		<-done
	})
}

// waitForState polls until a message reaches state, for at most 30 s.
func (f *processingFixture) waitForState(id int64, state string) {
	f.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for f.processing(id).state != state {
		if time.Now().After(deadline) {
			f.t.Fatalf("message %d is %s after 30 s, want %s", id, f.processing(id).state, state)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestProcessingOrderAndStates(t *testing.T) {
	f := newProcessingFixture(t)
	names := []string{"plain-text.eml", "html-only.eml", "attachment.eml", "encoded-subject.eml", "no-date.eml"}
	var ids []int64
	for _, name := range names {
		ids = append(ids, f.importFixture(name))
	}

	if taken := f.runOnce(f.processor); taken != 5 {
		t.Fatalf("processing took %d messages, want 5", taken)
	}
	// Newest capture first, and no gaps.
	for i, id := range ids {
		want := processingRow{state: "processed", seq: int64(len(ids) - i)}
		if got := f.processing(id); got != want {
			t.Errorf("%s: %+v, want %+v", names[i], got, want)
		}
	}

	row := f.row(ids[2])
	if s := row.Summary; s == nil || *s.SenderName != "Dora Klein" || *s.SenderAddress != "dora@example.test" ||
		*s.Subject != "Vertrag" || !s.HasAttachments || s.DateReason != nil {
		t.Errorf("attachment.eml's summary %+v", row.Summary)
	}
	if want := time.Date(2026, 11, 7, 14, 0, 0, 0, time.UTC); !row.ListDate.Equal(want) || row.ListDateIsArchiveTime {
		t.Errorf("attachment.eml's list date %s, archive time %t; want %s from its header", row.ListDate, row.ListDateIsArchiveTime, want)
	}
	row = f.row(ids[4])
	if row.Summary == nil || row.Summary.DateReason == nil || *row.Summary.DateReason != "missing" || !row.ListDateIsArchiveTime {
		t.Errorf("no-date.eml reads as %+v, want its archive time with the reason missing", row)
	}

	select {
	case <-f.processor.Processed():
	default:
		t.Error("results didn't signal search")
	}

	// A new processor on the same catalog finds nothing to do.
	again := New(f.archive, f.pool, slogDiscard())
	if taken := f.runOnce(again); taken != 0 {
		t.Errorf("a second processor took %d messages, want 0", taken)
	}
}

func TestProcessingWakesOnCapture(t *testing.T) {
	f := newProcessingFixture(t)
	f.run()

	first := f.importFixture("plain-text.eml")
	f.waitForState(first, "processed")

	// An unknown character set is a problem, not a failure.
	withProblem := f.importBytes([]byte("From: anna@example.test\r\nContent-Type: text/plain; charset=x-unknown\r\n\r\nK\xf6ln\r\n")).Message.ID
	f.waitForState(withProblem, "processed_with_problems")
	var position, declared, kind string
	err := f.db.Reader.QueryRow("SELECT position, declared_type, kind FROM message_problem WHERE message_id = ?", withProblem).
		Scan(&position, &declared, &kind)
	if err != nil || position != "1" || declared != "text/plain" || kind != "unknown_charset" {
		t.Errorf("problem %q %q %q, %v; want unknown_charset in part 1, text/plain", position, declared, kind, err)
	}
	if got := f.processing(withProblem).seq; got != 2 {
		t.Errorf("the second message has processed_seq %d, want 2", got)
	}
}

func TestProcessingSkipsMissingOriginal(t *testing.T) {
	f := newProcessingFixture(t)
	original := plainFixture(t, "plain-text.eml")
	stored := f.importBytes(original)
	id, hash := stored.Message.ID, stored.Message.ContentHash
	if err := os.Remove(filepath.Join(f.dataDir, "blobs", hash[0:2], hash[2:4], hash)); err != nil {
		t.Fatal(err)
	}

	for range 2 {
		f.runOnce(f.processor)
	}
	if got := f.processing(id); got.state != "not_processed" {
		t.Errorf("a message without its original is %s, want not_processed", got.state)
	}
	var events int
	for _, event := range f.status.Snapshot().Failures {
		if event.Category == opstatus.StoredOriginalMissing && *event.MessageID == id {
			events++
		}
	}
	if events != 1 {
		t.Errorf("%d stored_original_missing events, want 1 across two runs", events)
	}
	var skipped int
	for _, entry := range f.log.entries(t) {
		if entry["msg"] == "processing skipped" {
			skipped++
			if entry["message_id"] != float64(id) || entry["category"] != "stored_original_missing" || entry["level"] != "WARN" {
				t.Errorf("log entry %v, want message_id %d and category stored_original_missing at WARN", entry, id)
			}
		}
	}
	if skipped != 1 {
		t.Errorf("logged processing skipped %d times, want once", skipped)
	}

	// Importing the file again restores the original, and processing reads it.
	if restored := f.importBytes(original); restored.Outcome != archive.Restored {
		t.Fatalf("import gave %v, want Restored", restored.Outcome)
	}
	f.run()
	f.waitForState(id, "processed")
}

func TestReadForIndex(t *testing.T) {
	f := newProcessingFixture(t)
	id := f.importFixture("recipients.eml")
	content, err := f.processor.Read(context.Background(), id, true)
	if err != nil {
		t.Fatal(err)
	}
	if content.Subject == nil || *content.Subject != "Empfaenger" {
		t.Errorf("subject %v", content.Subject)
	}
	wantRecipients := []Address{
		{Name: "Team", Address: "team@example.test"},
		{Address: "bob@example.test"},
		{Name: "Carla Braun", Address: "carla@example.test"},
		{Address: "geheim@example.test"},
	}
	if len(content.Recipients) != len(wantRecipients) {
		t.Fatalf("recipients %+v, want %+v", content.Recipients, wantRecipients)
	}
	for i, want := range wantRecipients {
		if content.Recipients[i] != want {
			t.Errorf("recipient %d is %+v, want %+v", i, content.Recipients[i], want)
		}
	}
	if len(content.Text) != 1 || content.Text[0] != "Body.\n" {
		t.Errorf("text %q, want the plain body", content.Text)
	}

	attachment := f.importFixture("attachment.eml")
	content, err = f.processor.Read(context.Background(), attachment, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(content.AttachmentNames) != 1 || content.AttachmentNames[0] != "Vertrag_2026.pdf" || content.Text != nil {
		t.Errorf("attachment names %q and text %q, want Vertrag_2026.pdf and no text", content.AttachmentNames, content.Text)
	}
}
