package archive

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// Two imports of the same new bytes both get a confirmation, one Archived and
// one Already archived, whichever order they finish in. Timing decides which
// interleaving a round gets, so it runs many.
func TestConcurrentImports(t *testing.T) {
	f := newFixture(t)

	for round := range 50 {
		content := fmt.Appendf(nil, "From: sender@example.test\r\nSubject: Round %d\r\n\r\nBody.\r\n", round)
		blobsBefore := len(files(t, filepath.Join(f.dataDir, "blobs")))
		observationsBefore := f.count(t, "SELECT COUNT(*) FROM source_observation")

		release := make(chan struct{})
		results := make([]Result, 2)
		errs := make([]error, 2)
		var finished sync.WaitGroup
		for i := range results {
			finished.Go(func() {
				<-release
				results[i], errs[i] = f.archive.Import(context.Background(), bytes.NewReader(content), ImportMeta{AccountEmail: account, FileName: name("same.eml")})
			})
		}
		close(release)
		finished.Wait()

		outcomes := map[Outcome]int{}
		var ids [2]int64
		for i, result := range results {
			if errs[i] != nil {
				t.Fatalf("round %d: import %d: %v", round, i, errs[i])
			}
			stored, ok := result.(Stored)
			if !ok {
				t.Fatalf("round %d: import %d = %#v, want Stored", round, i, result)
			}
			outcomes[stored.Outcome]++
			ids[i] = stored.Message.ID
		}
		if outcomes[Archived] != 1 || outcomes[AlreadyArchived] != 1 {
			t.Fatalf("round %d: outcomes = %v, want one Archived and one Already archived", round, outcomes)
		}
		if ids[0] != ids[1] {
			t.Fatalf("round %d: message IDs = %v, want one message", round, ids)
		}

		if got := len(files(t, filepath.Join(f.dataDir, "blobs"))) - blobsBefore; got != 1 {
			t.Fatalf("round %d: %d new blobs, want 1", round, got)
		}
		if got := f.count(t, "SELECT COUNT(*) FROM source_observation") - observationsBefore; got != 2 {
			t.Fatalf("round %d: %d new observations, want 2", round, got)
		}
		f.assertTempEmpty(t)
		if failures := f.status.Snapshot().Failures; len(failures) != 0 {
			t.Fatalf("round %d: failures = %+v, want none", round, failures)
		}
	}

	if got := f.count(t, "SELECT COUNT(*) FROM message"); got != 50 {
		t.Errorf("message rows = %d, want 50", got)
	}
}

// Hold both captures after their real inspection and before either replacement.
// This forces the schedules that a simultaneous start alone cannot guarantee.
func TestConcurrentImportsAfterInspection(t *testing.T) {
	for _, scenario := range []struct {
		name      string
		existing  bool
		damaged   bool
		wantBlob  blobState
		wantEvent opstatus.Category
	}{
		{name: "both observe a new absent blob", wantBlob: blobAbsent},
		{name: "both observe a missing original", existing: true, wantBlob: blobAbsent, wantEvent: opstatus.StoredOriginalMissing},
		{name: "both observe a damaged original", existing: true, damaged: true, wantBlob: blobNotMatching, wantEvent: opstatus.StoredOriginalDamaged},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			f := newFixture(t)
			body := []byte("Subject: controlled overlap\r\n\r\nOriginal bytes.\r\n")
			hash := hashOf(body)
			observations := 2
			if scenario.existing {
				f.importBytes(t, body, name("initial.eml"))
				observations++
				path := f.archive.blobPath(hash)
				if scenario.damaged {
					if err := os.WriteFile(path, []byte("damaged bytes"), 0o600); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			}

			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			inspected := make(chan blobState, 2)
			release := make(chan struct{})
			f.archive.inspectBlob = func(path, contentHash string) blobState {
				found := inspectBlob(path, contentHash)
				inspected <- found
				select {
				case <-release:
				case <-ctx.Done():
					t.Error("timed out waiting to release blob inspection")
				}
				return found
			}
			first := beginConcurrentImport(t, f, body, "first.eml")
			second := beginConcurrentImport(t, f, body, "second.eml")
			for range 2 {
				select {
				case found := <-inspected:
					if found != scenario.wantBlob {
						t.Errorf("inspection found %v, want %v", found, scenario.wantBlob)
					}
				case <-ctx.Done():
					t.Fatal("both imports did not reach blob inspection")
				}
			}
			close(release)
			results := []Stored{awaitConcurrentImport(t, first), awaitConcurrentImport(t, second)}
			outcomes := map[Outcome]int{}
			for _, result := range results {
				outcomes[result.Outcome]++
			}
			if scenario.existing {
				if outcomes[Restored] != 2 {
					t.Errorf("outcomes = %v, want two Restored results", outcomes)
				}
			} else if outcomes[Archived] != 1 || outcomes[AlreadyArchived] != 1 {
				t.Errorf("outcomes = %v, want one Archived and one Already archived", outcomes)
			}
			assertConcurrentCapture(t, f, body, results, observations)
			failures := f.status.Snapshot().Failures
			if !scenario.existing {
				if len(failures) != 0 {
					t.Errorf("failures = %+v, want none", failures)
				}
				return
			}
			if len(failures) != 2 {
				t.Fatalf("failures = %+v, want two repair events", failures)
			}
			for _, event := range failures {
				if event.Category != scenario.wantEvent || !event.Restored ||
					event.ContentHash == nil || *event.ContentHash != hash ||
					event.MessageID == nil || *event.MessageID != results[0].Message.ID {
					t.Errorf("repair event = %+v, want %v for the restored message", event, scenario.wantEvent)
				}
			}
		})
	}
}

func TestConcurrentImportInspectsAfterOtherCommit(t *testing.T) {
	f := newFixture(t)
	body := []byte("Subject: commit before inspection\r\n\r\nOriginal bytes.\r\n")
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	secondAtInspection := make(chan struct{})
	firstCommitted := make(chan struct{})
	var inspection sync.Mutex
	arrivals := 0
	f.archive.inspectBlob = func(path, contentHash string) blobState {
		inspection.Lock()
		arrivals++
		order := arrivals
		inspection.Unlock()
		if order == 2 {
			close(secondAtInspection)
			select {
			case <-firstCommitted:
			case <-ctx.Done():
				t.Error("timed out waiting for the first commit")
			}
		}
		found := inspectBlob(path, contentHash)
		want := blobAbsent
		if order == 2 {
			want = blobMatching
		}
		if found != want {
			t.Errorf("inspection %d found %v, want %v", order, found, want)
		}
		return found
	}
	first := beginConcurrentImport(t, f, body, "first.eml")
	second := beginConcurrentImport(t, f, body, "second.eml")
	select {
	case <-secondAtInspection:
	case <-ctx.Done():
		t.Fatal("second import did not reach blob inspection")
	}
	// Either worker may inspect first. Receive its confirmation before allowing
	// the other inspector to open the now-durable original.
	var committed Stored
	var remaining <-chan concurrentImportResult
	select {
	case result := <-first:
		committed = requireStoredImport(t, result)
		remaining = second
	case result := <-second:
		committed = requireStoredImport(t, result)
		remaining = first
	case <-ctx.Done():
		t.Fatal("first import did not commit")
	}
	close(firstCommitted)
	later := awaitConcurrentImport(t, remaining)
	if committed.Outcome != Archived || later.Outcome != AlreadyArchived {
		t.Errorf("outcomes = %v, %v, want Archived then Already archived", committed.Outcome, later.Outcome)
	}
	assertConcurrentCapture(t, f, body, []Stored{committed, later}, 2)
	if failures := f.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

type concurrentImportResult struct {
	result Result
	err    error
}

func beginConcurrentImport(t *testing.T, f *fixture, body []byte, fileName string) <-chan concurrentImportResult {
	t.Helper()
	finished := make(chan concurrentImportResult, 1)
	done := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("concurrent import was still running during cleanup")
		}
	})
	go func() {
		defer close(done)
		result, err := f.archive.Import(context.Background(), bytes.NewReader(body), ImportMeta{AccountEmail: account, FileName: name(fileName)})
		finished <- concurrentImportResult{result, err}
	}()
	return finished
}

func awaitConcurrentImport(t *testing.T, finished <-chan concurrentImportResult) Stored {
	t.Helper()
	select {
	case result := <-finished:
		return requireStoredImport(t, result)
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent import did not finish")
		return Stored{}
	}
}

func requireStoredImport(t *testing.T, finished concurrentImportResult) Stored {
	t.Helper()
	if finished.err != nil {
		t.Fatal(finished.err)
	}
	stored, ok := finished.result.(Stored)
	if !ok {
		t.Fatalf("import = %#v, want Stored", finished.result)
	}
	return stored
}

func assertConcurrentCapture(t *testing.T, f *fixture, body []byte, results []Stored, observations int) {
	t.Helper()
	hash := hashOf(body)
	for _, result := range results {
		if result.Message.ID != results[0].Message.ID || result.Message.ContentHash != hash || result.Message.Size != int64(len(body)) {
			t.Errorf("message = %+v, want the same ID, hash, and size", result.Message)
		}
	}
	f.assertBytes(t, f.archive.blobPath(hash), body)
	f.assertTempEmpty(t)
	if got := len(files(t, filepath.Join(f.dataDir, "blobs"))); got != 1 {
		t.Errorf("blob count = %d, want 1", got)
	}
	if got := f.count(t, "SELECT COUNT(*) FROM message"); got != 1 {
		t.Errorf("message count = %d, want 1", got)
	}
	if got := f.count(t, "SELECT COUNT(*) FROM source_observation"); got != observations {
		t.Errorf("observation count = %d, want %d", got, observations)
	}
	if got := f.count(t, "SELECT COUNT(DISTINCT file_name) FROM source_observation WHERE file_name IN ('first.eml', 'second.eml')"); got != 2 {
		t.Errorf("concurrent observations = %d, want both supplied names", got)
	}
	page, err := f.archive.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != 1 || len(page.Items) != 1 || page.Items[0].ObservationCount != observations {
		t.Errorf("listed messages = %+v, want one message with %d observations", page, observations)
	}
}
