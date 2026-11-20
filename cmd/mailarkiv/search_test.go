package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// searchedRow is the part of a search result row these tests check.
type searchedRow struct {
	ID     int64 `json:"id"`
	Sender *struct {
		Name    *string `json:"name"`
		Address *string `json:"address"`
	} `json:"sender"`
	Subject *string `json:"subject"`
}

type searchAnswer struct {
	Items []searchedRow `json:"items"`
	Total int           `json:"total"`
}

// plainFixture reads one of the synthetic messages processing is tested with.
func plainFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "..", "internal", "mailparse", "testdata", "plain", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

// search sends a words-only search, the way the interface does.
func (p *serverProcess) search(token, words string) (*http.Response, string) {
	p.t.Helper()
	body := fmt.Sprintf(`{"words":%q,"from":"","to":"","subject":"","attachment":"",`+
		`"list_date_from":null,"list_date_before":null,"has_attachments":false,"page":1}`, words)
	return p.request(http.MethodPost, "/api/search", body, token)
}

// waitForSearch searches every 500 ms until the answer finds something, and
// fails the test after 60 s. Until the search process has started, searches
// answer 503.
func (p *serverProcess) waitForSearch(token, words string) searchAnswer {
	p.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		response, body := p.search(token, words)
		var answer searchAnswer
		if response.StatusCode == http.StatusOK {
			if err := json.Unmarshal([]byte(body), &answer); err != nil {
				p.t.Fatalf("the search answer isn't JSON: %q: %v", body, err)
			}
			if answer.Total > 0 {
				return answer
			}
		} else if response.StatusCode != http.StatusServiceUnavailable {
			p.t.Fatalf("search = %d %s, want 200 or, while the search process starts, 503", response.StatusCode, body)
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("search found nothing within 60 s; last answer %d %s", response.StatusCode, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

// waitForAllIndexed searches every 500 ms until words find count messages
// and none is left out, so every message is processed and the index's cursor
// is saved past them. It fails the test after 60 s.
func (p *serverProcess) waitForAllIndexed(token, words string, count int) {
	p.t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	for {
		response, body := p.search(token, words)
		var answer struct {
			Total       int `json:"total"`
			NotIncluded int `json:"not_included"`
		}
		if response.StatusCode == http.StatusOK && json.Unmarshal([]byte(body), &answer) == nil &&
			answer.Total == count && answer.NotIncluded == 0 {
			return
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("search didn't find all %d messages within 60 s; last answer %d %s", count, response.StatusCode, body)
		}
		time.Sleep(500 * time.Millisecond)
	}
}

func importFixture(t *testing.T, server *serverProcess, token string, content []byte) int64 {
	t.Helper()
	response, body := postImport(t, server.baseURL, token, content)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	var imported struct {
		Message struct {
			ID int64 `json:"id"`
		} `json:"message"`
	}
	if err := json.Unmarshal([]byte(body), &imported); err != nil {
		t.Fatalf("the import answer isn't JSON: %q: %v", body, err)
	}
	return imported.Message.ID
}

func assertFoundOnce(t *testing.T, answer searchAnswer, id int64) {
	t.Helper()
	if answer.Total != 1 || len(answer.Items) != 1 || answer.Items[0].ID != id {
		t.Fatalf("search found %+v with total %d, want message %d once", answer.Items, answer.Total, id)
	}
	row := answer.Items[0]
	if row.Sender == nil || row.Sender.Name == nil || *row.Sender.Name != "Bernd Weber" ||
		row.Sender.Address == nil || *row.Sender.Address != "bernd@example.test" ||
		row.Subject == nil || *row.Subject != "Bericht" {
		t.Errorf("row %+v, want Bernd Weber <bernd@example.test> with subject Bericht", row)
	}
}

// A message is found by a word from its body, with the sender and subject
// processing read, and again after a restart.
func TestSearchFirstMessage(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	// Its body says "Der <b>Quartals</b>bericht", so the word is split by
	// markup.
	id := importFixture(t, server, token, plainFixture(t, "html-only.eml"))

	assertFoundOnce(t, server.waitForSearch(token, "quartals"), id)
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}

	next := startServer(t, dataDir)
	assertFoundOnce(t, next.waitForSearch(token, "quartals"), id)
	if strings.Contains(strings.ToLower(next.stderr()+server.stderr()), "quartals") {
		t.Error("the log holds the search word")
	}
}

// Without mailarkiv-search beside it, the server still captures, lists, and
// downloads. Searches answer 503.
func TestServeWithoutSearchProgram(t *testing.T) {
	alone := filepath.Join(t.TempDir(), "mailarkiv")
	if err := copyFile(serverBinary, alone, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startProcess(t, exec.Command(alone, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"))
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	content := plainFixture(t, "html-only.eml")
	id := importFixture(t, server, token, content)
	server.assertListedOnce(token, content, 1)
	response, original := server.request(http.MethodGet, "/api/messages/"+strconv.FormatInt(id, 10)+"/original", "", token)
	if response.StatusCode != http.StatusOK || original != string(content) {
		t.Errorf("download = %d with %d bytes, want 200 with the %d imported bytes", response.StatusCode, len(original), len(content))
	}

	response, body := server.search(token, "quartals")
	var answer struct {
		Error string `json:"error"`
		Cause string `json:"cause"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil || response.StatusCode != http.StatusServiceUnavailable ||
		answer.Error != "search_unavailable" || answer.Cause != "search_process_did_not_start" {
		t.Errorf("search = %d %s, want 503 search_unavailable for search_process_did_not_start", response.StatusCode, body)
	}
}

// parse-worker is for the server alone. Started from a terminal, it refuses,
// and the usage text doesn't name it.
func TestParseWorkerRefusesATerminal(t *testing.T) {
	ptmx, err := os.OpenFile("/dev/ptmx", os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer ptmx.Close()
	if err := unix.IoctlSetPointerInt(int(ptmx.Fd()), unix.TIOCSPTLCK, 0); err != nil {
		t.Fatal(err)
	}
	number, err := unix.IoctlGetInt(int(ptmx.Fd()), unix.TIOCGPTN)
	if err != nil {
		t.Fatal(err)
	}
	terminal, err := os.OpenFile("/dev/pts/"+strconv.Itoa(number), os.O_RDWR|unix.O_NOCTTY, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer terminal.Close()

	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, serverBinary, "parse-worker")
	cmd.Stdin = terminal
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	err = cmd.Run()
	if ctx.Err() != nil {
		t.Fatal("parse-worker on a terminal didn't exit")
	}
	if exit, ok := errors.AsType[*exec.ExitError](err); !ok || exit.ExitCode() != exitBadArgs {
		t.Errorf("parse-worker on a terminal: %v, want exit %d", err, exitBadArgs)
	}
	if stderr.String() != usage {
		t.Errorf("stderr = %q, want the usage text", stderr.String())
	}
	if strings.Contains(usage, "parse-worker") {
		t.Error("the usage text names parse-worker")
	}
}

// childProcess is a process whose parent was the server.
type childProcess struct {
	pid  int
	args []string
}

// childProcesses lists pid's children from /proc.
func childProcesses(t *testing.T, pid int) []childProcess {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatal(err)
	}
	var children []childProcess
	for _, entry := range entries {
		child, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "stat"))
		if err != nil {
			continue // It has exited.
		}
		// The name in parentheses can hold spaces, so the fields start after
		// its last ')': state, then the parent's ID.
		fields := strings.Fields(string(stat[bytes.LastIndexByte(stat, ')')+1:]))
		if len(fields) < 2 || fields[1] != strconv.Itoa(pid) {
			continue
		}
		cmdline, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		children = append(children, childProcess{pid: child, args: strings.Split(strings.TrimRight(string(cmdline), "\x00"), "\x00")})
	}
	return children
}

// isAlive reports whether pid names a process, a zombie included.
func isAlive(pid int) bool {
	return !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH)
}
