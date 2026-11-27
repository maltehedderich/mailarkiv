package web

import (
	"context"
	"fmt"
	"net/http"

	"github.com/maltehedderich/mailarkiv/internal/collect"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/search"
)

type recoveryJSON struct {
	FinishedAt          string `json:"finished_at"`
	TempFilesRemoved    int    `json:"temp_files_removed"`
	TempFilesNotRemoved int    `json:"temp_files_not_removed"`
	UnreferencedFound   int    `json:"unreferenced_found"`
}

type unreferencedJSON struct {
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
}

type problemSourceJSON struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Since          string `json:"since"`
	FailedAttempts *int   `json:"failed_attempts"`
}

type failureJSON struct {
	At          string             `json:"at"`
	Category    string             `json:"category"`
	MessageID   *int64             `json:"message_id,omitempty"`
	ContentHash *string            `json:"content_hash,omitempty"`
	SourceID    *int64             `json:"source_id,omitempty"`
	SourceName  *string            `json:"source_name,omitempty"`
	Restored    bool               `json:"restored"`
	Repeats     int                `json:"repeats"`
	LastAt      string             `json:"last_at"`
	Detail      *failureDetailJSON `json:"detail,omitempty"`
}

// failureDetailJSON holds a search failure's IDs and closed words. Keys that don't
// apply are left out.
type failureDetailJSON struct {
	ExitStatus     *int   `json:"exit_status,omitempty"`
	Signal         *int   `json:"signal,omitempty"`
	Cause          string `json:"cause,omitempty"`
	Operation      string `json:"operation,omitempty"`
	GenerationID   *int64 `json:"generation_id,omitempty"`
	RebuildID      *int64 `json:"rebuild_id,omitempty"`
	FirstMessageID *int64 `json:"first_message_id,omitempty"`
	LastMessageID  *int64 `json:"last_message_id,omitempty"`
	FreeBytes      *int64 `json:"free_bytes,omitempty"`
}

func newFailureDetailJSON(d *opstatus.Detail) *failureDetailJSON {
	if d == nil {
		return nil
	}
	return &failureDetailJSON{
		ExitStatus: d.ExitStatus, Signal: d.Signal, Cause: d.Cause.String(), Operation: d.Operation.String(),
		GenerationID: d.GenerationID, RebuildID: d.RebuildID,
		FirstMessageID: d.FirstMessageID, LastMessageID: d.LastMessageID, FreeBytes: d.FreeBytes,
	}
}

type processingJSON struct {
	NotProcessed          int `json:"not_processed"`
	ProcessedWithProblems int `json:"processed_with_problems"`
	Unreadable            int `json:"unreadable"`
}

type searchStatusJSON struct {
	State        string            `json:"state"`
	NextStartAt  *string           `json:"next_start_at"`
	NeedsRebuild *needsRebuildJSON `json:"needs_rebuild"`
	Restarts     int               `json:"restarts"`
	Indexing     indexingJSON      `json:"indexing"`
	Rebuild      *rebuildJSON      `json:"rebuild"`
}

type needsRebuildJSON struct {
	Cause string `json:"cause"`
	Since string `json:"since"`
}

type indexingJSON struct {
	State           string `json:"state"`
	Waiting         int64  `json:"waiting"`
	Done            *int64 `json:"done"`
	ToDo            *int64 `json:"to_do"`
	FreeBytes       *int64 `json:"free_bytes"`
	PauseBelowBytes int64  `json:"pause_below_bytes"`
	ResumeAtBytes   int64  `json:"resume_at_bytes"`
}

func newSearchStatusJSON(report search.Report) searchStatusJSON {
	status := searchStatusJSON{
		State:    string(report.State.Search),
		Restarts: report.Restarts,
		Indexing: indexingJSON{
			State:           string(report.State.Indexing),
			Waiting:         report.Waiting,
			FreeBytes:       report.FreeBytes,
			PauseBelowBytes: search.PauseBelowBytes,
			ResumeAtBytes:   search.ResumeAtBytes,
		},
	}
	if report.NextStartAt != nil {
		next := formatTime(*report.NextStartAt)
		status.NextStartAt = &next
	}
	if mark := report.NeedsRebuild; mark != nil {
		status.NeedsRebuild = &needsRebuildJSON{Cause: mark.Cause.String(), Since: formatTime(mark.Since)}
	}
	if progress := report.Progress; progress != nil {
		status.Indexing.Done, status.Indexing.ToDo = &progress.Done, &progress.ToDo
	}
	if rebuild := report.Rebuild; rebuild != nil {
		status.Rebuild = &rebuildJSON{ID: rebuild.ID, StartedAt: formatTime(rebuild.StartedAt), StartedBy: rebuild.StartedBy}
	}
	return status
}

// readStatus answers GET /api/status. It reads opstatus, the names of the
// sources its events point to, the sources with problems, the processing
// counts, and the search module's report. No route here changes the archive.
func readStatus(s *server, w http.ResponseWriter, r *http.Request) {
	problemSources, err := s.problemSources(r)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	counts, err := s.archive.ProcessingCounts(r.Context())
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	report, err := s.search.Report(r.Context())
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	snapshot := s.status.Snapshot()
	var sourceIDs []int64
	for _, failure := range snapshot.Failures {
		if failure.SourceID != nil {
			sourceIDs = append(sourceIDs, *failure.SourceID)
		}
	}
	names, err := s.labels.SourceNames(r.Context(), sourceIDs)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	// Recovery runs before the server listens, so a served request always has one.
	var recovery *recoveryJSON
	if result := snapshot.Recovery; result != nil {
		recovery = &recoveryJSON{
			FinishedAt:          formatTime(result.FinishedAt),
			TempFilesRemoved:    result.TempFilesRemoved,
			TempFilesNotRemoved: result.TempFilesNotRemoved,
			UnreferencedFound:   result.UnreferencedFound,
		}
	}
	unreferenced := make([]unreferencedJSON, 0, len(snapshot.Unreferenced))
	for _, original := range snapshot.Unreferenced {
		unreferenced = append(unreferenced, unreferencedJSON{original.ContentHash, original.Size})
	}
	failures := make([]failureJSON, 0, len(snapshot.Failures))
	for _, failure := range snapshot.Failures {
		event := failureJSON{
			At:          formatTime(failure.At),
			Category:    failure.Category.String(),
			MessageID:   failure.MessageID,
			ContentHash: failure.ContentHash,
			SourceID:    failure.SourceID,
			Restored:    failure.Restored,
			Repeats:     failure.Repeats,
			LastAt:      formatTime(failure.LastAt),
			Detail:      newFailureDetailJSON(failure.Detail),
		}
		if failure.SourceID != nil {
			// A source is never deleted, only marked removed, so its name stays.
			name, ok := names[*failure.SourceID]
			if !ok {
				s.writeInternalError(w, r, fmt.Errorf("source %d has no name", *failure.SourceID))
				return
			}
			event.SourceName = &name
		}
		failures = append(failures, event)
	}

	sources := make([]problemSourceJSON, len(problemSources))
	for i, source := range problemSources {
		sources[i] = problemSourceJSON{
			ID:             source.ID,
			Name:           source.Name,
			Category:       source.Category.String(),
			Since:          formatTime(source.Since),
			FailedAttempts: source.FailedAttempts,
		}
	}

	writeJSON(w, http.StatusOK, struct {
		StartedAt    string              `json:"started_at"`
		Processing   processingJSON      `json:"processing"`
		Search       searchStatusJSON    `json:"search"`
		Recovery     *recoveryJSON       `json:"recovery"`
		Sources      []problemSourceJSON `json:"sources"`
		Unreferenced []unreferencedJSON  `json:"unreferenced"`
		Failures     []failureJSON       `json:"failures"`
	}{
		formatTime(snapshot.StartedAt),
		processingJSON{counts.NotProcessed, counts.ProcessedWithProblems, counts.Unreadable},
		newSearchStatusJSON(report),
		recovery, sources, unreferenced, failures,
	})
}

// problemSources returns the sources the status screen lists.
func (s *server) problemSources(r *http.Request) ([]collect.ProblemSource, error) {
	return s.collect.ProblemSources(r.Context())
}

// problemCount is the number every signed-in page shows: every failure with
// its repeats, the unreferenced originals, and the problem sources, plus one
// while search is Unavailable or Needs rebuild and one while indexing is
// Paused. Messages with processing problems have their own list and don't
// count.
func (s *server) problemCount(ctx context.Context) (int, error) {
	sources, err := s.collect.ProblemSources(ctx)
	if err != nil {
		return 0, err
	}
	count := s.status.ProblemCount() + len(sources)
	state := s.search.State()
	if state.Search != search.SearchAvailable {
		count++
	}
	if state.Indexing == search.IndexingPaused {
		count++
	}
	return count, nil
}
