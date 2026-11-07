package web

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/fault"
)

// The pages a browser shows in place of a failed download. The build prerenders
// them from routes/download-failed/[reason].
const (
	missingPageFile = "download-failed/missing.html"
	damagedPageFile = "download-failed/damaged.html"
)

// downloadOriginal answers GET /api/messages/{id}/original with the stored bytes
// (FR-022), after checking them against the content hash (FR-023).
func downloadOriginal(s *server, w http.ResponseWriter, r *http.Request) {
	id, ok := parseMessageID(r)
	if !ok {
		s.writeFailedDownload(w, r, http.StatusNotFound, missingPageFile)
		return
	}
	original, err := s.archive.OpenVerified(r.Context(), id)
	if err == nil {
		defer original.File.Close()
	}

	// The server's 30 second WriteTimeout started when the headers were read, so
	// after a slow hash no answer would leave, including the failure pages.
	controller := http.NewResponseController(w)
	if err := controller.SetWriteDeadline(time.Now().Add(transferStall)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the write deadline: %w", err))
		return
	}
	switch {
	case errors.Is(err, archive.ErrNotFound), errors.Is(err, archive.ErrOriginalMissing):
		s.writeFailedDownload(w, r, http.StatusNotFound, missingPageFile)
		return
	case errors.Is(err, archive.ErrOriginalDamaged):
		s.writeFailedDownload(w, r, http.StatusInternalServerError, damagedPageFile)
		return
	case err != nil:
		s.writeInternalError(w, r, err)
		return
	}

	header := w.Header()
	header.Set("Content-Type", "application/octet-stream")
	header.Set("Content-Disposition", `attachment; filename="`+original.ContentHash+`.eml"`)
	header.Set("Content-Length", strconv.FormatInt(original.Size, 10))
	w.WriteHeader(http.StatusOK)
	// Sent now, so even a failure on the first read shows as a failed download
	// and not an error page. A failed flush means the client left, and the first
	// write reports that.
	_ = controller.Flush()

	sink := &stallWriter{body: w, controller: controller}
	if _, err := io.CopyN(sink, originalReader{original.File}, original.Size); err != nil {
		// A write error means the client left or stalled. Anything else is the
		// stored file failing after its check, including one that got shorter.
		if !sink.failed {
			s.logger.LogAttrs(r.Context(), slog.LevelError, "read a stored original",
				slog.Int64("message_id", id),
				slog.String("error", err.Error()),
			)
			s.archive.RecordDamaged(original)
		}
		// The headers are out, so no status can report this. Ending the
		// connection makes the browser show a failed download, not a short file.
		panic(http.ErrAbortHandler)
	}
}

// writeFailedDownload answers with a page the browser shows in place of the
// download. It has no Content-Disposition, so nothing is saved (FR-023).
func (s *server) writeFailedDownload(w http.ResponseWriter, r *http.Request, status int, name string) {
	page, err := fs.ReadFile(s.files, name)
	if err != nil {
		s.writeInternalError(w, r, fmt.Errorf("read the failed-download page: %w", err))
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(page)))
	w.WriteHeader(status)
	// A failed write means the client left. There's no one to tell.
	_, _ = w.Write(page)
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

// originalReader reads a stored original. The faults build can fail a read here,
// the way a failing disk would after the check passed.
type originalReader struct{ file *os.File }

func (r originalReader) Read(p []byte) (int, error) {
	if err := fault.At("download-read"); err != nil {
		return 0, err
	}
	return r.file.Read(p)
}

// stallWriter moves the write deadline forward before each write, so a download
// that keeps moving can take as long as it needs, and one that stops for
// transferStall ends. It hides the response's ReadFrom, which would send the
// whole file with no deadline in between. It notes a failure, so the caller can
// tell it from a read error after a copy.
type stallWriter struct {
	body       io.Writer
	controller *http.ResponseController
	failed     bool
}

func (s *stallWriter) Write(p []byte) (int, error) {
	if err := s.controller.SetWriteDeadline(time.Now().Add(transferStall)); err != nil {
		s.failed = true
		return 0, fmt.Errorf("set the write deadline: %w", err)
	}
	n, err := s.body.Write(p)
	s.failed = err != nil
	return n, err
}
