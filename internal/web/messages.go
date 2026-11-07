package web

import (
	"errors"
	"net/http"
	"regexp"
	"strconv"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

// messageJSON is a message as the contract writes it in the list and in an
// import's answer.
type messageJSON struct {
	ID          int64   `json:"id"`
	ContentHash string  `json:"content_hash"`
	Size        int64   `json:"size"`
	FileName    *string `json:"file_name"`
	ImportedAt  string  `json:"imported_at"`
	ImportCount int     `json:"import_count"`
}

func newMessageJSON(m archive.Message) messageJSON {
	return messageJSON{
		ID:          m.ID,
		ContentHash: m.ContentHash,
		Size:        m.Size,
		FileName:    m.FileName,
		ImportedAt:  formatTime(m.ImportedAt),
		ImportCount: m.ImportCount,
	}
}

// pageNumber is a page as the contract allows it: a whole number from 1, with
// no sign and no leading zero.
var pageNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// listMessages answers GET /api/messages?page=N. A page past the end has no
// items and the real total, so the client can move to the last page (FR-019).
func listMessages(s *server, w http.ResponseWriter, r *http.Request) {
	page := 1
	if values, given := r.URL.Query()["page"]; given {
		if len(values) != 1 || !pageNumber.MatchString(values[0]) {
			writeError(w, http.StatusBadRequest, "bad_page")
			return
		}
		number, err := strconv.Atoi(values[0])
		if err != nil {
			// Only a number too large for an int gets here.
			writeError(w, http.StatusBadRequest, "bad_page")
			return
		}
		page = number
	}

	listed, err := s.archive.List(r.Context(), page)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	items := make([]messageJSON, 0, len(listed.Items))
	for _, message := range listed.Items {
		items = append(items, newMessageJSON(message))
	}
	writeJSON(w, http.StatusOK, struct {
		Items    []messageJSON `json:"items"`
		Page     int           `json:"page"`
		PageSize int           `json:"page_size"`
		Total    int           `json:"total"`
	}{items, page, archive.PageSize, listed.Total})
}

// parseMessageID reads the path's {id}. Anything that isn't a number can't name
// a message, so the caller treats it as an unknown ID.
func parseMessageID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil
}

type observationJSON struct {
	SourceType   string  `json:"source_type"`
	ObservedAt   string  `json:"observed_at"`
	AccountEmail string  `json:"account_email"`
	FileName     *string `json:"file_name"`
}

// readHistory answers GET /api/messages/{id} with the full content hash and
// every import, newest first (FR-020).
func readHistory(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := parseMessageID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	history, err := s.archive.History(r.Context(), id)
	if errors.Is(err, archive.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	observations := make([]observationJSON, 0, len(history.Observations))
	for _, observation := range history.Observations {
		observations = append(observations, observationJSON{
			SourceType:   observation.SourceType,
			ObservedAt:   formatTime(observation.ObservedAt),
			AccountEmail: observation.AccountEmail,
			FileName:     observation.FileName,
		})
	}
	writeJSON(w, http.StatusOK, struct {
		ID           int64             `json:"id"`
		ContentHash  string            `json:"content_hash"`
		Size         int64             `json:"size"`
		Observations []observationJSON `json:"observations"`
	}{history.ID, history.ContentHash, history.Size, observations})
}
