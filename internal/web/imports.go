package web

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// transferStall is how long an upload or download may go without moving any
// bytes before the server ends it.
const transferStall = 60 * time.Second

const fileNameHeader = "MailArkiv-File-Name"

// importAnswer is the body of every import result except bad_file_name.
type importAnswer struct {
	Outcome string       `json:"outcome"`
	Reason  string       `json:"reason,omitempty"`
	Message *messageJSON `json:"message,omitempty"`
}

// importFile answers POST /api/imports. The guard has checked the session, so
// no body is read for a caller without one.
func importFile(s *server, w http.ResponseWriter, r *http.Request) {
	if r.ContentLength > archive.MaxSize {
		writeJSON(w, http.StatusRequestEntityTooLarge, importAnswer{Outcome: "refused", Reason: archive.TooLarge.String()})
		return
	}
	fileName, ok := decodeFileName(r.Header)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_file_name")
		return
	}

	// The server's 30 second ReadTimeout would cut off a large upload. A rolling
	// deadline lets a slow upload that keeps moving finish.
	controller := http.NewResponseController(w)
	if err := controller.SetReadDeadline(time.Now().Add(transferStall)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the read deadline: %w", err))
		return
	}
	body := &stallReader{body: r.Body, controller: controller}
	meta := archive.ImportMeta{AccountEmail: signedInAccount(r).Email, FileName: fileName}
	result, importErr := s.archive.Import(r.Context(), body, meta)
	if _, dropped := result.(archive.Dropped); dropped {
		// The body ended early, so no one is waiting for an answer. This ends the
		// connection without writing anything.
		panic(http.ErrAbortHandler)
	}

	// The server's 30 second WriteTimeout started when the headers were read, so
	// after a long upload no answer would leave, including the 500 below.
	if err := controller.SetWriteDeadline(time.Now().Add(transferStall)); err != nil {
		s.writeInternalError(w, r, fmt.Errorf("set the write deadline: %w", err))
		return
	}
	if importErr != nil {
		s.writeInternalError(w, r, importErr)
		return
	}
	switch result := result.(type) {
	case archive.Stored:
		status := http.StatusOK
		if result.Outcome == archive.Archived {
			status = http.StatusCreated
		}
		messages, err := s.encodeMessages(r.Context(), []archive.Message{result.Message})
		if err != nil {
			s.writeInternalError(w, r, err)
			return
		}
		writeJSON(w, status, importAnswer{Outcome: result.Outcome.String(), Message: &messages[0]})
	case archive.Refused:
		status := http.StatusUnprocessableEntity
		if result.Reason == archive.TooLarge {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, importAnswer{Outcome: "refused", Reason: result.Reason.String()})
	case archive.Failed:
		status := http.StatusInternalServerError
		if result.Category == opstatus.StorageFull {
			status = http.StatusInsufficientStorage
		}
		writeJSON(w, status, importAnswer{Outcome: "failed", Reason: result.Category.String()})
	default:
		s.writeInternalError(w, r, fmt.Errorf("unknown import result %T", result))
		return
	}

	// A failed flush means the client left, and the import stands whatever it saw.
	_ = controller.Flush()
	// Only a kill means anything here. The answer is out, so no error path is left.
	_ = fault.At("after-response")
}

// decodeFileName reads the optional name header. It returns false when the
// header isn't valid percent-encoding. Invalid UTF-8 is replaced with U+FFFD,
// so the catalog holds only valid UTF-8.
func decodeFileName(header http.Header) (*string, bool) {
	values := header.Values(fileNameHeader)
	if len(values) == 0 {
		return nil, true
	}
	// PathUnescape, not QueryUnescape: encodeURIComponent leaves "+" as it is.
	name, err := url.PathUnescape(values[0])
	if err != nil {
		return nil, false
	}
	name = strings.ToValidUTF8(name, "�")
	return &name, true
}

// stallReader moves the read deadline forward before each read, so a body that
// keeps arriving can take as long as it needs, and one that stops for
// transferStall ends.
type stallReader struct {
	body       io.Reader
	controller *http.ResponseController
}

func (s *stallReader) Read(p []byte) (int, error) {
	if err := s.controller.SetReadDeadline(time.Now().Add(transferStall)); err != nil {
		return 0, err
	}
	return s.body.Read(p)
}
