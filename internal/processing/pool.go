package processing

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// MessageLimit is the most time a worker may spend on one message. The
// bounds on parts and depth keep real mail far below it.
const MessageLimit = 60 * time.Second

// The pools' sizes. Processing and indexing share the background pool, and
// the message view gets its own, so neither waits for the other.
const (
	BackgroundWorkers  = 2
	InteractiveWorkers = 2
	// InteractiveWait is how long a view waits for an interactive worker.
	InteractiveWait = 30 * time.Second
)

// Pool keeps a fixed number of long-lived workers and hands each request to
// an idle one. A worker that exits is started again. Pool is safe for
// concurrent use.
type Pool struct {
	command func() *exec.Cmd
	limit   time.Duration
	logger  *slog.Logger
	idle    chan *worker

	mu      sync.Mutex
	workers map[*worker]bool
	closed  bool
}

// errPoolClosed means the server is stopping.
var errPoolClosed = errors.New("the worker pool is closed")

// WorkerStoppedError means a worker exited before it answered, or was killed
// at the limit.
type WorkerStoppedError struct {
	// Exit is the worker's exit status or signal, as the log shows it.
	Exit string
	// TookTooLong is true when the pool killed the worker at the limit.
	TookTooLong bool
}

func (e *WorkerStoppedError) Error() string { return "the worker stopped: " + e.Exit }

// WorkerCommand returns a function that starts executable as a worker, with
// an empty environment except for MAILARKIV_FAULTS, which fault builds read.
func WorkerCommand(executable string) func() *exec.Cmd {
	return func() *exec.Cmd {
		cmd := exec.Command(executable, "parse-worker")
		cmd.Env = []string{}
		if faults, ok := os.LookupEnv("MAILARKIV_FAULTS"); ok {
			cmd.Env = append(cmd.Env, "MAILARKIV_FAULTS="+faults)
		}
		return cmd
	}
}

// NewPool starts size workers, each from command. A worker that spends
// longer than limit on one request is killed.
func NewPool(size int, limit time.Duration, command func() *exec.Cmd, logger *slog.Logger) (*Pool, error) {
	p := &Pool{
		command: command, limit: limit, logger: logger,
		idle: make(chan *worker, size), workers: make(map[*worker]bool),
	}
	for range size {
		w, err := p.start()
		if err != nil {
			p.Close()
			return nil, err
		}
		p.idle <- w
	}
	return p, nil
}

// Close stops every worker. Requests in progress fail.
func (p *Pool) Close() {
	p.mu.Lock()
	p.closed = true
	workers := make([]*worker, 0, len(p.workers))
	for w := range p.workers {
		workers = append(workers, w)
	}
	p.mu.Unlock()
	for _, w := range workers {
		w.stop()
	}
}

type worker struct {
	cmd    *exec.Cmd
	stdin  *os.File
	stdout *bufio.Reader
	// exited closes when the process has exited and been waited for.
	exited chan struct{}
	// busy is true while a request has the worker. The pool's mutex guards it.
	busy     bool
	replaced sync.Once
}

// start starts one worker. It must run on a goroutine that doesn't lock its
// OS thread: Pdeathsig follows the thread that forked, not the process.
func (p *Pool) start() (*worker, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil, errPoolClosed
	}
	// Our own pipes, so Wait never closes the ends we read and write.
	childIn, stdin, err := os.Pipe()
	if err != nil {
		return nil, fmt.Errorf("start a worker: %w", err)
	}
	stdout, childOut, err := os.Pipe()
	if err != nil {
		childIn.Close()
		stdin.Close()
		return nil, fmt.Errorf("start a worker: %w", err)
	}
	cmd := p.command()
	cmd.Stdin, cmd.Stdout = childIn, childOut
	// Standard error goes nowhere: a crash report could quote the message.
	cmd.Stderr = nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	err = cmd.Start()
	childIn.Close()
	childOut.Close()
	if err != nil {
		stdin.Close()
		stdout.Close()
		return nil, fmt.Errorf("start a worker: %w", err)
	}
	w := &worker{cmd: cmd, stdin: stdin, stdout: bufio.NewReader(stdout), exited: make(chan struct{})}
	go func() {
		_ = cmd.Wait()
		stdin.Close()
		stdout.Close()
		close(w.exited)
		// A worker that dies while idle is replaced now. One that dies
		// during a request is replaced by that request.
		p.mu.Lock()
		idle := !w.busy
		p.mu.Unlock()
		if idle {
			p.replace(w)
		}
	}()
	p.workers[w] = true
	return w, nil
}

// stop kills the worker and waits until it has exited.
func (w *worker) stop() {
	_ = w.cmd.Process.Kill()
	<-w.exited
}

func (w *worker) hasExited() bool {
	select {
	case <-w.exited:
		return true
	default:
		return false
	}
}

func (w *worker) exitStatus() string {
	<-w.exited
	return w.cmd.ProcessState.String()
}

// replace stops a worker and starts another in its place, once per worker.
// If the new one can't start, it tries again every second until the pool
// closes.
func (p *Pool) replace(w *worker) {
	w.replaced.Do(func() { p.startReplacement(w) })
}

func (p *Pool) startReplacement(w *worker) {
	w.stop()
	p.mu.Lock()
	delete(p.workers, w)
	p.mu.Unlock()
	for {
		next, err := p.start()
		if err == nil {
			p.idle <- next
			return
		}
		if errors.Is(err, errPoolClosed) {
			return
		}
		p.logger.Error("start a processing worker", "error", err.Error())
		time.Sleep(time.Second)
	}
}

// take waits for an idle worker that is still running.
func (p *Pool) take(ctx context.Context) (*worker, error) {
	for {
		select {
		case w := <-p.idle:
			p.mu.Lock()
			alive := !w.hasExited()
			w.busy = alive
			p.mu.Unlock()
			if alive {
				return w, nil
			}
			// Its replacement is already on the way.
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// read sends one original of size bytes to a worker and returns its answer.
// When the limit passes or ctx ends first, the worker is killed, so a message
// it is stuck on can't hold it.
func (p *Pool) read(ctx context.Context, original io.Reader, size int64, withText bool) (readAnswer, error) {
	w, err := p.take(ctx)
	if err != nil {
		return readAnswer{}, err
	}
	type result struct {
		answer readAnswer
		err    error
	}
	done := make(chan result, 1)
	go func() {
		var r result
		r.err = w.exchange(workerRequest{Op: "read", Size: size, Text: withText}, original, &r.answer)
		done <- r
	}()
	limit := time.NewTimer(p.limit)
	defer limit.Stop()
	select {
	case r := <-done:
		if r.err != nil {
			// The worker may be waiting for the rest of the original, so it
			// is replaced either way.
			if source, ok := errors.AsType[*sourceError](r.err); ok {
				go p.replace(w)
				return readAnswer{}, source
			}
			stopped := &WorkerStoppedError{Exit: w.exitStatusAfterFailure()}
			go p.replace(w)
			return readAnswer{}, stopped
		}
		p.mu.Lock()
		w.busy = false
		p.mu.Unlock()
		p.idle <- w
		return r.answer, nil
	case <-limit.C:
		w.stop()
		<-done
		go p.replace(w)
		return readAnswer{}, &WorkerStoppedError{Exit: w.exitStatus(), TookTooLong: true}
	case <-ctx.Done():
		w.stop()
		// The exchange ends once the worker is gone, and only then may the
		// caller close original.
		<-done
		go p.replace(w)
		return readAnswer{}, ctx.Err()
	}
}

// exitStatusAfterFailure kills a worker that broke the exchange but still
// runs, then reports how it ended.
func (w *worker) exitStatusAfterFailure() string {
	if !w.hasExited() {
		select {
		case <-w.exited:
		case <-time.After(time.Second):
			_ = w.cmd.Process.Kill()
		}
	}
	return w.exitStatus()
}

func (w *worker) exchange(request workerRequest, original io.Reader, answer *readAnswer) error {
	if err := WriteFrame(w.stdin, request); err != nil {
		return err
	}
	source := &sourceReader{reader: original}
	if _, err := io.CopyN(w.stdin, source, request.Size); err != nil {
		if source.err != nil {
			return &sourceError{err: source.err}
		}
		return fmt.Errorf("send the original: %w", err)
	}
	return ReadFrame(w.stdout, answer)
}

// sourceError means the original couldn't be read, so the worker isn't to
// blame.
type sourceError struct{ err error }

func (e *sourceError) Error() string { return "read the original: " + e.err.Error() }
func (e *sourceError) Unwrap() error { return e.err }

// sourceReader remembers its reader's error, which io.CopyN can't tell from
// the worker's.
type sourceReader struct {
	reader io.Reader
	err    error
}

func (r *sourceReader) Read(p []byte) (int, error) {
	n, err := r.reader.Read(p)
	if err != nil && !errors.Is(err, io.EOF) {
		r.err = err
	}
	return n, err
}

// pids lists the running workers' process IDs.
func (p *Pool) pids() []int {
	p.mu.Lock()
	defer p.mu.Unlock()
	var pids []int
	for w := range p.workers {
		if !w.hasExited() {
			pids = append(pids, w.cmd.Process.Pid)
		}
	}
	return pids
}
