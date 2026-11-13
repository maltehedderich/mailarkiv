package web

import (
	"context"
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

// checkRequest is the body of POST /api/sources/check.
type checkRequest struct {
	Host     json.RawMessage `json:"host"`
	Port     json.RawMessage `json:"port"`
	Security json.RawMessage `json:"security"`
	UserName json.RawMessage `json:"user_name"`
	Password json.RawMessage `json:"password"`
}

// changeRequest is the body of POST /api/sources/{id}/check.
type changeRequest struct {
	Port     json.RawMessage `json:"port"`
	Security json.RawMessage `json:"security"`
	Password json.RawMessage `json:"password"`
}

// addSource answers POST /api/sources. It signs in before saving, so a
// source is saved only if its mailbox accepts the password.
func addSource(s *server, w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBody[sourceRequest](w, r)
	if !ok || !s.allowSignIn(w, r) {
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
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusCreated, newSourceJSON(source))
}

// checkMailbox answers POST /api/sources/check: it signs in with new details
// and lists the server's folders.
func checkMailbox(s *server, w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBody[checkRequest](w, r)
	if !ok || !s.allowSignIn(w, r) {
		return
	}
	folders, err := s.collect.Check(r.Context(), collect.CheckInput{
		Host:     decodeField[string](request.Host),
		Port:     decodeField[int64](request.Port),
		Security: decodeField[string](request.Security),
		UserName: decodeField[string](request.UserName),
		Password: decodeField[string](request.Password),
	})
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeServerFolders(w, folders)
}

// checkStoredSource answers POST /api/sources/{id}/check: it signs in with
// the stored details, or the ones the request changes, and lists folders.
func checkStoredSource(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	request, ok := decodeBody[changeRequest](w, r)
	if !ok || !s.allowSignIn(w, r) {
		return
	}
	folders, err := s.collect.CheckSource(r.Context(), id, collect.MailboxChange{
		Port:     optional[int64](request.Port),
		Security: optional[string](request.Security),
		Password: optionalPassword(request.Password),
	})
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeServerFolders(w, folders)
}

// editSource answers PATCH /api/sources/{id}. A change of port, security,
// or password signs in first, so it has the sign-in limits.
func editSource(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	request, ok := decodeBody[sourceRequest](w, r)
	if !ok || !s.allowSignIn(w, r) {
		return
	}
	source, err := s.collect.Edit(r.Context(), id, collect.EditInput{
		Name:     optional[string](request.Name),
		Host:     optional[string](request.Host),
		UserName: optional[string](request.UserName),
		MailboxChange: collect.MailboxChange{
			Port:     optional[int64](request.Port),
			Security: optional[string](request.Security),
			Password: optionalPassword(request.Password),
		},
		Folders: optional[[]string](request.Folders),
	})
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newSourceDetailJSON(source))
}

// pauseSource, resumeSource, and syncSource answer
// POST /api/sources/{id}/pause, /resume, and /sync. They take no body.
func pauseSource(s *server, w http.ResponseWriter, r *http.Request) {
	s.act(w, r, s.collect.Pause)
}

func resumeSource(s *server, w http.ResponseWriter, r *http.Request) {
	s.act(w, r, s.collect.Resume)
}

func syncSource(s *server, w http.ResponseWriter, r *http.Request) {
	s.act(w, r, s.collect.SyncNow)
}

// act runs an action on the source the path names and answers its list
// entry.
func (s *server) act(w http.ResponseWriter, r *http.Request, action func(context.Context, int64) (collect.Source, error)) {
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	source, err := action(r.Context(), id)
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newSourceJSON(source))
}

// removeSource answers DELETE /api/sources/{id}.
func removeSource(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	if err := s.collect.Remove(r.Context(), id); err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sourceID reads the path's source ID. One that isn't a number answers 404,
// like an unknown one.
func sourceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return 0, false
	}
	return id, true
}

// allowSignIn raises the write deadline for a request that may sign in to a
// mail server. It answers 500 and returns false when it can't.
func (s *server) allowSignIn(w http.ResponseWriter, r *http.Request) bool {
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(signInWriteDeadline)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the write deadline: %w", err))
		return false
	}
	return true
}

// writeSourceError answers an error from a source action.
func (s *server) writeSourceError(w http.ResponseWriter, r *http.Request, err error) {
	var invalid *collect.InvalidFieldsError
	var checkFailed *collect.CheckFailedError
	var exists *collect.SourceExistsError
	switch {
	case errors.As(err, &invalid):
		writeInvalidFields(w, invalid.Fields)
	case errors.As(err, &checkFailed):
		writeCheckFailed(w, checkFailed)
	case errors.As(err, &exists):
		writeJSON(w, http.StatusConflict, struct {
			Error    string `json:"error"`
			SourceID int64  `json:"source_id"`
		}{"source_exists", exists.ID})
	case errors.Is(err, collect.ErrNoKey):
		writeError(w, http.StatusConflict, "no_key")
	case errors.Is(err, collect.ErrAlreadySyncing):
		writeError(w, http.StatusConflict, "already_syncing")
	case errors.Is(err, collect.ErrPaused):
		writeError(w, http.StatusConflict, "paused")
	case errors.Is(err, collect.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	default:
		s.writeInternalError(w, r, err)
	}
}

func writeServerFolders(w http.ResponseWriter, folders []collect.ServerFolder) {
	type folderJSON struct {
		Name     string `json:"name"`
		Messages *int   `json:"messages"`
	}
	entries := make([]folderJSON, len(folders))
	for i, folder := range folders {
		entries[i] = folderJSON{Name: folder.Name, Messages: folder.Messages}
	}
	writeJSON(w, http.StatusOK, struct {
		Folders []folderJSON `json:"folders"`
	}{entries})
}

// decodeBody reads one JSON object of known fields, at most maxSourceBody
// bytes long. Otherwise it answers 400 naming no field.
func decodeBody[T any](w http.ResponseWriter, r *http.Request) (T, bool) {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxSourceBody))
	decoder.DisallowUnknownFields()
	var request, zero T
	if err := decoder.Decode(&request); err != nil {
		writeInvalidFields(w, []string{})
		return zero, false
	}
	if _, err := decoder.Token(); !errors.Is(err, io.EOF) {
		writeInvalidFields(w, []string{})
		return zero, false
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

// optional decodes a field the request may leave out, which is nil then. A
// field of the wrong type, or that isn't UTF-8, becomes the zero value,
// which breaks every input rule.
func optional[T any](raw json.RawMessage) *T {
	if raw == nil {
		return nil
	}
	value := decodeField[T](raw)
	return &value
}

// optionalPassword is optional for a password, where an empty string keeps
// the stored one. A value that isn't a UTF-8 string becomes one that isn't
// valid UTF-8, so it still breaks the password rule.
func optionalPassword(raw json.RawMessage) *string {
	if raw == nil {
		return nil
	}
	var password string
	if !utf8.Valid(raw) || json.Unmarshal(raw, &password) != nil {
		broken := "\xff"
		return &broken
	}
	return &password
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
	id, ok := sourceID(w, r)
	if !ok {
		return
	}
	source, err := s.collect.Get(r.Context(), id)
	if err != nil {
		s.writeSourceError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, newSourceDetailJSON(source))
}

// newSourceDetailJSON writes a source's page. Collection doesn't yet record
// problem messages, so those lists are always empty.
func newSourceDetailJSON(source collect.Source) sourceDetailJSON {
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
	return sourceDetailJSON{
		sourceJSON: entry,
		Messages:   messagesJSON{WaitingForRetry: empty, NotArchived: empty},
	}
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

// newSourceJSON writes a source's list entry. Collection doesn't yet count
// problems, so those counts are always zero.
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
	entry.PausedAt = optionalTime(source.PausedAt)
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
