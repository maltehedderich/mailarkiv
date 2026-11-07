package web

import "net/http"

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
	Restored    bool    `json:"restored"`
}

// readStatus answers GET /api/status. It only reads opstatus, and no route here
// changes the archive (FR-027).
func readStatus(s *server, w http.ResponseWriter, _ *http.Request) {
	snapshot := s.status.Snapshot()

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
		failures = append(failures, failureJSON{
			At:          formatTime(failure.At),
			Category:    failure.Category.String(),
			MessageID:   failure.MessageID,
			ContentHash: failure.ContentHash,
			Restored:    failure.Restored,
		})
	}

	writeJSON(w, http.StatusOK, struct {
		StartedAt    string             `json:"started_at"`
		Recovery     *recoveryJSON      `json:"recovery"`
		Unreferenced []unreferencedJSON `json:"unreferenced"`
		Failures     []failureJSON      `json:"failures"`
	}{formatTime(snapshot.StartedAt), recovery, unreferenced, failures})
}
