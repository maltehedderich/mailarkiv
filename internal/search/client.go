package search

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
)

// callLimit is how long the server waits for an answer to op. A call that
// passes it records Search process didn't answer.
func callLimit(op opstatus.Operation) time.Duration {
	switch op {
	case opstatus.OperationOpen, opstatus.OperationClose:
		return 60 * time.Second
	case opstatus.OperationIndex:
		return 120 * time.Second
	case opstatus.OperationSearch:
		// The search process stops a search at 10 s, so it normally answers
		// timed_out first.
		return 12 * time.Second
	case opstatus.OperationMaintain:
		return 30 * time.Minute
	case opstatus.OperationSize:
		return 10 * time.Second
	}
	panic("search: no time limit for the operation " + op.String())
}

var (
	// errStopped means the search process exited or its output broke, so
	// the call has no answer.
	errStopped = errors.New("the search process stopped")
	// ErrDidNotAnswer means a call passed its time limit.
	ErrDidNotAnswer = errors.New("the search process didn't answer")
)

// answerError is an error code from the search process, with the cause some
// codes carry.
type answerError struct {
	code  string
	cause string
}

func (e *answerError) Error() string { return "the search process answered " + e.code }

func isAnswer(err error, code string) bool {
	answer, ok := errors.AsType[*answerError](err)
	return ok && answer.code == code
}

// incoming is any frame from the search process: an answer, or a log record.
type incoming struct {
	Type   string          `json:"type"`
	ID     int64           `json:"id"`
	OK     json.RawMessage `json:"ok"`
	Error  string          `json:"error"`
	Cause  string          `json:"cause"`
	Level  string          `json:"level"`
	Target string          `json:"target"`
}

// client sends numbered requests and matches answers to them, which may
// arrive in any order. It is safe for concurrent use.
type client struct {
	status *opstatus.Status
	logger *slog.Logger
	// done closes when the search process's output ends or breaks.
	done chan struct{}

	writeMu sync.Mutex
	stdin   io.WriteCloser

	mu      sync.Mutex
	nextID  int64
	pending map[int64]chan incoming
	stopped bool
}

func newClient(stdin io.WriteCloser, stdout *bufio.Reader, status *opstatus.Status, logger *slog.Logger) *client {
	c := &client{
		status:  status,
		logger:  logger,
		done:    make(chan struct{}),
		stdin:   stdin,
		pending: make(map[int64]chan incoming),
	}
	go c.read(stdout)
	return c
}

func (c *client) read(stdout *bufio.Reader) {
	defer close(c.done)
	for {
		var frame incoming
		if err := processing.ReadFrame(stdout, &frame); err != nil {
			break
		}
		if frame.Type == "log" {
			c.logger.Warn("search process log", "level", frame.Level, "target", frame.Target)
			continue
		}
		c.mu.Lock()
		answer, waiting := c.pending[frame.ID]
		delete(c.pending, frame.ID)
		c.mu.Unlock()
		// An answer nobody waits for came after its call's limit.
		if waiting {
			answer <- frame
		}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.stopped = true
	for id, answer := range c.pending {
		close(answer)
		delete(c.pending, id)
	}
}

// call sends the request that build makes for a fresh ID and decodes the
// answer's ok object into result, which may be nil.
func (c *client) call(ctx context.Context, op opstatus.Operation, build func(id int64) any, result any) error {
	return c.callSent(ctx, op, build, result, nil)
}

// callSent is call that also runs sent, if set, once the request is written
// or can't be, before the wait for the answer.
func (c *client) callSent(ctx context.Context, op opstatus.Operation, build func(id int64) any, result any, sent func()) error {
	written := func() {
		if sent != nil {
			sent()
			sent = nil
		}
	}
	defer written()
	answer := make(chan incoming, 1)
	c.mu.Lock()
	if c.stopped {
		c.mu.Unlock()
		return errStopped
	}
	c.nextID++
	id := c.nextID
	c.pending[id] = answer
	c.mu.Unlock()
	forget := func() {
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
	}

	c.writeMu.Lock()
	err := processing.WriteFrame(c.stdin, build(id))
	c.writeMu.Unlock()
	written()
	if err != nil {
		forget()
		return errStopped
	}

	limit := time.NewTimer(callLimit(op))
	defer limit.Stop()
	select {
	case frame, ok := <-answer:
		if !ok {
			return errStopped
		}
		if frame.Error != "" {
			return &answerError{code: frame.Error, cause: frame.Cause}
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(frame.OK, result); err != nil {
			return errors.New("the search process answered " + op.String() + " in a shape the server doesn't read")
		}
		return nil
	case <-limit.C:
		forget()
		c.status.RecordFailure(opstatus.FailureEvent{
			Category: opstatus.SearchProcessDidNotAnswer,
			Detail:   &opstatus.Detail{Operation: op},
		})
		return ErrDidNotAnswer
	case <-ctx.Done():
		forget()
		return ctx.Err()
	}
}

// close closes the search process's standard input, which asks it to exit.
// It doesn't wait for writeMu: closing also ends a write the process isn't
// reading.
func (c *client) close() {
	_ = c.stdin.Close()
}
