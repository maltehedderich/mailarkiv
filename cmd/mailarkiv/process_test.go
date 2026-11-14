package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
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

// serverBinary is the real binary, built once by TestMain. faultsBinary is the
// same program built with -tags faults, which obeys MAILARKIV_FAULTS.
var (
	serverBinary string
	faultsBinary string
)

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
	faultsBinary = filepath.Join(dir, "mailarkiv-faults")
	for _, args := range [][]string{
		{"build", "-o", serverBinary, "."},
		{"build", "-tags", "faults", "-o", faultsBinary, "."},
	} {
		build := exec.Command("go", args...)
		// The same settings as `mise run server:build`, so tests run what ships.
		build.Env = append(os.Environ(), "CGO_ENABLED=0")
		if output, err := build.CombinedOutput(); err != nil {
			fmt.Fprintf(os.Stderr, "go %v: %v\n%s", args, err, output)
			return 1
		}
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

// faultsCommand runs the faults binary on a free port with the given
// MAILARKIV_FAULTS setting.
func faultsCommand(dataDir, faults string) *exec.Cmd {
	cmd := exec.Command(faultsBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "MAILARKIV_FAULTS="+faults)
	return cmd
}

// startFaultServer runs the faults binary and returns once it listens.
func startFaultServer(t *testing.T, dataDir, faults string) *serverProcess {
	t.Helper()
	return startProcess(t, faultsCommand(dataDir, faults))
}

// waitForKill waits for a fault to stop the process and fails the test unless
// SIGKILL is what ended it.
func (p *serverProcess) waitForKill() {
	p.t.Helper()
	select {
	case <-p.scanned:
	case <-time.After(processTimeout):
		p.t.Fatalf("the server didn't die within %s:\n%s", processTimeout, p.stderr())
	}
	p.wait()
	assertKilled(p.t, p.cmd.ProcessState, p.stderr())
}

// runFaultsUntilKilled runs the faults binary for a fault that fires before the
// server listens, and returns once SIGKILL has ended it.
func runFaultsUntilKilled(t *testing.T, dataDir, faults string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, faultsBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "MAILARKIV_FAULTS="+faults)
	var output bytes.Buffer
	cmd.Stderr = &output

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("the server didn't die within %s:\n%s", processTimeout, output.String())
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) {
		t.Fatalf("run the faults binary: %v\n%s", err, output.String())
	}
	assertKilled(t, cmd.ProcessState, output.String())
}

func assertKilled(t *testing.T, state *os.ProcessState, stderr string) {
	t.Helper()
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
		t.Fatalf("the process ended with %s, want SIGKILL:\n%s", state, stderr)
	}
}

const adminAddress = "admin@example.test"

// createAdmin creates the account these tests sign in with, through the CLI.
func createAdmin(t *testing.T, dataDir string) {
	t.Helper()
	if code, _, stderr := runAccount(t, accountPassword+"\n", "account", "create", "--data-dir", dataDir, adminAddress); code != 0 {
		t.Fatalf("account create = %d: %s", code, stderr)
	}
}

// contentHash is the hash the contract names a message by: lowercase hex SHA-256.
func contentHash(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// blobPath locates an original using only its content hash.
func blobPath(dataDir, hash string) string {
	return filepath.Join(dataDir, "blobs", hash[0:2], hash[2:4], hash)
}

// seedBlob writes a stored original directly, the way one is left by a crash
// after the rename, and returns its hash.
func seedBlob(t *testing.T, dataDir string, content []byte) string {
	t.Helper()
	hash := contentHash(content)
	path := blobPath(dataDir, hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatal(err)
	}
	return hash
}

// seedTempFile leaves a file in tmp/, the way a crash during an upload does.
func seedTempFile(t *testing.T, dataDir string) {
	t.Helper()
	tmp := filepath.Join(dataDir, "tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "up-leftover"), []byte("Subject: part of a"), 0o600); err != nil {
		t.Fatal(err)
	}
}

// tempFiles names the entries in tmp/.
func tempFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func assertNoTempFiles(t *testing.T, dataDir string) {
	t.Helper()
	if names := tempFiles(t, dataDir); len(names) != 0 {
		t.Errorf("tmp/ holds %v, want nothing", names)
	}
}

// waitForTempFile returns the name of the file in tmp/ once one appears.
func waitForTempFile(t *testing.T, dataDir string) string {
	t.Helper()
	deadline := time.Now().Add(processTimeout)
	for time.Now().Before(deadline) {
		if names := tempFiles(t, dataDir); len(names) == 1 {
			return names[0]
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no temp file appeared within %s", processTimeout)
	return ""
}

// blobFiles lists every file under blobs/, relative to it, in a fixed order.
func blobFiles(t *testing.T, dataDir string) []string {
	t.Helper()
	blobs := filepath.Join(dataDir, "blobs")
	found := []string{}
	err := filepath.WalkDir(blobs, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			relative, err := filepath.Rel(blobs, path)
			if err != nil {
				return err
			}
			found = append(found, relative)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// assertOneBlob checks that blobs/ holds this one file with exactly these bytes.
func assertOneBlob(t *testing.T, dataDir string, content []byte) {
	t.Helper()
	if blobs := blobFiles(t, dataDir); len(blobs) != 1 {
		t.Errorf("blobs/ holds %v, want one file", blobs)
	}
	stored, err := os.ReadFile(blobPath(dataDir, contentHash(content)))
	if err != nil {
		t.Fatalf("the stored original: %v", err)
	}
	if !bytes.Equal(stored, content) {
		t.Errorf("the stored original differs from the imported bytes (%d bytes, want %d)", len(stored), len(content))
	}
}

// importAttempt is what a client saw for one import. A non-nil err means it got
// no complete answer.
type importAttempt struct {
	status int
	body   string
	err    error
}

// tryImport imports content and returns whatever happens, for the cases where
// the server is expected to die before it answers.
func tryImport(baseURL, token string, content []byte) importAttempt {
	return sendImport(baseURL, token, bytes.NewReader(content))
}

func sendImport(baseURL, token string, body io.Reader) importAttempt {
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/imports", body)
	if err != nil {
		return importAttempt{err: err}
	}
	// Sent by hand: net/http/cookiejar withholds Secure cookies over plain HTTP.
	request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	response, err := processClient.Do(request)
	if err != nil {
		return importAttempt{err: err}
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	return importAttempt{status: response.StatusCode, body: string(read), err: err}
}

// stalledUpload is a chunked import that has stopped sending, so the server is
// in the middle of reading it.
type stalledUpload struct {
	t      *testing.T
	writer *io.PipeWriter
	result chan importAttempt
}

// startStalledUpload begins a chunked import and sends first. It must be longer
// than the 4 KiB file check, or the server creates no temp file.
func startStalledUpload(t *testing.T, baseURL, token string, first []byte) *stalledUpload {
	t.Helper()
	reader, writer := io.Pipe()
	upload := &stalledUpload{t: t, writer: writer, result: make(chan importAttempt, 1)}
	t.Cleanup(func() { writer.CloseWithError(errors.New("the test ended")) })
	go func() { upload.result <- sendImport(baseURL, token, reader) }()
	upload.send(first)
	return upload
}

func (u *stalledUpload) send(p []byte) {
	u.t.Helper()
	if _, err := u.writer.Write(p); err != nil {
		u.t.Fatalf("send the upload's bytes: %v", err)
	}
}

// finish sends the end of the body and returns what the client saw.
func (u *stalledUpload) finish() importAttempt {
	u.t.Helper()
	if err := u.writer.Close(); err != nil {
		u.t.Fatal(err)
	}
	return u.wait()
}

// abandon stops sending and returns what the client saw. The transport reports a
// lost connection only once its request body has ended, so this ends it first.
func (u *stalledUpload) abandon() importAttempt {
	u.t.Helper()
	u.writer.CloseWithError(errors.New("the test stopped sending"))
	return u.wait()
}

func (u *stalledUpload) wait() importAttempt {
	u.t.Helper()
	select {
	case attempt := <-u.result:
		return attempt
	case <-time.After(processTimeout):
		u.t.Fatalf("the upload got no result within %s", processTimeout)
		return importAttempt{}
	}
}

// listedMessage is a message as the API contract writes it.
type listedMessage struct {
	ID               int64  `json:"id"`
	ContentHash      string `json:"content_hash"`
	Size             int64  `json:"size"`
	ObservationCount int    `json:"observation_count"`
}

// importAnswer is the body of an import's answer in the API contract.
type importAnswer struct {
	Outcome string         `json:"outcome"`
	Reason  string         `json:"reason"`
	Message *listedMessage `json:"message"`
}

func parseImportAnswer(t *testing.T, body string) importAnswer {
	t.Helper()
	var answer importAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("the import's answer isn't JSON: %q: %v", body, err)
	}
	return answer
}

// messages reads the first page of the list.
func (p *serverProcess) messages(token string) (items []listedMessage, total int) {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/messages", "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/messages = %d %s, want 200", response.StatusCode, body)
	}
	var page struct {
		Items []listedMessage `json:"items"`
		Total int             `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		p.t.Fatalf("the list isn't JSON: %q: %v", body, err)
	}
	return page.Items, page.Total
}

func (p *serverProcess) assertNothingListed(token string) {
	p.t.Helper()
	if items, total := p.messages(token); total != 0 || len(items) != 0 {
		p.t.Errorf("the list holds %+v with total %d, want nothing", items, total)
	}
}

// assertListedOnce checks that the list holds only this content, with this many
// imports in its history.
func (p *serverProcess) assertListedOnce(token string, content []byte, importCount int) listedMessage {
	p.t.Helper()
	items, total := p.messages(token)
	if total != 1 || len(items) != 1 {
		p.t.Fatalf("the list holds %+v with total %d, want one message", items, total)
	}
	message := items[0]
	if message.ContentHash != contentHash(content) || message.Size != int64(len(content)) || message.ObservationCount != importCount {
		p.t.Errorf("listed %+v, want hash %s, size %d, and %d imports", message, contentHash(content), len(content), importCount)
	}
	return message
}

// unreferencedEntry and the types below are GET /api/status in the API contract.
type unreferencedEntry struct {
	ContentHash string `json:"content_hash"`
	Size        int64  `json:"size"`
}

type failureEntry struct {
	At          string  `json:"at"`
	Category    string  `json:"category"`
	MessageID   *int64  `json:"message_id"`
	ContentHash *string `json:"content_hash"`
	Restored    bool    `json:"restored"`
}

type recoveryEntry struct {
	FinishedAt          string `json:"finished_at"`
	TempFilesRemoved    int    `json:"temp_files_removed"`
	TempFilesNotRemoved int    `json:"temp_files_not_removed"`
	UnreferencedFound   int    `json:"unreferenced_found"`
}

type statusAnswer struct {
	StartedAt    string              `json:"started_at"`
	Recovery     recoveryEntry       `json:"recovery"`
	Unreferenced []unreferencedEntry `json:"unreferenced"`
	Failures     []failureEntry      `json:"failures"`
}

// status reads GET /api/status.
func (p *serverProcess) status(token string) statusAnswer {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/status", "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/status = %d %s, want 200", response.StatusCode, body)
	}
	var answer statusAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		p.t.Fatalf("the status isn't JSON: %q: %v", body, err)
	}
	return answer
}

// problemCount reads GET /api/session. The server runs on the real clock, so
// the time left is only checked to lie within a fresh session's 12 hours.
func (p *serverProcess) problemCount(token string) int {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/session", "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/session = %d %s, want 200", response.StatusCode, body)
	}
	var answer struct {
		ProblemCount *int `json:"problem_count"`
		ExpiresIn    *int `json:"expires_in"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&answer); err != nil || answer.ProblemCount == nil || answer.ExpiresIn == nil {
		p.t.Fatalf("GET /api/session = %s, want problem_count and expires_in alone: %v", body, err)
	}
	if *answer.ExpiresIn <= 0 || *answer.ExpiresIn > 12*60*60 {
		p.t.Errorf("expires_in = %d, want 1 to 43200 seconds", *answer.ExpiresIn)
	}
	return *answer.ProblemCount
}

// assertLogged stops the server, which guarantees all of its stderr has been
// read, and checks that a log line has this category and the time.
func (p *serverProcess) assertLogged(category string) {
	p.t.Helper()
	if code := p.stop(syscall.SIGTERM); code != 0 {
		p.t.Fatalf("exit = %d, want 0:\n%s", code, p.stderr())
	}
	for _, entry := range logEntries(p.t, p.stderr()) {
		if entry["category"] == category && entry["time"] != nil {
			return
		}
	}
	p.t.Errorf("no log line has category %s:\n%s", category, p.stderr())
}
