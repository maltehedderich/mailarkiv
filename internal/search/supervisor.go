// Package search runs the mailarkiv-search program, keeps its index in step
// with processed messages, and answers searches through it. It never parses
// mail itself: documents come from processing and the catalog.
package search

import (
	"bufio"
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Config is what the module needs from the server.
type Config struct {
	// DataDir is the search process's working directory. Its indexes live
	// in DataDir/index.
	DataDir string
	// Program is the path of mailarkiv-search.
	Program   string
	DB        *sqlitedb.DB
	Archive   *archive.Archive
	Processor *processing.Processor
	Status    *opstatus.Status
	Logger    *slog.Logger
	Now       func() time.Time
	// FreeSpace, if set, replaces the reading of free space in a
	// directory, for tests.
	FreeSpace func(path string) (int64, error)
}

const (
	// helloWait is how long a new search process has to send its hello.
	helloWait = 10 * time.Second
	// stopWait is how long a stop waits after closing standard input before
	// it kills the process.
	stopWait = 5 * time.Second
	// A process that ran this long starts the restart wait again at 1 s.
	restartWaitReset = 10 * time.Minute
	maxRestartWait   = 60 * time.Second
)

// The hello this server's search program sends. Both are built from the same
// repository, so anything else is the wrong program.
var wantHello = hello{Type: "hello", Protocol: 1, Tantivy: "0.27.0", SchemaVersion: schemaVersion}

type hello struct {
	Type          string `json:"type"`
	Protocol      int    `json:"protocol"`
	Tantivy       string `json:"tantivy"`
	SchemaVersion int    `json:"schema_version"`
}

// panicLine is the only line the search process writes on standard error.
// The server logs its location and only counts any other line, which could
// quote mail.
var panicLine = regexp.MustCompile(`^mailarkiv-search panic at ([A-Za-z0-9_/.-]+:[0-9]+)$`)

// Module supervises the search process and its index. Its methods are safe
// for concurrent use.
type Module struct {
	config Config

	// These change only in tests.
	command        func() *exec.Cmd
	batchMessages  int
	batchBytes     int
	commitMessages int
	retryAfter     func(time.Duration) <-chan time.Time
	restartAfter   func(time.Duration) <-chan time.Time
	batchSent      func(sentBatch)
	maintained     func(generationID int64, answer maintainAnswer)
	freeSpace      func(path string) (int64, error)
	diskCheckEvery time.Duration
	maintainEvery  int
	maintainAfter  time.Duration

	// faultsGiven is set once a process got the search fault list.
	faultsGiven atomic.Bool
	// wake asks the indexing loop to look for a rebuild that started.
	wake chan struct{}
	// startMu lets one rebuild start at a time, and markMu one mark.
	startMu sync.Mutex
	markMu  sync.Mutex

	mu      sync.Mutex
	current *process
	session *session
	// lastFailure is the category of the latest stop or failed start.
	lastFailure opstatus.Category
	nextStartAt *time.Time
	restarts    int
	// saved is set once the mark and the running rebuild were read.
	saved        bool
	needsRebuild *needsRebuild
	rebuild      *runningRebuild
	// crashes holds the times of stops nobody asked for, in the last 30
	// minutes.
	crashes             []time.Time
	maintenanceFailures int
	rebuildFailures     int
	paused              bool
	freeBytes           int64
	// diskRead is set once freeBytes holds a reading.
	diskRead bool
	// progress is what the indexing loop last saw, or empty before it ran.
	progress IndexingState
	state    State
}

// New returns a module that hasn't started the search process yet.
func New(config Config) *Module {
	m := &Module{
		config:         config,
		batchMessages:  maxBatchMessages,
		batchBytes:     maxBatchBytes,
		commitMessages: commitEvery,
		retryAfter:     time.After,
		restartAfter:   time.After,
		freeSpace:      statfsFree,
		diskCheckEvery: diskCheckEvery,
		maintainEvery:  maintainEveryCommits,
		maintainAfter:  maintainAfterTime,
		wake:           make(chan struct{}, 1),
		lastFailure:    opstatus.SearchProcessDidNotStart,
		state:          State{Search: SearchUnavailable, Indexing: IndexingStopped},
	}
	if config.FreeSpace != nil {
		m.freeSpace = config.FreeSpace
	}
	m.command = m.programCommand
	return m
}

// faultsVariable holds the fault list that the search program's test build
// reads. Release builds ignore it.
const faultsVariable = "MAILARKIV_SEARCH_FAULTS"

// programCommand starts the program with an empty environment. When the
// server's environment sets a fault list, only the first process gets it,
// so a fault that ends the process fires once and the next one recovers.
func (m *Module) programCommand() *exec.Cmd {
	cmd := exec.Command(m.config.Program)
	cmd.Env = []string{}
	if faults, set := os.LookupEnv(faultsVariable); set && m.faultsGiven.CompareAndSwap(false, true) {
		cmd.Env = append(cmd.Env, faultsVariable+"="+faults)
	}
	return cmd
}

// processCommand is the command for one search process: no arguments, the
// data directory as working directory, and killed when the server dies.
func (m *Module) processCommand() *exec.Cmd {
	cmd := m.command()
	cmd.Dir = m.config.DataDir
	cmd.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
	return cmd
}

// Run starts the search process and starts it again whenever it stops, until
// ctx ends. Then it stops indexing, closes the process's standard input,
// and returns once the process has exited, killing it after 5 s.
func (m *Module) Run(ctx context.Context) {
	var wait restartWait
	for {
		started := time.Now()
		m.setNextStart(nil)
		p, err := m.start(ctx)
		if err == nil {
			m.supervise(ctx, p)
		} else if failed, ok := errors.AsType[*startError](err); ok {
			m.config.Status.RecordFailure(opstatus.FailureEvent{
				Category: opstatus.SearchProcessDidNotStart,
				Detail:   &opstatus.Detail{Cause: failed.cause},
			})
			m.setFailure(opstatus.SearchProcessDidNotStart)
		}
		if ctx.Err() != nil {
			return
		}
		delay := wait.next(time.Since(started))
		next := m.config.Now().Add(delay)
		m.setNextStart(&next)
		select {
		case <-ctx.Done():
			return
		case <-m.restartAfter(delay):
		}
	}
}

// restartWait is the wait before the next start: 1 s, doubling to 60 s, and
// 1 s again after a process ran for 10 minutes.
type restartWait struct{ wait backoff }

func (w *restartWait) next(ranFor time.Duration) time.Duration {
	if ranFor >= restartWaitReset {
		w.wait.reset()
	}
	return w.wait.next()
}

// process is one running search process.
type process struct {
	cmd    *exec.Cmd
	client *client
	// exited closes once the process has exited and its standard error has
	// been read to the end. ignoredLines is set then.
	exited       chan struct{}
	ignoredLines int
}

// startError means the search process didn't start, for cause.
type startError struct{ cause opstatus.Cause }

func (e *startError) Error() string { return "the search process didn't start: " + e.cause.String() }

// start starts the search process and reads its hello. It must run on a
// goroutine that doesn't lock its OS thread, because Pdeathsig follows the
// thread that forked.
func (m *Module) start(ctx context.Context) (*process, error) {
	cmd := m.processCommand()
	var ends []*os.File
	pipe := func() (*os.File, *os.File) {
		r, w, err := os.Pipe()
		if err != nil {
			return nil, nil
		}
		ends = append(ends, r, w)
		return r, w
	}
	childIn, stdin := pipe()
	stdout, childOut := pipe()
	stderr, childErr := pipe()
	if len(ends) != 6 {
		closeAll(ends)
		return nil, &startError{cause: opstatus.CauseNotExecutable}
	}
	cmd.Stdin, cmd.Stdout, cmd.Stderr = childIn, childOut, childErr
	err := cmd.Start()
	closeAll([]*os.File{childIn, childOut, childErr})
	if err != nil {
		closeAll([]*os.File{stdin, stdout, stderr})
		if errors.Is(err, fs.ErrNotExist) || errors.Is(err, exec.ErrNotFound) {
			return nil, &startError{cause: opstatus.CauseMissing}
		}
		return nil, &startError{cause: opstatus.CauseNotExecutable}
	}

	p := &process{cmd: cmd, exited: make(chan struct{})}
	errorsRead := make(chan struct{})
	go func() {
		defer close(errorsRead)
		p.ignoredLines = m.readErrors(stderr)
	}()
	go func() {
		_ = cmd.Wait()
		<-errorsRead
		stderr.Close()
		close(p.exited)
	}()

	output := bufio.NewReader(stdout)
	greeted := make(chan error, 1)
	go func() {
		var got hello
		err := processing.ReadFrame(output, &got)
		if err == nil && got != wantHello {
			err = errors.New("a wrong hello")
		}
		greeted <- err
	}()
	fail := func(err error) (*process, error) {
		_ = cmd.Process.Kill()
		<-p.exited
		stdin.Close()
		stdout.Close()
		return nil, err
	}
	select {
	case err := <-greeted:
		if err != nil {
			return fail(&startError{cause: opstatus.CauseBadHello})
		}
	case <-time.After(helloWait):
		return fail(&startError{cause: opstatus.CauseBadHello})
	case <-ctx.Done():
		return fail(ctx.Err())
	}
	p.client = newClient(stdin, output, m.config.Status, m.config.Logger)
	go func() {
		<-p.exited
		stdout.Close()
	}()
	return p, nil
}

func closeAll(files []*os.File) {
	for _, file := range files {
		if file != nil {
			file.Close()
		}
	}
}

// readErrors reads the search process's standard error to its end. It logs
// panic lines and returns how many other lines it read.
func (m *Module) readErrors(stderr io.Reader) int {
	ignored := 0
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		if match := panicLine.FindSubmatch(scanner.Bytes()); match != nil {
			m.config.Logger.Error("search process panic", "location", string(match[1]))
			continue
		}
		ignored++
	}
	if scanner.Err() != nil {
		// A line too long to scan still counts, and the rest is dropped.
		ignored++
		_, _ = io.Copy(io.Discard, stderr)
	}
	return ignored
}

// supervise runs indexing while p runs. It returns when p has exited, or,
// once ctx ends, after stopping p.
func (m *Module) supervise(ctx context.Context, p *process) {
	m.loadSavedOnce(ctx)
	s := newSession(p.client)
	m.mu.Lock()
	m.current = p
	m.session = s
	restarts := m.restarts
	m.restarts++
	m.progress = ""
	m.refreshLocked()
	m.mu.Unlock()
	m.config.Logger.Info("search process started", "restarts", restarts)
	m.config.Status.RecordSuccess(opstatus.SearchProcessDidNotStart)

	running, endSession := context.WithCancel(ctx)
	indexing := make(chan struct{})
	go func() {
		defer close(indexing)
		m.index(running, s)
	}()

	requested := false
	select {
	case <-p.exited:
	case <-p.client.done:
		// Its output broke the frame rules, so nothing more can be read.
		_ = p.cmd.Process.Kill()
		<-p.exited
	case <-ctx.Done():
		requested = true
		endSession()
		<-indexing
		p.client.close()
		select {
		case <-p.exited:
		case <-time.After(stopWait):
			_ = p.cmd.Process.Kill()
			<-p.exited
		}
	}
	endSession()
	<-indexing

	m.mu.Lock()
	m.current = nil
	m.session = nil
	m.refreshLocked()
	m.mu.Unlock()
	if p.ignoredLines > 0 {
		m.config.Logger.Warn("search process output ignored", "lines", p.ignoredLines)
	}
	if !requested {
		m.config.Status.RecordFailure(opstatus.FailureEvent{
			Category: opstatus.SearchProcessStopped,
			Detail:   exitDetail(p.cmd.ProcessState),
		})
		m.setFailure(opstatus.SearchProcessStopped)
		m.recordCrash(ctx)
	}
}

// loadSavedOnce reads the saved mark and rebuild before the first indexing.
// A failed read is logged and tried again at the next start.
func (m *Module) loadSavedOnce(ctx context.Context) {
	m.mu.Lock()
	saved := m.saved
	m.mu.Unlock()
	if saved {
		return
	}
	if err := m.loadSaved(ctx); err != nil {
		m.config.Logger.Error("load the search state", "error", err.Error())
		return
	}
	m.mu.Lock()
	m.saved = true
	m.mu.Unlock()
}

// exitDetail names how a process ended: its exit status, or the signal that
// ended it.
func exitDetail(state *os.ProcessState) *opstatus.Detail {
	status, ok := state.Sys().(syscall.WaitStatus)
	if ok && status.Signaled() {
		signal := int(status.Signal())
		return &opstatus.Detail{Signal: &signal}
	}
	code := state.ExitCode()
	return &opstatus.Detail{ExitStatus: &code}
}

// PrepareFailed records that Prepare returned err. Searches then answer
// Unavailable with Index write failed, and the server doesn't call Run.
func (m *Module) PrepareFailed(err error) {
	cause := opstatus.CauseOther
	if errors.Is(err, fs.ErrPermission) {
		cause = opstatus.CausePermissionDenied
	}
	m.config.Logger.Error("prepare the index directory", "error", err.Error())
	m.config.Status.RecordFailure(opstatus.FailureEvent{Category: opstatus.IndexWriteFailed, Detail: &opstatus.Detail{Cause: cause}})
	m.setFailure(opstatus.IndexWriteFailed)
}

func (m *Module) setFailure(category opstatus.Category) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastFailure = category
}

// unavailableLocked is the answer to a search while no process runs. m.mu
// must be held.
func (m *Module) unavailableLocked() *UnavailableError {
	unavailable := &UnavailableError{Cause: m.lastFailure}
	if m.nextStartAt != nil {
		next := *m.nextStartAt
		unavailable.NextStartAt = &next
	}
	return unavailable
}

func (m *Module) setNextStart(at *time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.nextStartAt = at
}

// pid is the running search process's ID, or 0.
func (m *Module) pid() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.current == nil {
		return 0
	}
	return m.current.cmd.Process.Pid
}
