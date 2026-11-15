package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/collect"
)

// messageJSON is a message as the contract writes it in the list and in an
// import's answer.
type messageJSON struct {
	ID               int64  `json:"id"`
	ContentHash      string `json:"content_hash"`
	Size             int64  `json:"size"`
	ObservationCount int    `json:"observation_count"`
	Latest           any    `json:"latest"`
}

type importEntryJSON struct {
	SourceType   string  `json:"source_type"`
	ObservedAt   string  `json:"observed_at"`
	AccountEmail string  `json:"account_email"`
	FileName     *string `json:"file_name"`
}

type collectionEntryJSON struct {
	SourceType string          `json:"source_type"`
	ObservedAt string          `json:"observed_at"`
	Source     entrySourceJSON `json:"source"`
	Folder     string          `json:"folder"`
	ReceivedAt *string         `json:"received_at"`
}

type entrySourceJSON struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Host     string `json:"host"`
	UserName string `json:"user_name"`
	Removed  bool   `json:"removed"`
}

// encodeMessages writes messages as the contract does, with one label lookup
// for all of them.
func (s *server) encodeMessages(ctx context.Context, messages []archive.Message) ([]messageJSON, error) {
	latest := make([]archive.Entry, len(messages))
	for i, message := range messages {
		latest[i] = message.Latest
	}
	entries, err := s.encodeEntries(ctx, latest)
	if err != nil {
		return nil, err
	}
	encoded := make([]messageJSON, len(messages))
	for i, message := range messages {
		encoded[i] = messageJSON{
			ID:               message.ID,
			ContentHash:      message.ContentHash,
			Size:             message.Size,
			ObservationCount: message.ObservationCount,
			Latest:           entries[i],
		}
	}
	return encoded, nil
}

// encodeEntries writes history entries, taking the labels of collection
// entries from one call.
func (s *server) encodeEntries(ctx context.Context, entries []archive.Entry) ([]any, error) {
	var folderIDs []int64
	for _, entry := range entries {
		if collected, ok := entry.(archive.CollectionEntry); ok {
			folderIDs = append(folderIDs, collected.FolderID)
		}
	}
	labels := map[int64]collect.Label{}
	if len(folderIDs) > 0 {
		var err error
		if labels, err = s.labels.Labels(ctx, folderIDs); err != nil {
			return nil, err
		}
	}

	encoded := make([]any, len(entries))
	for i, entry := range entries {
		switch entry := entry.(type) {
		case archive.ImportEntry:
			encoded[i] = importEntryJSON{
				SourceType:   "manual_import",
				ObservedAt:   formatTime(entry.ObservedAt),
				AccountEmail: entry.AccountEmail,
				FileName:     entry.FileName,
			}
		case archive.CollectionEntry:
			label, ok := labels[entry.FolderID]
			if !ok {
				return nil, fmt.Errorf("folder %d has no label", entry.FolderID)
			}
			var receivedAt *string
			if entry.ReceivedAt != nil {
				formatted := formatTime(*entry.ReceivedAt)
				receivedAt = &formatted
			}
			encoded[i] = collectionEntryJSON{
				SourceType: "imap_collection",
				ObservedAt: formatTime(entry.ObservedAt),
				Source: entrySourceJSON{
					ID:       label.Source.ID,
					Name:     label.Source.Name,
					Host:     label.Source.Host,
					UserName: label.Source.UserName,
					Removed:  label.Source.Removed,
				},
				Folder:     label.Folder,
				ReceivedAt: receivedAt,
			}
		default:
			return nil, fmt.Errorf("unknown entry %T", entry)
		}
	}
	return encoded, nil
}

// pageNumber is a page as the contract allows it: a whole number from 1, with
// no sign and no leading zero.
var pageNumber = regexp.MustCompile(`^[1-9][0-9]*$`)

// listMessages answers GET /api/messages?page=N. A page past the end has no
// items and the real total, so the client can move to the last page.
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
	items, err := s.encodeMessages(r.Context(), listed.Items)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
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

// readHistory answers GET /api/messages/{id} with the full content hash and
// every entry, newest first.
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
	observations, err := s.encodeEntries(r.Context(), history.Entries)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		ID           int64  `json:"id"`
		ContentHash  string `json:"content_hash"`
		Size         int64  `json:"size"`
		Observations []any  `json:"observations"`
	}{history.ID, history.ContentHash, history.Size, observations})
}
