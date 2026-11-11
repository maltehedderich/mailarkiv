package web

import (
	"fmt"
	"net/http"
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

type failureJSON struct {
	At          string  `json:"at"`
	Category    string  `json:"category"`
	MessageID   *int64  `json:"message_id,omitempty"`
	ContentHash *string `json:"content_hash,omitempty"`
	SourceID    *int64  `json:"source_id,omitempty"`
	SourceName  *string `json:"source_name,omitempty"`
	Restored    bool    `json:"restored"`
}

// readStatus answers GET /api/status. It reads opstatus and the names of the
// sources its events point to, and no route here changes the archive.
func readStatus(s *server, w http.ResponseWriter, r *http.Request) {
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

	writeJSON(w, http.StatusOK, struct {
		StartedAt    string             `json:"started_at"`
		Recovery     *recoveryJSON      `json:"recovery"`
		Unreferenced []unreferencedJSON `json:"unreferenced"`
		Failures     []failureJSON      `json:"failures"`
	}{formatTime(snapshot.StartedAt), recovery, unreferenced, failures})
}
