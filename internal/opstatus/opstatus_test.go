package opstatus

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log/slog"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

var (
	startTime = time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC)
	later     = startTime.Add(20 * time.Minute)
)

type fixture struct {
	status *Status
	log    *bytes.Buffer
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{log: &bytes.Buffer{}, now: startTime}
	logger := slog.New(slog.NewJSONHandler(f.log, nil))
	f.status = New(logger, func() time.Time { return f.now })
	return f
}

// logEntries parses the log, so a line that isn't JSON fails here.
func (f *fixture) logEntries(t *testing.T) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(f.log.String()), "\n") {
		if line == "" {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line isn't JSON: %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func pointer[T any](value T) *T { return &value }

// The frontend keys its failure messages by these six category names.
func TestCategoryNames(t *testing.T) {
	tests := []struct {
		category Category
		want     string
	}{
		{StorageFull, "storage_full"},
		{StorageWriteFailed, "storage_write_failed"},
		{CatalogWriteFailed, "catalog_write_failed"},
		{StoredOriginalMissing, "stored_original_missing"},
		{StoredOriginalDamaged, "stored_original_damaged"},
		{TempFileNotRemoved, "temp_file_not_removed"},
	}
	for _, tt := range tests {
		if got := tt.category.String(); got != tt.want {
			t.Errorf("String() = %q, want %q", got, tt.want)
		}
	}
}

// Another package can't name a private field, so it can't make a seventh category.
func TestCategoryIsClosed(t *testing.T) {
	category := reflect.TypeFor[Category]()
	if category.Kind() != reflect.Struct {
		t.Fatalf("Category is a %s, which other packages can convert to", category.Kind())
	}
	for field := range category.Fields() {
		if field.IsExported() {
			t.Errorf("Category exports the field %s", field.Name)
		}
	}
}

// None of these types has a place for a subject, a message address, a body, a
// file name, or an account address. Adding a field fails here, so it gets a
// decision.
func TestEntitiesHoldOnlyTheAllowedFields(t *testing.T) {
	tests := []struct {
		entity reflect.Type
		want   []string
	}{
		{
			reflect.TypeFor[RecoveryResult](),
			[]string{"Duration", "FinishedAt", "TempFilesNotRemoved", "TempFilesRemoved", "UnreferencedFound"},
		},
		{
			reflect.TypeFor[UnreferencedOriginal](),
			[]string{"ContentHash", "Size"},
		},
		{
			reflect.TypeFor[FailureEvent](),
			[]string{"At", "Category", "ContentHash", "MessageID", "Restored"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.entity.Name(), func(t *testing.T) {
			var got []string
			for field := range tt.entity.Fields() {
				got = append(got, field.Name)
			}
			sort.Strings(got)
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("fields = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestStartTimeComesFromTheClock(t *testing.T) {
	f := newFixture(t)

	if got := f.status.Snapshot().StartedAt; !got.Equal(startTime) {
		t.Fatalf("StartedAt = %v, want %v", got, startTime)
	}
}

// Failure events plus unreferenced originals.
func TestProblemCount(t *testing.T) {
	f := newFixture(t)
	if got := f.status.ProblemCount(); got != 0 {
		t.Fatalf("count on a fresh start = %d, want 0", got)
	}

	f.status.RecordFailure(FailureEvent{Category: StorageFull})
	f.status.RecordFailure(FailureEvent{Category: TempFileNotRemoved})
	f.status.AddUnreferenced(UnreferencedOriginal{ContentHash: "aa", Size: 1})
	f.status.AddUnreferenced(UnreferencedOriginal{ContentHash: "bb", Size: 2})
	f.status.AddUnreferenced(UnreferencedOriginal{ContentHash: "cc", Size: 3})
	if got := f.status.ProblemCount(); got != 5 {
		t.Fatalf("count = %d, want 5", got)
	}

	f.status.RemoveUnreferenced("bb")
	if got := f.status.ProblemCount(); got != 4 {
		t.Fatalf("count after removing one = %d, want 4", got)
	}
	want := []UnreferencedOriginal{{ContentHash: "aa", Size: 1}, {ContentHash: "cc", Size: 3}}
	if got := f.status.Snapshot().Unreferenced; !reflect.DeepEqual(got, want) {
		t.Fatalf("Unreferenced = %v, want %v", got, want)
	}

	f.status.RemoveUnreferenced("not listed")
	if got := f.status.ProblemCount(); got != 4 {
		t.Fatalf("count after removing an unlisted hash = %d, want 4", got)
	}
}

func TestFailureListHasNoCap(t *testing.T) {
	f := newFixture(t)
	const count = 10_000

	for range count {
		f.status.RecordFailure(FailureEvent{Category: StorageWriteFailed})
	}

	if got := len(f.status.Snapshot().Failures); got != count {
		t.Fatalf("kept %d failures, want %d", got, count)
	}
	if got := f.status.ProblemCount(); got != count {
		t.Fatalf("count = %d, want %d", got, count)
	}
}

func TestFailureIsListedAndLogged(t *testing.T) {
	hash := strings.Repeat("ab", 32)

	tests := []struct {
		name    string
		event   FailureEvent
		wantLog map[string]any
		absent  []string
	}{
		{
			name:    "no message",
			event:   FailureEvent{Category: StorageFull},
			wantLog: map[string]any{"category": "storage_full"},
			absent:  []string{"message_id", "content_hash", "restored"},
		},
		{
			name:    "content hash only",
			event:   FailureEvent{Category: CatalogWriteFailed, ContentHash: &hash},
			wantLog: map[string]any{"category": "catalog_write_failed", "content_hash": hash},
			absent:  []string{"message_id", "restored"},
		},
		{
			name:    "message and hash",
			event:   FailureEvent{Category: StoredOriginalDamaged, MessageID: pointer(int64(17)), ContentHash: &hash},
			wantLog: map[string]any{"category": "stored_original_damaged", "message_id": float64(17), "content_hash": hash},
			absent:  []string{"restored"},
		},
		{
			name:    "restored",
			event:   FailureEvent{Category: StoredOriginalMissing, MessageID: pointer(int64(17)), ContentHash: &hash, Restored: true},
			wantLog: map[string]any{"category": "stored_original_missing", "message_id": float64(17), "content_hash": hash, "restored": true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t)
			f.now = later

			f.status.RecordFailure(tt.event)

			failures := f.status.Snapshot().Failures
			if len(failures) != 1 {
				t.Fatalf("listed %d failures, want 1", len(failures))
			}
			listed := tt.event
			listed.At = later
			if !reflect.DeepEqual(failures[0], listed) {
				t.Errorf("listed %+v, want %+v", failures[0], listed)
			}

			entries := f.logEntries(t)
			if len(entries) != 1 {
				t.Fatalf("wrote %d log lines, want 1", len(entries))
			}
			entry := entries[0]
			for key, want := range tt.wantLog {
				if entry[key] != want {
					t.Errorf("log %s = %v, want %v", key, entry[key], want)
				}
			}
			for _, key := range tt.absent {
				if _, present := entry[key]; present {
					t.Errorf("log holds %s = %v, want it absent", key, entry[key])
				}
			}
			// Every failure has its time in the log.
			if _, present := entry["time"]; !present {
				t.Error("log line has no time")
			}
		})
	}
}

func TestRecoveryResultIsKeptAndLogged(t *testing.T) {
	f := newFixture(t)
	if got := f.status.Snapshot().Recovery; got != nil {
		t.Fatalf("Recovery before recovery ran = %+v, want nil", got)
	}

	result := RecoveryResult{
		FinishedAt:          later,
		TempFilesRemoved:    2,
		TempFilesNotRemoved: 1,
		UnreferencedFound:   3,
		Duration:            410 * time.Millisecond,
	}
	f.status.RecordRecovery(result)

	got := f.status.Snapshot().Recovery
	if got == nil || *got != result {
		t.Fatalf("Recovery = %+v, want %+v", got, result)
	}

	entries := f.logEntries(t)
	if len(entries) != 1 {
		t.Fatalf("wrote %d log lines, want 1", len(entries))
	}
	want := map[string]any{
		"temp_files_removed":     float64(2),
		"temp_files_not_removed": float64(1),
		"unreferenced_found":     float64(3),
		"duration_ms":            float64(410),
	}
	for key, value := range want {
		if entries[0][key] != value {
			t.Errorf("log %s = %v, want %v", key, entries[0][key], value)
		}
	}
	if _, present := entries[0]["time"]; !present {
		t.Error("log line has no time")
	}
}

// Every recorded event also goes to the log, so a restart loses nothing an
// operator can't still read.
func TestUnreferencedOriginalIsLogged(t *testing.T) {
	f := newFixture(t)
	hash := strings.Repeat("cd", 32)

	f.status.AddUnreferenced(UnreferencedOriginal{ContentHash: hash, Size: 2048})

	entries := f.logEntries(t)
	if len(entries) != 1 {
		t.Fatalf("wrote %d log lines, want 1", len(entries))
	}
	if entries[0]["content_hash"] != hash || entries[0]["size"] != float64(2048) {
		t.Fatalf("log line = %v, want the hash and the size", entries[0])
	}
}

func TestSnapshotIsACopy(t *testing.T) {
	f := newFixture(t)
	f.status.RecordFailure(FailureEvent{Category: StorageFull})
	f.status.AddUnreferenced(UnreferencedOriginal{ContentHash: "aa", Size: 1})
	f.status.RecordRecovery(RecoveryResult{TempFilesRemoved: 1})

	first := f.status.Snapshot()
	first.Failures[0].Category = TempFileNotRemoved
	first.Unreferenced[0].ContentHash = "changed"
	first.Recovery.TempFilesRemoved = 99

	second := f.status.Snapshot()
	if second.Failures[0].Category != StorageFull {
		t.Error("changing a snapshot changed the failure list")
	}
	if second.Unreferenced[0].ContentHash != "aa" {
		t.Error("changing a snapshot changed the unreferenced list")
	}
	if second.Recovery.TempFilesRemoved != 1 {
		t.Error("changing a snapshot changed the recovery result")
	}
}

// Meaningful under -race, which `mise run server:test` always uses.
func TestConcurrentUse(t *testing.T) {
	logger := slog.New(slog.DiscardHandler)
	status := New(logger, time.Now)
	const workers = 8
	const rounds = 200

	var wg sync.WaitGroup
	for worker := range workers {
		wg.Go(func() {
			for round := range rounds {
				hash := fmt.Sprintf("%d-%d", worker, round)
				status.RecordFailure(FailureEvent{Category: StorageFull})
				status.AddUnreferenced(UnreferencedOriginal{ContentHash: hash, Size: 1})
				status.ProblemCount()
				status.Snapshot()
				status.RemoveUnreferenced(hash)
			}
		})
	}
	wg.Wait()

	if got, want := status.ProblemCount(), workers*rounds; got != want {
		t.Fatalf("count = %d, want %d", got, want)
	}
}
