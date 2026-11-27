package web

import (
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/processing"
)

// The view's policies. The preview runs sandboxed with no script, loads
// nothing but inline styles and data: images, and may only sit in a frame of
// this site. Bodies and attachments opened as a page show nothing active.
const (
	previewCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; " +
		"frame-ancestors 'self'; base-uri 'none'; form-action 'none'"
	previewWithImagesCSP = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data: https:; " +
		"frame-ancestors 'self'; base-uri 'none'; form-action 'none'; upgrade-insecure-requests"
	inertCSP = "sandbox; default-src 'none'; frame-ancestors 'none'"
)

// viewDeadline covers the 30 s wait for an interactive worker and its 60 s
// limit, which the server's 30 s write timeout would cut short.
const viewDeadline = 100 * time.Second

type contentJSON struct {
	ID          int64            `json:"id"`
	ContentHash string           `json:"content_hash"`
	Size        int64            `json:"size"`
	Processing  string           `json:"processing"`
	Problems    []problemJSON    `json:"problems"`
	ListDate    string           `json:"list_date"`
	DateReason  *string          `json:"date_reason"`
	DateHeader  *string          `json:"date_header"`
	Subject     *string          `json:"subject"`
	From        []addressJSON    `json:"from"`
	To          []addressJSON    `json:"to"`
	Cc          []addressJSON    `json:"cc"`
	Bcc         []addressJSON    `json:"bcc"`
	Encrypted   *bool            `json:"encrypted"`
	HTML        *htmlJSON        `json:"html"`
	Text        *textJSON        `json:"text"`
	Attachments []attachmentJSON `json:"attachments"`
}

// problemJSON's Position is nil for the whole message.
type problemJSON struct {
	Position     *string `json:"position"`
	DeclaredType *string `json:"declared_type"`
	Kind         string  `json:"kind"`
}

// addressJSON's Address is nil when the header wasn't an address list.
type addressJSON struct {
	Name    *string `json:"name"`
	Address *string `json:"address"`
}

type htmlJSON struct {
	Truncated             bool `json:"truncated"`
	RemoteImages          bool `json:"remote_images"`
	EmbeddedImagesLeftOut bool `json:"embedded_images_left_out"`
}

type textJSON struct {
	Truncated bool `json:"truncated"`
}

type attachmentJSON struct {
	Position     string  `json:"position"`
	Name         *string `json:"name"`
	DeclaredType *string `json:"declared_type"`
	Size         int64   `json:"size"`
	Damaged      bool    `json:"damaged"`
	Kind         string  `json:"kind"`
}

func encodeContent(view processing.MessageView) contentJSON {
	message := view.Message
	content := contentJSON{
		ID:          message.ID,
		ContentHash: message.ContentHash,
		Size:        message.Size,
		Processing:  message.Processing.String(),
		Problems:    []problemJSON{},
		ListDate:    formatTime(message.ListDate),
		Attachments: []attachmentJSON{},
	}
	for _, problem := range view.Problems {
		content.Problems = append(content.Problems, problemJSON{
			Position: nonEmpty(problem.Position), DeclaredType: problem.DeclaredType, Kind: problem.Kind,
		})
	}
	if summary := message.Summary; summary != nil {
		content.DateReason = summary.DateReason
	}
	parsed := view.Parsed
	if parsed == nil {
		return content
	}
	if summary := message.Summary; summary != nil {
		content.DateHeader = summary.DateHeader
	}
	content.Subject = parsed.Subject
	content.From = encodeAddresses(parsed.From)
	content.To = encodeAddresses(parsed.To)
	content.Cc = encodeAddresses(parsed.Cc)
	content.Bcc = encodeAddresses(parsed.Bcc)
	content.Encrypted = &parsed.Encrypted
	if body := parsed.HTML; body != nil {
		content.HTML = &htmlJSON{body.Truncated, body.RemoteImages, body.EmbeddedImagesLeftOut}
	}
	if body := parsed.Text; body != nil {
		content.Text = &textJSON{body.Truncated}
	}
	for _, a := range parsed.Attachments {
		content.Attachments = append(content.Attachments, attachmentJSON{
			Position: a.Position, Name: a.Name, DeclaredType: nonEmpty(a.DeclaredType),
			Size: a.Size, Damaged: a.Damaged, Kind: a.Kind,
		})
	}
	return content
}

func encodeAddresses(addresses []processing.Address) []addressJSON {
	encoded := []addressJSON{}
	for _, a := range addresses {
		encoded = append(encoded, addressJSON{Name: nonEmpty(a.Name), Address: nonEmpty(a.Address)})
	}
	return encoded
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// readContent answers GET /api/messages/{id}/content.
func readContent(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := parseMessageID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !s.raiseDeadline(w, r) {
		return
	}
	view, err := s.processing.Content(r.Context(), id)
	if err != nil {
		s.writeViewError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, encodeContent(view))
}

// readText answers GET /api/messages/{id}/text with the plain text body.
func readText(s *server, w http.ResponseWriter, r *http.Request) {
	replacePolicies(w, inertCSP, "")
	id, ok := parseMessageID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !s.raiseDeadline(w, r) {
		return
	}
	body, err := s.processing.Text(r.Context(), id)
	if err != nil {
		s.writeViewError(w, r, err)
		return
	}
	writeBody(w, "text/plain; charset=utf-8", body)
}

// readPreview answers GET /api/messages/{id}/html with the rewritten HTML
// body. Its headers go first, so every answer carries them, errors included.
func readPreview(s *server, w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w, r)
	if r.URL.RawQuery != "" && r.URL.RawQuery != "images=1" {
		writeError(w, http.StatusBadRequest, "bad_query")
		return
	}
	id, ok := parseMessageID(r)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !s.raiseDeadline(w, r) {
		return
	}
	body, err := s.processing.HTML(r.Context(), id)
	if err != nil {
		s.writeViewError(w, r, err)
		return
	}
	writeBody(w, "text/html; charset=utf-8", body)
}

// setPreviewHeaders sets the preview's policies. Only the exact query
// images=1 allows images from the internet.
func setPreviewHeaders(w http.ResponseWriter, r *http.Request) {
	policy := previewCSP
	if r.URL.RawQuery == "images=1" {
		policy = previewWithImagesCSP
	}
	replacePolicies(w, policy, "no-referrer")
	w.Header().Set("X-DNS-Prefetch-Control", "off")
}

// previewWithoutSession answers the preview for a caller without a valid
// session. Opened as a page, it goes to sign-in as a download does. In the
// frame, a JSON error would show as the message, so the 401 is empty.
func previewWithoutSession(w http.ResponseWriter, r *http.Request) {
	setPreviewHeaders(w, r)
	if r.Header.Get("Sec-Fetch-Dest") == "document" {
		seeSignIn(w, r)
		return
	}
	w.Header().Set("Content-Length", "0")
	w.WriteHeader(http.StatusUnauthorized)
}

// downloadAttachment answers GET /api/messages/{id}/attachments/{position}
// with the decoded part, as a file to save and never a page to show.
func downloadAttachment(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := parseMessageID(r)
	position := r.PathValue("position")
	if !ok || !isPartNumber(position) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if !s.raiseDeadline(w, r) {
		return
	}
	controller := http.NewResponseController(w)
	started := false
	err := s.processing.Attachment(r.Context(), id, position, func(file processing.AttachmentFile) (io.Writer, error) {
		started = true
		header := w.Header()
		replacePolicies(w, inertCSP, "")
		header.Set("Content-Type", "application/octet-stream")
		header.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": file.Name}))
		header.Set("Content-Length", strconv.FormatInt(file.Size, 10))
		w.WriteHeader(http.StatusOK)
		// Sent now, so a worker that fails before the first byte shows as a
		// broken transfer. A failed flush means the client left, and the
		// first write reports that.
		_ = controller.Flush()
		return &stallWriter{body: w, controller: controller}, nil
	})
	if err == nil {
		return
	}
	if started {
		// The headers are out, so no status can report this. Ending the
		// connection makes the client see a broken transfer, not a short file.
		panic(http.ErrAbortHandler)
	}
	if errors.Is(err, processing.ErrNoAttachment) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	s.writeViewError(w, r, err)
}

// isPartNumber reports whether position is a part number: numbers from 1,
// joined by dots.
func isPartNumber(position string) bool {
	for piece := range strings.SplitSeq(position, ".") {
		if piece == "" || piece[0] == '0' || strings.Trim(piece, "0123456789") != "" {
			return false
		}
	}
	return true
}

// raiseDeadline gives the view's routes time for the worker. It answers 500
// itself when it can't.
func (s *server) raiseDeadline(w http.ResponseWriter, r *http.Request) bool {
	if err := http.NewResponseController(w).SetWriteDeadline(time.Now().Add(viewDeadline)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the write deadline: %w", err))
		return false
	}
	return true
}

// writeViewError answers a failure of the viewer, as the content route's
// table gives it.
func (s *server) writeViewError(w http.ResponseWriter, r *http.Request, err error) {
	var stopped *processing.WorkerStoppedError
	switch {
	case errors.Is(err, archive.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, processing.ErrNoBody):
		writeError(w, http.StatusNotFound, "no_body")
	case errors.Is(err, archive.ErrOriginalMissing):
		writeError(w, http.StatusNotFound, "stored_original_missing")
	case errors.Is(err, archive.ErrOriginalDamaged):
		writeError(w, http.StatusInternalServerError, "stored_original_damaged")
	case errors.Is(err, processing.ErrBusy):
		writeError(w, http.StatusServiceUnavailable, "busy")
	case errors.As(err, &stopped):
		writeError(w, http.StatusInternalServerError, "could_not_read")
	default:
		s.writeInternalError(w, r, err)
	}
}

// writeBody writes a body of the given type, with its length, so a client can
// tell it arrived whole.
func writeBody(w http.ResponseWriter, contentType, body string) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	// A failed write means the client left. There's no one to tell.
	_, _ = io.WriteString(w, body)
}
