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
	Op   string `json:"op"`
	Size int64  `json:"size"`
	// Text asks for the bodies' words, which only the index needs.
	Text bool `json:"text,omitempty"`
}

// readAnswer is a worker's answer to "read". The bodies themselves aren't
// sent: processing stores none, and the index needs only their words.
type readAnswer struct {
	Message mailparse.Message `json:"message"`
	Text    []string          `json:"text,omitempty"`
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
		if request.Op != "read" || request.Size < 0 || request.Size > archive.MaxSize {
			return workerBadRequest
		}
		original := make([]byte, request.Size)
		if _, err := io.ReadFull(reader, original); err != nil {
			return workerBadRequest
		}
		if err := fault.At("parse-worker"); err != nil {
			return workerFault
		}
		if err := WriteFrame(out, read(original, request.Text)); err != nil {
			return workerBadRequest
		}
	}
}

func read(original []byte, withText bool) readAnswer {
	message := mailparse.Read(original)
	answer := readAnswer{Message: message}
	if withText {
		answer.Text = message.Text()
	}
	answer.Message.Plain, answer.Message.HTML = nil, nil
	return answer
}
