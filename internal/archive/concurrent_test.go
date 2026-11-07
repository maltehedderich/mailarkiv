package archive

import (
	"bytes"
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

// User Story 4 scenario 4, risk 14: two imports of the same new bytes both get a
// confirmation, one Archived and one Already archived, whichever order they
// finish in. Timing decides which interleaving a round gets, so it runs many.
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
