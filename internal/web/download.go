package web

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

// downloadOriginal answers GET /api/messages/{id}/original with the stored
// bytes (FR-022). T094 adds the hash check before the headers.
func downloadOriginal(s *server, w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	original, err := s.archive.OpenOriginal(r.Context(), id)
	if errors.Is(err, archive.ErrNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	defer original.File.Close()

	header := w.Header()
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Disposition", `attachment; filename="`+original.ContentHash+`.eml"`)
	header.Set("Content-Length", strconv.FormatInt(original.Size, 10))
	w.WriteHeader(http.StatusOK)

	source := &readErrorTracker{body: original.File}
	sink := &stallWriter{body: w, controller: http.NewResponseController(w)}
	if _, err := io.Copy(sink, source); err != nil {
		// A write error means the client left or stalled. A read error is the
		// server's, so the operator needs it. T094 records it as an event.
		if source.err != nil {
			s.logger.LogAttrs(r.Context(), slog.LevelError, "read a stored original",
				slog.Int64("message_id", id),
				slog.String("error", source.err.Error()),
			)
		}
		// The headers are out, so no status can report this. Ending the
		// connection makes the browser show a failed download, not a short file.
		panic(http.ErrAbortHandler)
	}
}

// redirectToSignIn answers the download route for a caller without a valid
// session. A plain link has no page to report a 401 on, so a navigation goes to
// sign-in, which offers a way back to the page the link was on. Anything else
// gets the usual 401.
func redirectToSignIn(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Sec-Fetch-Mode") != "navigate" {
		answerNoSession(w, r)
		return
	}
	w.Header().Set("Location", "/sign-in?ended=1&next="+url.QueryEscape(returnPath(r)))
	w.WriteHeader(http.StatusSeeOther)
}

// returnPath is the path and query of the page that linked here, or / when the
// Referer is missing, unreadable, or from another host. The scheme isn't
// compared, because TLS may end at a proxy. Only the path and query leave here,
// and the sign-in page checks them again before using them.
func returnPath(r *http.Request) string {
	referer, err := url.Parse(r.Referer())
	if err != nil || referer.Host != r.Host || referer.EscapedPath() == "" {
		return "/"
	}
	if referer.RawQuery == "" {
		return referer.EscapedPath()
	}
	return referer.EscapedPath() + "?" + referer.RawQuery
}

// readErrorTracker remembers a read error, so the caller can tell it from a
// write error after io.Copy.
type readErrorTracker struct {
	body io.Reader
	err  error
}

func (t *readErrorTracker) Read(p []byte) (int, error) {
	n, err := t.body.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		t.err = err
	}
	return n, err
}

// stallWriter moves the write deadline forward before each write, so a download
// that keeps moving can take as long as it needs, and one that stops for
// transferStall ends. It hides the response's ReadFrom, which would send the
// whole file with no deadline in between.
type stallWriter struct {
	body       io.Writer
	controller *http.ResponseController
}

func (s *stallWriter) Write(p []byte) (int, error) {
	if err := s.controller.SetWriteDeadline(time.Now().Add(transferStall)); err != nil {
		return 0, fmt.Errorf("set the write deadline: %w", err)
	}
	return s.body.Write(p)
}
