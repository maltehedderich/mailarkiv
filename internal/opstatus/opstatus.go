// Package opstatus holds what the status screen shows: the start time, the
// recovery result, the unreferenced originals, and the failure events since the
// latest start. Each of these also goes to the log, so a restart loses nothing
// an operator can't still read.
//
// No type here has a field for a subject, a message address, a body, a file
// name, or an account address. That is how FR-029 holds at the type level.
package opstatus

import (
	"context"
	"log/slog"
	"slices"
	"sync"
	"time"
)

// Category is one of FR-038's six failure categories. Its field is private, so
// no other package can make a seventh.
type Category struct{ name string }

var (
	StorageFull           = Category{"storage_full"}
	StorageWriteFailed    = Category{"storage_write_failed"}
	CatalogWriteFailed    = Category{"catalog_write_failed"}
	StoredOriginalMissing = Category{"stored_original_missing"}
	StoredOriginalDamaged = Category{"stored_original_damaged"}
	TempFileNotRemoved    = Category{"temp_file_not_removed"}
)

// String returns the category's name, which the log, the API, and the frontend
// share.
func (c Category) String() string { return c.name }

// RecoveryResult is what startup recovery found.
type RecoveryResult struct {
	FinishedAt          time.Time
	TempFilesRemoved    int
	TempFilesNotRemoved int
	UnreferencedFound   int
	Duration            time.Duration
}

// UnreferencedOriginal is a stored original with no catalog entry. It leaves the
// list when an import of those bytes commits (FR-018).
type UnreferencedOriginal struct {
	ContentHash string
	Size        int64
}

// FailureEvent is one failure since the latest start. MessageID and ContentHash
// are nil where they don't apply. Restored means an import has already replaced
// the stored original.
type FailureEvent struct {
	At          time.Time
	Category    Category
	MessageID   *int64
	ContentHash *string
	Restored    bool
}

// Snapshot is a copy of the status at one moment.
type Snapshot struct {
	StartedAt    time.Time
	Recovery     *RecoveryResult // Nil until recovery has run.
	Unreferenced []UnreferencedOriginal
	Failures     []FailureEvent
}

// Status is safe for concurrent use.
type Status struct {
	logger *slog.Logger
	now    func() time.Time

	mu           sync.Mutex
	startedAt    time.Time
	recovery     *RecoveryResult
	unreferenced []UnreferencedOriginal
	failures     []FailureEvent
}

// New starts the status at the clock's current time.
func New(logger *slog.Logger, now func() time.Time) *Status {
	return &Status{logger: logger, now: now, startedAt: now()}
}

// RecordFailure lists the event at the current time and logs it (FR-028). The
// list has no cap: failure events come only from signed-in actions and startup.
func (s *Status) RecordFailure(event FailureEvent) {
	if event.Category == (Category{}) {
		panic("opstatus: a failure event needs a category")
	}
	event.At = s.now()

	s.mu.Lock()
	s.failures = append(s.failures, event)
	s.mu.Unlock()

	attrs := []slog.Attr{slog.String("category", event.Category.String())}
	if event.MessageID != nil {
		attrs = append(attrs, slog.Int64("message_id", *event.MessageID))
	}
	if event.ContentHash != nil {
		attrs = append(attrs, slog.String("content_hash", *event.ContentHash))
	}
	if event.Restored {
		attrs = append(attrs, slog.Bool("restored", true))
	}
	s.logger.LogAttrs(context.Background(), slog.LevelError, "failure", attrs...)
}

// RecordRecovery keeps startup recovery's result and logs it, with the duration
// the sizing test reads.
func (s *Status) RecordRecovery(result RecoveryResult) {
	s.mu.Lock()
	s.recovery = &result
	s.mu.Unlock()

	s.logger.LogAttrs(context.Background(), slog.LevelInfo, "recovery finished",
		slog.Int("temp_files_removed", result.TempFilesRemoved),
		slog.Int("temp_files_not_removed", result.TempFilesNotRemoved),
		slog.Int("unreferenced_found", result.UnreferencedFound),
		slog.Int64("duration_ms", result.Duration.Milliseconds()),
	)
}

// AddUnreferenced lists a stored original that has no catalog entry and logs it.
func (s *Status) AddUnreferenced(original UnreferencedOriginal) {
	s.mu.Lock()
	s.unreferenced = append(s.unreferenced, original)
	s.mu.Unlock()

	s.logger.LogAttrs(context.Background(), slog.LevelWarn, "unreferenced original",
		slog.String("content_hash", original.ContentHash),
		slog.Int64("size", original.Size),
	)
}

// RemoveUnreferenced takes the original with this hash off the list, if it's
// there.
func (s *Status) RemoveUnreferenced(contentHash string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unreferenced = slices.DeleteFunc(s.unreferenced, func(original UnreferencedOriginal) bool {
		return original.ContentHash == contentHash
	})
}

// ProblemCount is the number every signed-in page shows: failure events plus
// unreferenced originals (FR-039).
func (s *Status) ProblemCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.failures) + len(s.unreferenced)
}

// Snapshot copies the status, so the caller can't change it.
func (s *Status) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()

	snapshot := Snapshot{
		StartedAt:    s.startedAt,
		Unreferenced: slices.Clone(s.unreferenced),
		Failures:     slices.Clone(s.failures),
	}
	if s.recovery != nil {
		recovery := *s.recovery
		snapshot.Recovery = &recovery
	}
	return snapshot
}
