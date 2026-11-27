package search

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// batchLog records each batch the indexer sent, with the cursor stored when
// its answer had been handled.
type batchLog struct {
	f       *fixture
	mu      sync.Mutex
	batches []loggedBatch
}

type loggedBatch struct {
	messages  []int64
	documents int
	commit    bool
	cursor    int64
}

func (l *batchLog) record(batch sentBatch) {
	var cursor int64
	if err := l.f.db.Reader.QueryRow("SELECT cursor FROM search_generation WHERE id = ?", batch.generation).Scan(&cursor); err != nil {
		l.f.t.Error(err)
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.batches = append(l.batches, loggedBatch{batch.messages, batch.documents, batch.commit, cursor})
}

func (l *batchLog) list() []loggedBatch {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.batches)
}

func watchBatches(f *fixture) *batchLog {
	log := &batchLog{f: f}
	f.module.batchSent = log.record
	return log
}

func TestBatchesCommitWhenTheQueueIsEmpty(t *testing.T) {
	tests := []struct {
		name     string
		messages int
		bytes    int
		sizes    []int
	}{
		{"default bounds", 0, 0, []int{5}},
		{"500 messages lowered to 2", 2, 0, []int{2, 2, 1}},
		{"32 MiB lowered below one document", 0, 1, []int{1, 1, 1, 1, 1}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, searchProgram(t))
			if tt.messages > 0 {
				f.module.batchMessages = tt.messages
			}
			if tt.bytes > 0 {
				f.module.batchBytes = tt.bytes
			}
			for _, name := range []string{"plain-text.eml", "html-only.eml", "attachment.eml", "recipients.eml", "no-date.eml"} {
				f.importFixture(name)
			}
			f.runProcessing()
			f.waitForProcessed(5)
			batches := watchBatches(f)
			f.runSearch()
			f.waitForIndexing(IndexingUpToDate)

			got := batches.list()
			if len(got) != len(tt.sizes) {
				t.Fatalf("batches %+v, want sizes %v", got, tt.sizes)
			}
			var seen []int64
			for i, batch := range got {
				last := i == len(got)-1
				if len(batch.messages) != tt.sizes[i] || batch.documents != tt.sizes[i] || batch.commit != last {
					t.Errorf("batch %d is %+v, want %d messages, committed %t", i, batch, tt.sizes[i], last)
				}
				// The cursor moves only after a commit's answer.
				wantCursor := int64(0)
				if last {
					wantCursor = 5
				}
				if batch.cursor != wantCursor {
					t.Errorf("after batch %d the cursor is %d, want %d", i, batch.cursor, wantCursor)
				}
				seen = append(seen, batch.messages...)
			}
			if len(seen) != 5 {
				t.Errorf("batches sent %v, want 5 messages", seen)
			}
		})
	}
}

func TestUnreadableMessageGetsNoDocument(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	unreadable := f.collectBytes(unreadableMessage())
	plan := f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(2)
	batches := watchBatches(f)
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)

	got := batches.list()
	if len(got) != 1 || !slices.Contains(got[0].messages, unreadable) || got[0].documents != 1 || got[0].cursor != 2 {
		t.Errorf("batches %+v, want one holding %d without a document, and the cursor at 2", got, unreadable)
	}
	if ids := f.ids("teilwort"); len(ids) != 0 {
		t.Errorf("a word of the unreadable message finds %v", ids)
	}
	if ids := f.ids("steht"); !slices.Equal(ids, []int64{plan}) {
		t.Errorf("steht finds %v, want %d", ids, plan)
	}
}

func TestMessageCapturedAfterTheBuildIsFound(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	f.importFixture("plain-text.eml")
	f.runProcessing()
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	f.waitFor("the first message indexed", func() bool { return len(f.ids("wochenplan")) == 1 })

	report := f.importFixture("html-only.eml")
	f.waitFor("the new message in results", func() bool { return slices.Equal(f.ids("quartals"), []int64{report}) })
}

func TestMissingOriginalIsIndexedFromItsSummary(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	contract := f.importFixture("attachment.eml")
	f.runProcessing()
	f.waitForProcessed(1)
	hash := f.archiveHash(contract)
	if err := os.Remove(filepath.Join(f.dataDir, "blobs", hash[0:2], hash[2:4], hash)); err != nil {
		t.Fatal(err)
	}

	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	for _, words := range []string{"vertrag", "dora klein"} {
		if got := f.ids(words); !slices.Equal(got, []int64{contract}) {
			t.Errorf("%q finds %v, want %d from the summary", words, got, contract)
		}
	}
	if got := f.ids("anbei"); len(got) != 0 {
		t.Errorf("a body word finds %v without the original", got)
	}
	missing := f.failures(opstatus.StoredOriginalMissing)
	if len(missing) == 0 || *missing[0].MessageID != contract {
		t.Errorf("events %+v, want stored_original_missing for %d", missing, contract)
	}
}

func TestFailedBatchWaitsAndIsSentAgain(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	f.importFixture("plain-text.eml")
	f.runProcessing()
	// Otherwise the build may finish empty, and the first message joins the
	// failing batch.
	f.waitForProcessed(1)

	var mu sync.Mutex
	var waits []time.Duration
	// restoreAt says after how many waits the directory is writable again.
	restoreAt := 3
	var generation string
	f.module.retryAfter = func(d time.Duration) <-chan time.Time {
		mu.Lock()
		waits = append(waits, d)
		if len(waits) == restoreAt {
			if err := os.Chmod(generation, 0o700); err != nil {
				t.Error(err)
			}
		}
		mu.Unlock()
		return time.After(time.Millisecond)
	}
	waitsSoFar := func() []time.Duration {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(waits)
	}
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	rows := f.generations()
	generation = filepath.Join(f.dataDir, "index", itoa64(rows[0].id))

	if err := os.Chmod(generation, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(generation, 0o700) })
	report := f.importFixture("html-only.eml")
	f.waitFor("the batch to commit", func() bool { return slices.Equal(f.ids("quartals"), []int64{report}) })

	if got, want := waitsSoFar(), []time.Duration{time.Second, 2 * time.Second, 4 * time.Second}; !slices.Equal(got, want) {
		t.Errorf("waits %v, want %v", got, want)
	}
	if got := f.failureCount(opstatus.IndexWriteFailed); got != 3 {
		t.Fatalf("%d index_write_failed failures, want 3", got)
	}
	for _, event := range f.failures(opstatus.IndexWriteFailed) {
		d := event.Detail
		if d == nil || d.GenerationID == nil || d.FirstMessageID == nil || d.LastMessageID == nil {
			t.Errorf("detail %+v, want generation and message IDs", d)
			continue
		}
		if *d.GenerationID != rows[0].id || *d.FirstMessageID != report || *d.LastMessageID != report ||
			d.Cause != opstatus.CausePermissionDenied {
			t.Errorf("detail generation %d, messages %d to %d, cause %s; want generation %d, message %d, permission_denied",
				*d.GenerationID, *d.FirstMessageID, *d.LastMessageID, d.Cause, rows[0].id, report)
		}
	}

	// A committed batch resets the wait.
	mu.Lock()
	restoreAt = 4
	mu.Unlock()
	if err := os.Chmod(generation, 0o500); err != nil {
		t.Fatal(err)
	}
	contract := f.importFixture("attachment.eml")
	f.waitFor("the second batch to commit", func() bool { return slices.Equal(f.ids("vertrag"), []int64{contract}) })
	if got := waitsSoFar(); len(got) != 4 || got[3] != time.Second {
		t.Errorf("waits %v, want a fourth of 1 s", got)
	}
}

func TestSearchCountsWhatIsNotIncluded(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	plan := f.importFixture("plain-text.eml")
	ctx, stopProcessing := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.processor.Run(ctx)
	}()
	f.waitForProcessed(1)
	stopProcessing()
	<-done
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)

	f.importFixture("html-only.eml")
	result, err := f.module.Search(context.Background(), Query{Words: "wochenplan", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0].ID != plan || result.Total != 1 || result.NotIncluded != 1 || result.FullList {
		t.Errorf("result %+v, want %d, total 1, 1 not included", result, plan)
	}
	if result.Rows[0].Summary == nil || *result.Rows[0].Summary.Subject != "Wochenplan" {
		t.Errorf("row %+v, want the list row with its summary", result.Rows[0])
	}

	// Words without letters or digits give the full list.
	result, err = f.module.Search(context.Background(), Query{Words: " -- ", Page: 1})
	if err != nil {
		t.Fatal(err)
	}
	if !result.FullList || result.Total != 2 || len(result.Rows) != 2 || result.NotIncluded != 0 {
		t.Errorf("result %+v, want the full list of 2", result)
	}
}

func TestCallLimitValues(t *testing.T) {
	tests := []struct {
		operation opstatus.Operation
		want      time.Duration
	}{
		{opstatus.OperationOpen, 60 * time.Second},
		{opstatus.OperationIndex, 120 * time.Second},
		{opstatus.OperationSearch, 12 * time.Second},
		{opstatus.OperationMaintain, 30 * time.Minute},
		{opstatus.OperationClose, 60 * time.Second},
		{opstatus.OperationSize, 10 * time.Second},
	}
	for _, tt := range tests {
		if got := callLimit(tt.operation); got != tt.want {
			t.Errorf("%s: limit %s, want %s", tt.operation, got, tt.want)
		}
	}
}
