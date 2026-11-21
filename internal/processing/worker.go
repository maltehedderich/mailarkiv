package processing

import (
	"bufio"
	"errors"
	"io"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/mailparse"
)

// Exit statuses of a worker.
const (
	workerDone       = 0
	workerBadRequest = 3
	// workerFault is a test's fault at parse-worker that returned an error.
	workerFault = 4
)

// workerRequest is one request to a worker. The original's Size bytes follow
// the frame, because an original can be larger than a frame.
type workerRequest struct {
	// Op is read, html, text, or attachment.
	Op   string `json:"op"`
	Size int64  `json:"size"`
	// Text asks read for the bodies' words, which only the index needs.
	Text bool `json:"text,omitempty"`
	// View asks read for what the message view shows beyond the fields.
	View bool `json:"view,omitempty"`
	// Position names the part attachment sends.
	Position string `json:"position,omitempty"`
}

var workerOps = map[string]bool{"read": true, "html": true, "text": true, "attachment": true}

// readAnswer is a worker's answer to "read". The bodies themselves aren't
// sent: processing stores none, the index needs only their words, and the
// view asks for each body on its own.
type readAnswer struct {
	Message mailparse.Message `json:"message"`
	Text    []string          `json:"text,omitempty"`
	View    *viewAnswer       `json:"view,omitempty"`
}

// viewAnswer describes the bodies for the message view. Each is nil when the
// message lacks that body.
type viewAnswer struct {
	HTML *htmlFlags `json:"html"`
	Text *textFlags `json:"text"`
}

type htmlFlags struct {
	Truncated             bool `json:"truncated"`
	RemoteImages          bool `json:"remote_images"`
	EmbeddedImagesLeftOut bool `json:"embedded_images_left_out"`
}

type textFlags struct {
	Truncated bool `json:"truncated"`
}

// bodyAnswer is a worker's answer to "html" and "text". Body is nil when the
// message lacks that body.
type bodyAnswer struct {
	Body *string `json:"body"`
}

// attachmentAnswer comes first in a worker's answer to "attachment". When
// Found, Size bytes follow it, because an attachment can be larger than a
// frame. Name is the cleaned file name.
type attachmentAnswer struct {
	Found bool   `json:"found"`
	Name  string `json:"name,omitempty"`
	Size  int64  `json:"size,omitempty"`
}

// RunWorker answers requests on in, one at a time, until in closes. It is the
// whole of the parse-worker subcommand and opens no file or connection. It
// returns the process's exit status: 0 when in closes, 3 for a request that
// breaks the format, since server and worker are the same binary and only a
// bug sends one, and 4 when a test's fault stops it.
func RunWorker(in io.Reader, out io.Writer) int {
	reader := bufio.NewReader(in)
	for {
		var request workerRequest
		if err := ReadFrame(reader, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return workerDone
			}
			return workerBadRequest
		}
		if !workerOps[request.Op] || request.Size < 0 || request.Size > archive.MaxSize {
			return workerBadRequest
		}
		original := make([]byte, request.Size)
		if _, err := io.ReadFull(reader, original); err != nil {
			return workerBadRequest
		}
		if err := fault.At("parse-worker"); err != nil {
			return workerFault
		}
		if err := respond(out, request, original); err != nil {
			return workerBadRequest
		}
	}
}

// respond writes the answer to one request.
func respond(out io.Writer, request workerRequest, original []byte) error {
	switch request.Op {
	case "html":
		var answer bodyAnswer
		if _, preview := mailparse.ReadView(original); preview != nil {
			answer.Body = &preview.HTML
		}
		return WriteFrame(out, answer)
	case "text":
		var answer bodyAnswer
		if plain := mailparse.Read(original).Plain; plain != nil {
			answer.Body = &plain.Content
		}
		return WriteFrame(out, answer)
	case "attachment":
		attachment, content, ok := mailparse.AttachmentContent(original, request.Position)
		if !ok {
			return WriteFrame(out, attachmentAnswer{})
		}
		name := mailparse.CleanFileName(attachment.Name, attachment.Position, attachment.Kind)
		if err := WriteFrame(out, attachmentAnswer{Found: true, Name: name, Size: int64(len(content))}); err != nil {
			return err
		}
		_, err := out.Write(content)
		return err
	default:
		return WriteFrame(out, read(original, request.Text, request.View))
	}
}

func read(original []byte, withText, withView bool) readAnswer {
	var message mailparse.Message
	var preview *mailparse.Preview
	if withView {
		message, preview = mailparse.ReadView(original)
	} else {
		message = mailparse.Read(original)
	}
	answer := readAnswer{Message: message}
	if withText {
		answer.Text = message.Text()
	}
	if withView {
		answer.View = &viewAnswer{}
		if preview != nil {
			answer.View.HTML = &htmlFlags{
				Truncated:             message.HTML.Truncated,
				RemoteImages:          preview.RemoteImages,
				EmbeddedImagesLeftOut: preview.EmbeddedImagesLeftOut,
			}
		}
		if message.Plain != nil {
			answer.View.Text = &textFlags{Truncated: message.Plain.Truncated}
		}
	}
	answer.Message.Plain, answer.Message.HTML = nil, nil
	return answer
}
