package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// processTimeout bounds every wait on the real binary, so a hung server fails
// its test instead of holding the whole run.
const processTimeout = 30 * time.Second

// serverBinary is the real binary, built once by TestMain.
var serverBinary string

func TestMain(m *testing.M) {
	os.Exit(buildAndRun(m))
}

func buildAndRun(m *testing.M) int {
	// Children inherit this mask. A binary that stopped setting its own would
	// then create world-readable files, and the tests would see them.
	syscall.Umask(0o022)

	dir, err := os.MkdirTemp("", "mailarkiv-binary-")
	if err != nil {
		fmt.Fprintf(os.Stderr, "create the binary directory: %v\n", err)
		return 1
	}
	defer os.RemoveAll(dir)

	serverBinary = filepath.Join(dir, "mailarkiv")
	build := exec.Command("go", "build", "-o", serverBinary, ".")
	// The same settings as `mise run server:build`, so tests run what ships.
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if output, err := build.CombinedOutput(); err != nil {
		fmt.Fprintf(os.Stderr, "build the binary: %v\n%s", err, output)
		return 1
	}
	return m.Run()
}

// serverProcess is a running binary and the stderr it has written so far.
type serverProcess struct {
	t       *testing.T
	cmd     *exec.Cmd
	baseURL string

	mu      sync.Mutex
	lines   []string
	scanned chan struct{} // Closed when stderr ends.
	waited  bool

	// group sends signals to the whole process group. strace needs it: in its
	// -o FILE PROG mode it blocks fatal signals, so only the server gets them.
	group bool
}

// startServer runs the binary on a free port and returns once it listens.
// Port 0 lets the kernel choose, so no other process can take the port between
// finding it and using it. The server logs the address it got.
func startServer(t *testing.T, dataDir string) *serverProcess {
	t.Helper()
	return startProcess(t, exec.Command(serverBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"))
}

// startProcess starts cmd, which runs the server, and returns once it listens.
// A cmd that sets Setpgid gets its signals as a process group.
func startProcess(t *testing.T, cmd *exec.Cmd) *serverProcess {
	t.Helper()
	stderr, err := cmd.StderrPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	p := &serverProcess{t: t, cmd: cmd, scanned: make(chan struct{})}
	p.group = cmd.SysProcAttr != nil && cmd.SysProcAttr.Setpgid
	t.Cleanup(p.cleanUp)

	listening := make(chan string, 1)
	go p.scan(stderr, listening)

	select {
	case address := <-listening:
		p.baseURL = "http://" + address
	case <-p.scanned:
		t.Fatalf("the server exited before listening:\n%s", p.stderr())
	case <-time.After(processTimeout):
		t.Fatalf("the server didn't listen within %s:\n%s", processTimeout, p.stderr())
	}
	return p
}

func (p *serverProcess) scan(stderr io.Reader, listening chan<- string) {
	defer close(p.scanned)
	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		p.mu.Lock()
		p.lines = append(p.lines, line)
		p.mu.Unlock()

		var entry struct {
			Msg  string `json:"msg"`
			Addr string `json:"addr"`
		}
		if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == msgListening {
			listening <- entry.Addr
		}
	}
}

func (p *serverProcess) stderr() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return strings.Join(p.lines, "\n")
}

// stop sends the signal and returns the exit code.
func (p *serverProcess) stop(signal syscall.Signal) int {
	p.t.Helper()
	if err := p.signal(signal); err != nil {
		p.t.Fatal(err)
	}
	select {
	case <-p.scanned:
	case <-time.After(processTimeout):
		p.t.Fatalf("the server didn't exit within %s:\n%s", processTimeout, p.stderr())
	}
	return p.wait()
}

func (p *serverProcess) wait() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.waited {
		p.waited = true
		// A non-zero exit is an error here, and the exit code below reports it.
		_ = p.cmd.Wait()
	}
	return p.cmd.ProcessState.ExitCode()
}

func (p *serverProcess) cleanUp() {
	p.mu.Lock()
	waited := p.waited
	p.mu.Unlock()
	if waited {
		return
	}
	_ = p.signal(syscall.SIGKILL)
	<-p.scanned
	p.wait()
}

func (p *serverProcess) signal(signal syscall.Signal) error {
	if p.group {
		return syscall.Kill(-p.cmd.Process.Pid, signal)
	}
	return p.cmd.Process.Signal(signal)
}

// get answers with the response and its body. The client has a timeout, like
// every network call in this project.
func (p *serverProcess) get(path string) (*http.Response, string) {
	p.t.Helper()
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Get(p.baseURL + path)
	if err != nil {
		p.t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	return response, string(body)
}

// request sends one request with the session token as its cookie, sent by
// hand, because net/http/cookiejar withholds Secure cookies over plain HTTP.
// An empty token sends no cookie.
func (p *serverProcess) request(method, path, body, token string) (*http.Response, string) {
	p.t.Helper()
	request, err := http.NewRequest(method, p.baseURL+path, strings.NewReader(body))
	if err != nil {
		p.t.Fatal(err)
	}
	if token != "" {
		request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	}
	client := &http.Client{Timeout: 10 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		p.t.Fatal(err)
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		p.t.Fatal(err)
	}
	return response, string(read)
}

// runToExit runs the binary with args until it exits by itself, for the cases
// where it must refuse to start.
func runToExit(t *testing.T, args ...string) (exitCode int, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, serverBinary, args...)
	var output bytes.Buffer
	cmd.Stderr = &output

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("mailarkiv %v didn't exit within %s:\n%s", args, processTimeout, output.String())
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("mailarkiv %v: %v", args, err)
	}
	return cmd.ProcessState.ExitCode(), output.String()
}

// logEntries parses stderr as JSON lines, and any other line fails the test.
func logEntries(t *testing.T, stderr string) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for line := range strings.Lines(stderr) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("stderr line isn't JSON: %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}
