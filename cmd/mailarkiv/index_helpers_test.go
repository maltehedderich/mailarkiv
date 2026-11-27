package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// sharedWord is in the body of every indexMail message.
const sharedWord = "stichwort"

// indexMail is a small synthetic message holding sharedWord and its own word.
// No word is a prefix of another.
func indexMail(word string) []byte {
	return []byte("From: absender@example.test\r\nTo: admin@example.test\r\nSubject: Notiz " + word +
		"\r\nDate: Fri, 06 Nov 2026 10:00:00 +0100\r\n\r\nStichwort und " + word + ".\r\n")
}

// captureUnprocessed stores contents as imports by adminAddress through the
// archive, while no server runs, so processing hasn't read them. It returns
// their IDs.
func captureUnprocessed(t *testing.T, dataDir string, contents ...[]byte) []int64 {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	originals, err := archive.Open(dataDir, db, opstatus.New(logger, time.Now), logger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, content := range contents {
		result, err := originals.Import(context.Background(), bytes.NewReader(content), archive.ImportMeta{AccountEmail: adminAddress})
		stored, ok := result.(archive.Stored)
		if err != nil || !ok {
			t.Fatalf("capture gave %+v, %v; want stored", result, err)
		}
		ids = append(ids, stored.Message.ID)
	}
	return ids
}

// startWithoutSearch runs a copy of the default binary with no search
// program beside it. It captures and processes, and never indexes.
func startWithoutSearch(t *testing.T, dataDir string) *serverProcess {
	t.Helper()
	alone := filepath.Join(t.TempDir(), "mailarkiv")
	if err := copyFile(serverBinary, alone, 0o755); err != nil {
		t.Fatal(err)
	}
	return startProcess(t, exec.Command(alone, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"))
}

// processWithoutIndexing lets a server without its search program process
// every message, then stops it.
func processWithoutIndexing(t *testing.T, dataDir string) {
	t.Helper()
	server := startWithoutSearch(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	deadline := time.Now().Add(processTimeout)
	for {
		items, _ := server.messages(token)
		if !slices.ContainsFunc(items, func(m listedMessage) bool { return m.Processing == "not_processed" }) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("messages still not processed after %s: %+v", processTimeout, items)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}
}

// startFaultsWith runs the faults binary with Go and search process fault
// lists, either of which may be empty, and extra environment.
func startFaultsWith(t *testing.T, dataDir, faults, searchFaults string, args []string, env ...string) *serverProcess {
	t.Helper()
	cmd := exec.Command(faultsBinary, append([]string{"serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	if faults != "" {
		cmd.Env = append(cmd.Env, "MAILARKIV_FAULTS="+faults)
	}
	if searchFaults != "" {
		cmd.Env = append(cmd.Env, "MAILARKIV_SEARCH_FAULTS="+searchFaults)
	}
	return startProcess(t, cmd)
}

// catalogGeneration is a search_generation row.
type catalogGeneration struct {
	id        int64
	role      string
	cursor    int64
	rebuildID sql.NullInt64
}

// indexCatalog is what the catalog says about processing and the indexes.
type indexCatalog struct {
	processed   int
	highest     int64
	generations []catalogGeneration
	// outcomes holds each rebuild's outcome by ID. Invalid means running.
	outcomes map[int64]sql.NullString
}

func readIndexCatalog(t *testing.T, dataDir string) indexCatalog {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	c := indexCatalog{outcomes: map[int64]sql.NullString{}}
	if err := db.Reader.QueryRow(
		"SELECT count(processed_seq), coalesce(max(processed_seq), 0) FROM message").Scan(&c.processed, &c.highest); err != nil {
		t.Fatal(err)
	}
	rows, err := db.Reader.Query("SELECT id, role, cursor, rebuild_id FROM search_generation ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var g catalogGeneration
		if err := rows.Scan(&g.id, &g.role, &g.cursor, &g.rebuildID); err != nil {
			t.Fatal(err)
		}
		c.generations = append(c.generations, g)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rebuilds, err := db.Reader.Query("SELECT id, outcome FROM search_rebuild")
	if err != nil {
		t.Fatal(err)
	}
	defer rebuilds.Close()
	for rebuilds.Next() {
		var id int64
		var outcome sql.NullString
		if err := rebuilds.Scan(&id, &outcome); err != nil {
			t.Fatal(err)
		}
		c.outcomes[id] = outcome
	}
	if err := rebuilds.Err(); err != nil {
		t.Fatal(err)
	}
	return c
}

// withRole is the generation with role, or fails the test.
func (c indexCatalog) withRole(t *testing.T, role string) catalogGeneration {
	t.Helper()
	for _, g := range c.generations {
		if g.role == role {
			return g
		}
	}
	t.Fatalf("no %s generation in %+v", role, c.generations)
	return catalogGeneration{}
}

// indexSegment is a segment in an index's meta.json.
type indexSegment struct {
	ID      string `json:"segment_id"`
	MaxDoc  int    `json:"max_doc"`
	Deletes *struct {
		Count int `json:"num_deleted_docs"`
	} `json:"deletes"`
}

// indexDir is the directory of one generation's index.
func indexDir(dataDir string, generation int64) string {
	return filepath.Join(dataDir, "index", strconv.FormatInt(generation, 10))
}

// indexSegments reads the committed segments of a generation's index.
func indexSegments(t *testing.T, dataDir string, generation int64) []indexSegment {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(indexDir(dataDir, generation), "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Segments []indexSegment `json:"segments"`
	}
	if err := json.Unmarshal(content, &meta); err != nil {
		t.Fatalf("meta.json isn't JSON: %v", err)
	}
	return meta.Segments
}

// documents counts the live documents in segments.
func documents(segments []indexSegment) int {
	n := 0
	for _, s := range segments {
		n += s.MaxDoc
		if s.Deletes != nil {
			n -= s.Deletes.Count
		}
	}
	return n
}

// waitForCommit waits until a generation's index holds a committed segment.
func waitForCommit(t *testing.T, dataDir string, generation int64) {
	t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		content, err := os.ReadFile(filepath.Join(indexDir(dataDir, generation), "meta.json"))
		var meta struct {
			Segments []json.RawMessage `json:"segments"`
		}
		if err == nil && json.Unmarshal(content, &meta) == nil && len(meta.Segments) > 0 {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("index %d committed nothing within %s", generation, processTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// logLines returns the log lines with msg, parsed.
func (p *serverProcess) logLines(msg string) []map[string]any {
	var found []map[string]any
	for line := range strings.Lines(p.stderr()) {
		var entry map[string]any
		if json.Unmarshal([]byte(line), &entry) == nil && entry["msg"] == msg {
			found = append(found, entry)
		}
	}
	return found
}

// waitForLogLine waits until a log line has msg and returns it.
func (p *serverProcess) waitForLogLine(msg string) map[string]any {
	p.t.Helper()
	deadline := time.Now().Add(2 * processTimeout)
	for {
		if lines := p.logLines(msg); len(lines) > 0 {
			return lines[0]
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("no %q line within %s:\n%s", msg, 2*processTimeout, p.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// waitForState waits until the latest search state line has these states.
func (p *serverProcess) waitForState(search, indexing string) {
	p.t.Helper()
	deadline := time.Now().Add(2 * processTimeout)
	for {
		if lines := p.logLines("search state"); len(lines) > 0 {
			last := lines[len(lines)-1]
			if last["state"] == search && last["indexing"] == indexing {
				return
			}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("search and indexing weren't %s and %s within %s:\n%s", search, indexing, 2*processTimeout, p.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// startRebuild asks for a rebuild, and fails unless the server accepts it or
// dies before it answers.
func (p *serverProcess) startRebuild(token string) {
	p.t.Helper()
	request, err := http.NewRequest(http.MethodPost, p.baseURL+"/api/search/rebuild", nil)
	if err != nil {
		p.t.Fatal(err)
	}
	request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	response, err := (&http.Client{Timeout: 10 * time.Second}).Do(request)
	if err != nil {
		return
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != http.StatusAccepted {
		p.t.Fatalf("POST /api/search/rebuild = %d %s, want 202", response.StatusCode, body)
	}
}

// searchProcess is the server's running search process, found once it runs.
func (p *serverProcess) searchProcess() int {
	p.t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		for _, child := range childProcesses(p.t, p.cmd.Process.Pid) {
			if filepath.Base(child.args[0]) == "mailarkiv-search" {
				return child.pid
			}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("no search process within %s", processTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// killSearchProcess sends SIGKILL to the search process and returns when.
func (p *serverProcess) killSearchProcess() time.Time {
	p.t.Helper()
	if err := syscall.Kill(p.searchProcess(), syscall.SIGKILL); err != nil {
		p.t.Fatal(err)
	}
	return time.Now()
}

// searchIDs runs a search that must answer, and returns the IDs it found
// and how many messages it left out.
func (p *serverProcess) searchIDs(token, words string) (ids []int64, notIncluded int) {
	p.t.Helper()
	response, body := p.search(token, words)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("search = %d %s, want 200", response.StatusCode, body)
	}
	var answer struct {
		Items []struct {
			ID int64 `json:"id"`
		} `json:"items"`
		NotIncluded int `json:"not_included"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		p.t.Fatalf("the search answer isn't JSON: %q: %v", body, err)
	}
	for _, item := range answer.Items {
		ids = append(ids, item.ID)
	}
	slices.Sort(ids)
	return ids, answer.NotIncluded
}

// assertEachFoundOnce checks that words finds each message in ids once,
// and that each message's own word finds it alone.
func (p *serverProcess) assertEachFoundOnce(token string, words map[int64]string) {
	p.t.Helper()
	var all []int64
	for id, word := range words {
		all = append(all, id)
		if found, _ := p.searchIDs(token, word); !slices.Equal(found, []int64{id}) {
			p.t.Errorf("%s found %v, want message %d once", word, found, id)
		}
	}
	slices.Sort(all)
	if found, _ := p.searchIDs(token, sharedWord); !slices.Equal(found, all) {
		p.t.Errorf("%s found %v, want %v", sharedWord, found, all)
	}
}
