package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// The second mailbox some tests collect, with its own marker password.
const (
	otherMailboxUser     = "ben@example.test"
	otherMailboxPassword = "MARKER-PW-9b3e"
)

// listedSources reads GET /api/sources: its collection field, and each
// source by ID.
func (p *serverProcess) listedSources(token string) (collection string, sources map[int64]map[string]any) {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/sources", "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/sources = %d %s, want 200", response.StatusCode, body)
	}
	var answer struct {
		Collection string           `json:"collection"`
		Sources    []map[string]any `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		p.t.Fatalf("the list isn't JSON: %q: %v", body, err)
	}
	sources = map[int64]map[string]any{}
	for _, source := range answer.Sources {
		id, ok := source["id"].(float64)
		if !ok {
			p.t.Fatalf("a source has no numeric id: %s", body)
		}
		sources[int64(id)] = source
	}
	return answer.Collection, sources
}

// assertPasswordUnreadable checks that source needs attention because its
// password can't be read, found between from and to.
func assertPasswordUnreadable(t *testing.T, source map[string]any, from, to time.Time) {
	t.Helper()
	if source["state"] != "needs_attention" || source["next_sync_at"] != nil {
		t.Errorf("source = %v, want needs_attention with no next sync", source)
	}
	failure, ok := source["failure"].(map[string]any)
	if !ok || failure["category"] != "password_unreadable" {
		t.Fatalf("failure = %v, want password_unreadable", source["failure"])
	}
	since, err := time.Parse(time.RFC3339Nano, fmt.Sprint(failure["since"]))
	if err != nil || since.Before(from.Truncate(time.Millisecond)) || since.After(to) {
		t.Errorf("since = %v, want a time from %v to %v", failure["since"], from, to)
	}
	for _, field := range []string{"failed_attempts", "last_at", "reply"} {
		if value, present := failure[field]; !present || value != nil {
			t.Errorf("failure %s = %v (present %t), want null", field, value, present)
		}
	}
}

// collectOnce starts the binary with the key, adds a source for each body,
// waits for each to sync, and stops it. It returns the IDs in order.
func collectOnce(t *testing.T, dataDir, keyPath, caFile string, bodies ...string) []int64 {
	t.Helper()
	server := startCollecting(t, dataDir, keyPath, caFile, true)
	_, token := attemptSignIn(server, adminAddress, accountPassword)
	var ids []int64
	for _, body := range bodies {
		id := server.addSource(token, body)
		server.waitForSuccessAfter(token, id, "")
		ids = append(ids, id)
	}
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit code = %d, want 0:\n%s", code, server.stderr())
	}
	return ids
}

// Without a key, collection is unavailable and the rest works. A source
// saved under an earlier start needs attention, and nothing signs in.
func TestNoKey(t *testing.T) {
	double, caFile := newProcessDouble(t)
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	id := collectOnce(t, dataDir, keyPath, caFile, sourceRequest(t, double, "INBOX"))[0]
	logins := doubleLogins(double)

	started := time.Now()
	server := startCollecting(t, dataDir, "", caFile, true)
	_, token := attemptSignIn(server, adminAddress, accountPassword)

	var unavailable int
	for _, entry := range logEntries(t, server.stderr()) {
		if entry["msg"] == "collection unavailable" {
			unavailable++
			if entry["level"] != "INFO" || entry["reason"] != "no key file given" {
				t.Errorf("log entry = %v, want INFO with reason no key file given", entry)
			}
		}
	}
	if unavailable != 1 {
		t.Errorf("the log has %d collection unavailable lines, want 1:\n%s", unavailable, server.stderr())
	}

	collection, sources := server.listedSources(token)
	if collection != "no_key" {
		t.Errorf("collection = %q, want no_key", collection)
	}
	source, ok := sources[id]
	if !ok {
		t.Fatalf("source %d isn't listed: %v", id, sources)
	}
	assertPasswordUnreadable(t, source, started, time.Now())

	response, body := server.request(http.MethodPost, "/api/sources", sourceRequestFor(t, double, "Again", "carla@example.test", "another", "INBOX"), token)
	if response.StatusCode != http.StatusConflict || body != `{"error":"no_key"}` {
		t.Errorf("POST /api/sources = %d %s, want 409 no_key", response.StatusCode, body)
	}

	content := []byte("From: carla@example.test\r\nSubject: Imported without a key\r\n\r\nStill works\r\n")
	attempt := tryImport(server.baseURL, token, content)
	if attempt.err != nil || attempt.status != http.StatusCreated {
		t.Fatalf("the import = %d %s %v, want 201", attempt.status, attempt.body, attempt.err)
	}
	imported := parseImportAnswer(t, attempt.body)
	if imported.Message == nil {
		t.Fatalf("the import's answer has no message: %s", attempt.body)
	}
	response, original := server.request(http.MethodGet, "/api/messages/"+strconv.FormatInt(imported.Message.ID, 10)+"/original", "", token)
	if response.StatusCode != http.StatusOK || original != string(content) {
		t.Errorf("the download = %d with %d bytes, want 200 with the imported %d bytes", response.StatusCode, len(original), len(content))
	}

	if got := doubleLogins(double); got != logins {
		t.Errorf("LOGIN count = %d, want %d", got, logins)
	}
}

// Started with another key, no stored password opens: each source needs
// attention, the log says so once per source, and nothing signs in.
func TestWrongKey(t *testing.T) {
	anna := newMailServer(t, mailboxUser, mailboxPassword)
	ben := newMailServer(t, otherMailboxUser, otherMailboxPassword)
	caFile := writeCAs(t, anna, ben)
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	ids := collectOnce(t, dataDir, keyPath, caFile,
		sourceRequest(t, anna, "INBOX"),
		sourceRequestFor(t, ben, "Ben", otherMailboxUser, otherMailboxPassword, "INBOX"))
	before := map[*imaptest.Server]int{anna: doubleLogins(anna), ben: doubleLogins(ben)}

	started := time.Now()
	server := startCollecting(t, dataDir, createKey(t, dataDir), caFile, true)
	_, token := attemptSignIn(server, adminAddress, accountPassword)

	collection, sources := server.listedSources(token)
	if collection != "available" {
		t.Errorf("collection = %q, want available", collection)
	}
	checked := time.Now()
	for _, id := range ids {
		source, ok := sources[id]
		if !ok {
			t.Fatalf("source %d isn't listed: %v", id, sources)
		}
		assertPasswordUnreadable(t, source, started, checked)
	}

	logged := map[int64]int{}
	for _, entry := range logEntries(t, server.stderr()) {
		if entry["msg"] != "password unreadable" {
			continue
		}
		if entry["level"] != "WARN" || entry["category"] != "password_unreadable" {
			t.Errorf("log entry = %v, want WARN with category password_unreadable", entry)
		}
		id, _ := entry["source_id"].(float64)
		logged[int64(id)]++
	}
	if len(logged) != len(ids) || logged[ids[0]] != 1 || logged[ids[1]] != 1 {
		t.Errorf("password unreadable lines by source = %v, want one for each of %v", logged, ids)
	}

	for double, n := range before {
		if got := doubleLogins(double); got != n {
			t.Errorf("LOGIN count = %d, want %d", got, n)
		}
	}
}
