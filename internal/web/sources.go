package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
	"unicode/utf8"

	"github.com/maltehedderich/mailarkiv/internal/collect"
)

// maxSourceBody is the largest source request body read.
const maxSourceBody = 1 << 20

// signInWriteDeadline covers a request that signs in to a mail server, which
// collect lets take up to 120 seconds, plus time to answer.
const signInWriteDeadline = 130 * time.Second

// sourceRequest holds each field undecoded, so a field of the wrong type
// breaks only its own rule.
type sourceRequest struct {
	Name     json.RawMessage `json:"name"`
	Host     json.RawMessage `json:"host"`
	Port     json.RawMessage `json:"port"`
	Security json.RawMessage `json:"security"`
	UserName json.RawMessage `json:"user_name"`
	Password json.RawMessage `json:"password"`
	Folders  json.RawMessage `json:"folders"`
}

// addSource answers POST /api/sources. It signs in before saving, so a
// source is saved only if its mailbox accepts the password.
func addSource(s *server, w http.ResponseWriter, r *http.Request) {
	request, ok := decodeSourceRequest(w, r)
	if !ok {
		writeInvalidFields(w, []string{})
		return
	}
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(signInWriteDeadline)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the write deadline: %w", err))
		return
	}

	source, err := s.collect.Add(r.Context(), collect.SourceInput{
		Name:     decodeField[string](request.Name),
		Host:     decodeField[string](request.Host),
		Port:     decodeField[int64](request.Port),
		Security: decodeField[string](request.Security),
		UserName: decodeField[string](request.UserName),
		Password: decodeField[string](request.Password),
		Folders:  decodeField[[]string](request.Folders),
	})
	var invalid *collect.InvalidFieldsError
	var checkFailed *collect.CheckFailedError
	switch {
	case errors.As(err, &invalid):
		writeInvalidFields(w, invalid.Fields)
	case errors.As(err, &checkFailed):
		writeCheckFailed(w, checkFailed)
	case errors.Is(err, collect.ErrNoKey):
		writeError(w, http.StatusConflict, "no_key")
	case err != nil:
		s.writeInternalError(w, r, err)
	default:
		writeJSON(w, http.StatusCreated, newSourceJSON(source))
	}
}

// decodeSourceRequest reads one JSON object of known fields, at most
// maxSourceBody bytes long.
func decodeSourceRequest(w http.ResponseWriter, r *http.Request) (sourceRequest, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSourceBody))
	decoder.DisallowUnknownFields()
	var request sourceRequest
	if err := decoder.Decode(&request); err != nil {
		return sourceRequest{}, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		return sourceRequest{}, false
	}
	return request, true
}

// decodeField decodes one field, or returns the zero value, which breaks
// every input rule, when the field is missing, of the wrong type, or not
// UTF-8. The JSON decoder would replace bytes that aren't UTF-8, so they are
// checked first.
func decodeField[T any](raw json.RawMessage) T {
	var value T
	if raw == nil || !utf8.Valid(raw) || json.Unmarshal(raw, &value) != nil {
		var zero T
		return zero
	}
	return value
}

func writeInvalidFields(w http.ResponseWriter, fields []string) {
	writeJSON(w, http.StatusBadRequest, struct {
		Error  string   `json:"error"`
		Fields []string `json:"fields"`
	}{"invalid_fields", fields})
}

// writeCheckFailed answers a sign-in that failed. The reply is what the
// server sent, so the administrator can tell why.
func writeCheckFailed(w http.ResponseWriter, failed *collect.CheckFailedError) {
	writeJSON(w, http.StatusUnprocessableEntity, struct {
		Error    string `json:"error"`
		Category string `json:"category"`
		Reply    string `json:"reply,omitempty"`
	}{"check_failed", failed.Category.String(), failed.Reply})
}

// listSources answers GET /api/sources.
func listSources(s *server, w http.ResponseWriter, r *http.Request) {
	sources, err := s.collect.List(r.Context())
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	collection := "available"
	if !s.collect.HasKey() {
		collection = "no_key"
	}
	entries := make([]sourceJSON, len(sources))
	for i, source := range sources {
		entries[i] = newSourceJSON(source)
	}
	writeJSON(w, http.StatusOK, struct {
		Collection string       `json:"collection"`
		Sources    []sourceJSON `json:"sources"`
	}{collection, entries})
}

// readSource answers GET /api/sources/{id}, with each folder's state and the
// messages that need attention.
func readSource(s *server, w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	source, err := s.collect.Get(r.Context(), id)
	if errors.Is(err, collect.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	folders := make([]folderJSON, len(source.Folders))
	for i, folder := range source.Folders {
		folders[i] = folderJSON{
			Name:             folder.Name,
			UnavailableSince: optionalTime(folder.UnavailableSince),
			RenumberedAt:     optionalTime(folder.RenumberedAt),
		}
	}
	entry := newSourceJSON(source)
	entry.Folders = folders
	empty := problemMessagesJSON{Items: []struct{}{}}
	writeJSON(w, http.StatusOK, sourceDetailJSON{
		sourceJSON: entry,
		Messages:   messagesJSON{WaitingForRetry: empty, NotArchived: empty},
	})
}

// sourceJSON is a source's list entry.
type sourceJSON struct {
	ID          int64            `json:"id"`
	Name        string           `json:"name"`
	Host        string           `json:"host"`
	Port        int              `json:"port"`
	Security    string           `json:"security"`
	UserName    string           `json:"user_name"`
	State       string           `json:"state"`
	Folders     any              `json:"folders"`
	Progress    *progressJSON    `json:"progress"`
	LastSuccess *successJSON     `json:"last_success"`
	Collected   int              `json:"collected"`
	NextSyncAt  *string          `json:"next_sync_at"`
	PausedAt    *string          `json:"paused_at"`
	Failure     *syncFailureJSON `json:"failure"`
	Problems    problemsJSON     `json:"problems"`
}

type sourceDetailJSON struct {
	sourceJSON
	Messages messagesJSON `json:"messages"`
}

type syncFailureJSON struct {
	Category       string  `json:"category"`
	Since          string  `json:"since"`
	FailedAttempts *int    `json:"failed_attempts"`
	LastAt         *string `json:"last_at"`
	Reply          *string `json:"reply"`
}

type progressJSON struct {
	Archived int `json:"archived"`
	Found    int `json:"found"`
}

type successJSON struct {
	At       string `json:"at"`
	Archived int    `json:"archived"`
}

type folderJSON struct {
	Name             string  `json:"name"`
	UnavailableSince *string `json:"unavailable_since"`
	RenumberedAt     *string `json:"renumbered_at"`
}

type problemsJSON struct {
	UnavailableFolders int `json:"unavailable_folders"`
	WaitingForRetry    int `json:"waiting_for_retry"`
	NotArchived        int `json:"not_archived"`
}

type messagesJSON struct {
	WaitingForRetry problemMessagesJSON `json:"waiting_for_retry"`
	NotArchived     problemMessagesJSON `json:"not_archived"`
}

type problemMessagesJSON struct {
	Total int        `json:"total"`
	Items []struct{} `json:"items"`
}

// newSourceJSON writes a source's list entry. Collection doesn't yet record
// pauses or problem messages, so those fields are always empty.
func newSourceJSON(source collect.Source) sourceJSON {
	names := make([]string, len(source.Folders))
	for i, folder := range source.Folders {
		names[i] = folder.Name
	}
	entry := sourceJSON{
		ID:        source.ID,
		Name:      source.Name,
		Host:      source.Host,
		Port:      source.Port,
		Security:  source.Security.String(),
		UserName:  source.UserName,
		State:     source.State.String(),
		Folders:   names,
		Collected: source.Collected,
	}
	if source.Progress != nil {
		entry.Progress = &progressJSON{Archived: source.Progress.Archived, Found: source.Progress.Found}
	}
	if source.LastSuccess != nil {
		entry.LastSuccess = &successJSON{At: formatTime(source.LastSuccess.At), Archived: source.LastSuccess.Archived}
	}
	entry.NextSyncAt = optionalTime(source.NextSyncAt)
	if failure := source.Failure; failure != nil {
		entry.Failure = &syncFailureJSON{
			Category:       failure.Category.String(),
			Since:          formatTime(failure.Since),
			FailedAttempts: failure.FailedAttempts,
			LastAt:         optionalTime(failure.LastAt),
			Reply:          failure.Reply,
		}
	}
	return entry
}

func optionalTime(t *time.Time) *string {
	if t == nil {
		return nil
	}
	formatted := formatTime(*t)
	return &formatted
}
