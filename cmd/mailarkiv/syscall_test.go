package main

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"testing"
)

// traceCall is one traced syscall: its name, the path it acts on, and for a
// write, the start of the data.
type traceCall struct {
	line int
	name string
	path string
	data string
}

var (
	// "1234 fsync(7</data/tmp/up-1>) = 0", or the same line ending in
	// "<unfinished ...>" when another thread's call interrupts it. The start
	// line is enough, because one goroutine makes every call in capture order.
	fdCall = regexp.MustCompile(`^\d+\s+(fsync|fdatasync|write)\(\d+<([^>]*)>(?:, "((?:[^"\\]|\\.)*)")?`)
	// "1234 renameat(AT_FDCWD</cwd>, "/old", AT_FDCWD</cwd>, "/new") = 0"
	renameCall = regexp.MustCompile(`^\d+\s+(rename|renameat|renameat2)\(`)
	quoted     = regexp.MustCompile(`"((?:[^"\\]|\\.)*)"`)
)

func parseTrace(trace string) []traceCall {
	var calls []traceCall
	for i, line := range strings.Split(trace, "\n") {
		if match := fdCall.FindStringSubmatch(line); match != nil {
			calls = append(calls, traceCall{line: i + 1, name: match[1], path: match[2], data: match[3]})
			continue
		}
		if match := renameCall.FindStringSubmatch(line); match != nil {
			paths := quoted.FindAllStringSubmatch(line, -1)
			if len(paths) >= 2 {
				calls = append(calls, traceCall{line: i + 1, name: match[1], path: paths[1][1]})
			}
		}
	}
	return calls
}

func isSync(c traceCall) bool   { return c.name == "fsync" || c.name == "fdatasync" }
func isRename(c traceCall) bool { return strings.HasPrefix(c.name, "rename") }

// traceStep is one call the trace must hold, in order.
type traceStep struct {
	name string
	is   func(traceCall) bool
	// once is set for calls that only this import makes. The first one in the
	// whole trace must be the one in order, so a stray early call fails.
	once bool
}

// traceImport runs the server under strace and imports content. seed, if any,
// changes the data directory before the server starts. It returns the data
// directory and the calls the import traced, with the server stopped.
func traceImport(t *testing.T, content []byte, seed func(dataDir string)) (string, []traceCall) {
	t.Helper()
	stracePath, err := exec.LookPath("strace")
	if err != nil {
		t.Fatalf("this test needs strace: %v", err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(root, "data")
	traceFile := filepath.Join(root, "trace")
	createAdmin(t, dataDir)
	if seed != nil {
		seed(dataDir)
	}

	// -s 256 prints the rename's whole blob path and the response's status line.
	cmd := exec.Command(stracePath, "-f", "-y", "-s", "256",
		"-e", "trace=fsync,fdatasync,rename,renameat,renameat2,write",
		"-o", traceFile,
		serverBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	server := startProcess(t, cmd)

	cookie := signInOverHTTP(t, server.baseURL, adminAddress)
	response, body := postImport(t, server.baseURL, cookie, content)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}

	read, err := os.ReadFile(traceFile)
	if err != nil {
		t.Fatal(err)
	}
	return dataDir, parseTrace(string(read))
}

// assertTraceOrder checks that the calls hold every step, in the steps' order.
func assertTraceOrder(t *testing.T, calls []traceCall, steps []traceStep) {
	t.Helper()
	after := -1
	for _, step := range steps {
		found := -1
		for i, c := range calls {
			if !step.is(c) {
				continue
			}
			if i <= after {
				if step.once {
					t.Fatalf("%s at trace line %d comes before the step it must follow, at trace line %d", step.name, c.line, calls[after].line)
				}
				continue
			}
			found = i
			break
		}
		if found < 0 {
			t.Fatalf("no %s after trace line %d; traced calls:\n%s", step.name, lineOf(calls, after), describe(calls))
		}
		after = found
	}
}

func syncOf(path string) func(traceCall) bool {
	return func(c traceCall) bool { return isSync(c) && c.path == path }
}

// tempFileSync is the fsync of the upload's temp file.
func tempFileSync(dataDir string) traceStep {
	return traceStep{"fsync of tmp/up-…", func(c traceCall) bool {
		return isSync(c) && strings.HasPrefix(c.path, filepath.Join(dataDir, "tmp", "up-"))
	}, true}
}

// durableAfterBlob is what every path shares once the blob is in place: its
// directories, then the catalog, then the answer.
func durableAfterBlob(dataDir, hash string) []traceStep {
	leaf := filepath.Dir(blobPath(dataDir, hash))
	return []traceStep{
		{"fsync of blobs/ab/cd", syncOf(leaf), true},
		{"fsync of blobs/ab", syncOf(filepath.Dir(leaf)), true},
		{"fsync of blobs", syncOf(filepath.Join(dataDir, "blobs")), true},
		{"fsync of the data dir", syncOf(dataDir), false},
		// Sign-in committed a session earlier, so this one isn't the first.
		{"fsync of catalog.db-wal", syncOf(filepath.Join(dataDir, "catalog.db-wal")), false},
		{"write of HTTP/1.1 201 on the socket", func(c traceCall) bool {
			return c.name == "write" && (strings.HasPrefix(c.path, "socket:") || strings.HasPrefix(c.path, "TCP")) &&
				strings.HasPrefix(c.data, "HTTP/1.1 201 ")
		}, true},
	}
}

// ARCHITECTURE.md rule 3, FR-012: the 201 leaves only after the bytes, their
// directory entries, and the catalog commit are on disk.
func TestCaptureSyscallOrder(t *testing.T) {
	t.Run("new bytes", func(t *testing.T) {
		content := []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Traced\r\n\r\nBody.\r\n")
		hash := contentHash(content)

		dataDir, calls := traceImport(t, content, nil)

		blob := blobPath(dataDir, hash)
		steps := []traceStep{
			tempFileSync(dataDir),
			{"rename to blobs/ab/cd/<hash>", func(c traceCall) bool { return isRename(c) && c.path == blob }, true},
		}
		assertTraceOrder(t, calls, append(steps, durableAfterBlob(dataDir, hash)...))
	})

	// A crash can leave a blob visible but not yet durable. Before the catalog
	// entry that depends on it commits, capture must make it durable.
	t.Run("reused blob", func(t *testing.T) {
		content := []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Reused\r\n\r\nBody.\r\n")
		hash := contentHash(content)

		dataDir, calls := traceImport(t, content, func(dataDir string) { seedBlob(t, dataDir, content) })

		blob := blobPath(dataDir, hash)
		steps := []traceStep{
			tempFileSync(dataDir),
			{"fsync of the stored blob", syncOf(blob), true},
		}
		assertTraceOrder(t, calls, append(steps, durableAfterBlob(dataDir, hash)...))
		for _, c := range calls {
			if isRename(c) && c.path == blob {
				t.Errorf("trace line %d renames over a blob that already matched", c.line)
			}
		}
	})
}

func lineOf(calls []traceCall, index int) int {
	if index < 0 {
		return 0
	}
	return calls[index].line
}

func describe(calls []traceCall) string {
	var out strings.Builder
	for _, c := range calls {
		if c.name == "write" && !strings.HasPrefix(c.data, "HTTP/") {
			continue
		}
		fmt.Fprintf(&out, "%5d %s %s %.20q\n", c.line, c.name, c.path, c.data)
	}
	return out.String()
}

var processClient = &http.Client{Timeout: processTimeout}

// signInOverHTTP signs in and returns the session cookie's token.
func signInOverHTTP(t *testing.T, baseURL, address string) string {
	t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, address, accountPassword)
	response, err := processClient.Post(baseURL+"/api/session", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("sign in = %d, want 204", response.StatusCode)
	}
	for _, cookie := range response.Cookies() {
		if cookie.Name == "__Host-mailarkiv_session" {
			return cookie.Value
		}
	}
	t.Fatal("sign-in set no session cookie")
	return ""
}

func postImport(t *testing.T, baseURL, token string, content []byte) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/imports", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	// Sent by hand: net/http/cookiejar withholds Secure cookies over plain HTTP.
	request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	request.Header.Set("MailArkiv-File-Name", "traced.eml")
	response, err := processClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(read)
}
