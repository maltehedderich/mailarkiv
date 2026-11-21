package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// searchBackWithin is how soon after a kill search must answer again.
const searchBackWithin = 70 * time.Second

// waitForSearchBack searches every 200 ms until search answers 200, and fails
// once searchBackWithin has passed since killed.
func (p *serverProcess) waitForSearchBack(token string, killed time.Time) {
	p.t.Helper()
	for {
		response, _ := p.search(token, sharedWord)
		if response.StatusCode == http.StatusOK {
			p.t.Logf("search answered again %s after the kill", time.Since(killed).Round(time.Millisecond))
			return
		}
		if time.Since(killed) > searchBackWithin {
			p.t.Fatalf("search didn't answer within %s of the kill:\n%s", searchBackWithin, p.stderr())
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// waitForStopLogged waits for the failure line of a search process stop.
func (p *serverProcess) waitForStopLogged() time.Time {
	p.t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		for _, entry := range p.logLines("search failure") {
			if entry["category"] == "search_process_stopped" {
				return time.Now()
			}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("no search_process_stopped line within %s:\n%s", processTimeout, p.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// idOf finds the listed message with content's hash.
func (p *serverProcess) idOf(token string, content []byte) int64 {
	p.t.Helper()
	items, _ := p.messages(token)
	for _, item := range items {
		if item.ContentHash == contentHash(content) {
			return item.ID
		}
	}
	p.t.Fatalf("no listed message has hash %s: %+v", contentHash(content), items)
	return 0
}

// searchKill is one way the search process dies.
type searchKill struct {
	name string
	// searchFaults goes to the first search process the server starts.
	searchFaults string
	// indexedFirst has the first message indexed before the faults binary
	// starts, so the faults binary can start a rebuild at once.
	indexedFirst bool
	// kill ends the search process once the first message is captured, and
	// returns when it ended.
	kill func(t *testing.T, server *serverProcess, token, dataDir string) time.Time
}

var searchKills = []searchKill{
	{
		name:         "after-commit=exit",
		searchFaults: "after-commit=exit",
		kill: func(t *testing.T, server *serverProcess, token, dataDir string) time.Time {
			return server.waitForStopLogged()
		},
	},
	{
		name:         "during-search=exit",
		searchFaults: "during-search=exit",
		kill: func(t *testing.T, server *serverProcess, token, dataDir string) time.Time {
			server.waitForState("available", "up_to_date")
			response, body := server.search(token, sharedWord)
			var answer struct {
				Error string `json:"error"`
				Cause string `json:"cause"`
			}
			if err := json.Unmarshal([]byte(body), &answer); err != nil || response.StatusCode != http.StatusServiceUnavailable ||
				answer.Error != "search_unavailable" || answer.Cause != "search_process_stopped" {
				t.Errorf("the search that ended the process = %d %s, want 503 search_unavailable for search_process_stopped",
					response.StatusCode, body)
			}
			return server.waitForStopLogged()
		},
	},
	{
		// The first commit hangs, so the batch never gets its answer.
		name:         "SIGKILL during a batch",
		searchFaults: "after-commit=hang",
		kill: func(t *testing.T, server *serverProcess, token, dataDir string) time.Time {
			var building int64
			deadline := time.Now().Add(processTimeout)
			for building == 0 {
				for _, g := range readIndexCatalog(t, dataDir).generations {
					building = g.id
				}
				if time.Now().After(deadline) {
					t.Fatalf("no generation within %s", processTimeout)
				}
				time.Sleep(20 * time.Millisecond)
			}
			waitForCommit(t, dataDir, building)
			return server.killSearchProcess()
		},
	},
	{
		// The rebuild's first commit hangs, so the rebuild can't finish
		// before the kill.
		name:         "SIGKILL during a rebuild",
		searchFaults: "after-commit=hang",
		indexedFirst: true,
		kill: func(t *testing.T, server *serverProcess, token, dataDir string) time.Time {
			server.waitForState("available", "up_to_date")
			server.startRebuild(token)
			started := server.waitForLogLine("rebuild started")
			waitForCommit(t, dataDir, int64(started["generation_id"].(float64)))
			return server.killSearchProcess()
		},
	},
}

// Whatever ends the search process, captures go on, search answers again
// within 70 s, and every message is then found once.
func TestSearchProcessKilled(t *testing.T) {
	for _, kill := range searchKills {
		t.Run(kill.name, func(t *testing.T) {
			t.Parallel()
			double, caFile := newProcessDouble(t)
			dataDir := filepath.Join(t.TempDir(), "data")
			keyPath := createKey(t, dataDir)
			createAdmin(t, dataDir)
			words := map[int64]string{}

			first := indexMail("eintraga")
			if kill.indexedFirst {
				server := startCollecting(t, dataDir, keyPath, caFile)
				token := signInOverHTTP(t, server.baseURL, adminAddress)
				words[importFixture(t, server, token, first)] = "eintraga"
				server.waitForAllIndexed(token, sharedWord, 1)
				if code := server.stop(syscall.SIGTERM); code != 0 {
					t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
				}
			}
			server := startFaultsWith(t, dataDir, "", kill.searchFaults,
				[]string{"--key-file", keyPath}, "SSL_CERT_FILE="+caFile)
			token := signInOverHTTP(t, server.baseURL, adminAddress)
			source := server.addSource(token, sourceRequest(t, double, "INBOX"))
			synced := server.waitForSuccessAfter(token, source, "")
			if !kill.indexedFirst {
				words[importFixture(t, server, token, first)] = "eintraga"
			}

			killed := kill.kill(t, server, token, dataDir)

			// While search is down or restarting, an import and a sync work.
			words[importFixture(t, server, token, indexMail("eintragb"))] = "eintragb"
			collected := indexMail("eintragc")
			if _, err := double.AddMessage("INBOX", collected, time.Date(2026, 11, 6, 10, 0, 0, 0, time.UTC)); err != nil {
				t.Fatal(err)
			}
			server.syncNow(token, source)
			if after := server.waitForSuccessAfter(token, source, synced.LastSuccess.At); after.LastSuccess.Archived != 1 {
				t.Errorf("the sync archived %d, want 1", after.LastSuccess.Archived)
			}
			words[server.idOf(token, collected)] = "eintragc"

			server.waitForSearchBack(token, killed)
			server.waitForAllIndexed(token, sharedWord, 3)
			server.assertEachFoundOnce(token, words)
			if kill.indexedFirst {
				server.waitForLogLine("rebuild finished")
			}
		})
	}
}

// While search is unavailable, the list, a message's view, its preview, and
// the original and attachment downloads all work.
func TestCaptureWhileSearchKilled(t *testing.T) {
	dir := t.TempDir()
	binary := filepath.Join(dir, "mailarkiv")
	program := filepath.Join(dir, "mailarkiv-search")
	if err := copyFile(serverBinary, binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := copyFile(filepath.Join(filepath.Dir(serverBinary), "mailarkiv-search"), program, 0o755); err != nil {
		t.Fatal(err)
	}
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startProcess(t, exec.Command(binary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"))
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	htmlOnly := importFixture(t, server, token, plainFixture(t, "html-only.eml"))
	attachment := plainFixture(t, "attachment.eml")
	attached := importFixture(t, server, token, attachment)
	server.waitForSearch(token, "quartals")
	server.waitForSearch(token, "vertrag")

	// Without its program, the search process can't start again.
	if err := os.Rename(program, program+".away"); err != nil {
		t.Fatal(err)
	}
	server.killSearchProcess()
	server.waitForStopLogged()
	assertSearchUnavailable(t, server, token)

	if items, total := server.messages(token); total != 2 || len(items) != 2 {
		t.Errorf("the list holds %+v with total %d, want both messages", items, total)
	}
	response, body := server.request(http.MethodGet, fmt.Sprintf("/api/messages/%d/content", attached), "", token)
	var content struct {
		Attachments []struct {
			Position string `json:"position"`
		} `json:"attachments"`
	}
	if err := json.Unmarshal([]byte(body), &content); err != nil || response.StatusCode != http.StatusOK || len(content.Attachments) != 1 {
		t.Fatalf("the view = %d %s, want 200 with one attachment", response.StatusCode, body)
	}
	if response, body := server.request(http.MethodGet, fmt.Sprintf("/api/messages/%d/html", htmlOnly), "", token); response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Type") != "text/html; charset=utf-8" {
		t.Errorf("the preview = %d %s with %s, want 200 HTML", response.StatusCode, body, response.Header.Get("Content-Type"))
	}
	if response, body := server.request(http.MethodGet, fmt.Sprintf("/api/messages/%d/original", attached), "", token); response.StatusCode != http.StatusOK ||
		body != string(attachment) {
		t.Errorf("the original download = %d with %d bytes, want 200 with the %d imported bytes", response.StatusCode, len(body), len(attachment))
	}
	path := fmt.Sprintf("/api/messages/%d/attachments/%s", attached, content.Attachments[0].Position)
	if response, body := server.request(http.MethodGet, path, "", token); response.StatusCode != http.StatusOK ||
		response.Header.Get("Content-Length") != strconv.Itoa(len(body)) || len(body) == 0 {
		t.Errorf("the attachment download = %d with %d bytes, want 200 with its bytes", response.StatusCode, len(body))
	}
	assertSearchUnavailable(t, server, token)

	if err := os.Rename(program+".away", program); err != nil {
		t.Fatal(err)
	}
	found := server.waitForSearch(token, "vertrag")
	if found.Total != 1 || found.Items[0].ID != attached {
		t.Errorf("after the program came back search found %+v, want message %d", found.Items, attached)
	}
}

// assertSearchUnavailable checks that a search answers 503.
func assertSearchUnavailable(t *testing.T, server *serverProcess, token string) {
	t.Helper()
	response, body := server.search(token, sharedWord)
	var answer struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil || response.StatusCode != http.StatusServiceUnavailable ||
		answer.Error != "search_unavailable" {
		t.Errorf("search = %d %s, want 503 search_unavailable", response.StatusCode, body)
	}
}
