package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// collectCrashSize is each message's size, enough for capture to write it in
// more than one piece.
const collectCrashSize = 256 << 10

// collectCrash is a data directory with an account and a key, and a double
// whose INBOX holds three messages.
type collectCrash struct {
	double  *imaptest.Server
	caFile  string
	dataDir string
	keyPath string
	mail    [][]byte
	uids    []imap.UID
}

func newCollectCrash(t *testing.T, point string) *collectCrash {
	t.Helper()
	double, caFile := newProcessDouble(t)
	c := &collectCrash{double: double, caFile: caFile, dataDir: filepath.Join(t.TempDir(), "data")}
	for i := range 3 {
		head := fmt.Sprintf("From: ben@example.test\r\nSubject: Message %d at %s\r\n\r\n", i+1, point)
		body := append([]byte(head), bytes.Repeat([]byte("y"), collectCrashSize-len(head))...)
		uid, err := double.AddMessage("INBOX", body, time.Date(2026, 11, 4, 9, i, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		c.mail = append(c.mail, body)
		c.uids = append(c.uids, uid)
	}
	c.keyPath = createKey(t, c.dataDir)
	createAdmin(t, c.dataDir)
	return c
}

// startFaults runs the faults binary with collection and faults.
func (c *collectCrash) startFaults(t *testing.T, faults string) *serverProcess {
	t.Helper()
	cmd := exec.Command(faultsBinary, "serve", "--data-dir", c.dataDir, "--listen", "127.0.0.1:0",
		"--key-file", c.keyPath, "--preview-collection")
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+c.caFile, "MAILARKIV_FAULTS="+faults)
	return startProcess(t, cmd)
}

// addSource posts the double's mailbox. The first sync may kill the process
// before the answer arrives, so any answer, or none, is accepted.
func (c *collectCrash) addSource(t *testing.T, server *serverProcess, token string) {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, server.baseURL+"/api/sources", strings.NewReader(sourceRequest(t, c.double, "INBOX")))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	client := &http.Client{Timeout: 10 * time.Second}
	if response, err := client.Do(request); err == nil {
		response.Body.Close()
	}
}

// checkpoint reads the source and INBOX's last UID from the catalog, while
// no server runs.
func (c *collectCrash) checkpoint(t *testing.T) (sourceID int64, lastUID imap.UID) {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(c.dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.Reader.QueryRow(
		"SELECT s.id, f.last_uid FROM imap_source s JOIN imap_folder f ON f.source_id = s.id WHERE f.name = 'INBOX'").
		Scan(&sourceID, &lastUID); err != nil {
		t.Fatal(err)
	}
	return sourceID, lastUID
}

// collectedAnswer is the part of GET /api/sources/{id} this test reads.
type collectedAnswer struct {
	Collected int `json:"collected"`
}

func (p *serverProcess) collected(token string, id int64) int {
	p.t.Helper()
	response, body := p.request(http.MethodGet, fmt.Sprintf("/api/sources/%d", id), "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/sources/%d = %d %s, want 200", id, response.StatusCode, body)
	}
	var answer collectedAnswer
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		p.t.Fatalf("the source isn't JSON: %q: %v", body, err)
	}
	return answer.Collected
}

// listedHashes returns the listed messages' hashes, sorted, and fails the
// test unless each has one entry.
func (p *serverProcess) listedHashes(token string) []string {
	p.t.Helper()
	items, total := p.messages(token)
	if total != len(items) {
		p.t.Fatalf("the list holds %d messages on its first page of %d", len(items), total)
	}
	var hashes []string
	for _, item := range items {
		if item.ObservationCount != 1 {
			p.t.Errorf("message %d has %d entries, want 1", item.ID, item.ObservationCount)
		}
		hashes = append(hashes, item.ContentHash)
	}
	slices.Sort(hashes)
	return hashes
}

func (c *collectCrash) hashes(indexes ...int) []string {
	var hashes []string
	for _, i := range indexes {
		hashes = append(hashes, contentHash(c.mail[i]))
	}
	slices.Sort(hashes)
	return hashes
}

// The server stops at each step of collecting message 1. Before the next
// sync, the archive holds what the stop left. After it, the archive matches
// the double's mailbox, with one entry and one stored copy per message.
func TestCollectCrashMatrix(t *testing.T) {
	tests := []struct {
		point string
		// faults is MAILARKIV_FAULTS. A kill stops the process; otherwise the
		// first sync fails with failedWith and the server stops normally.
		faults     string
		failedWith string
		// listed are the messages listed after the stop, by index in mail.
		listed []int
		// atMessage is whether the checkpoint after the stop is at message 1
		// rather than before it.
		atMessage bool
		// unreferenced is whether message 1's original is stored but not
		// listed after the stop.
		unreferenced bool
		// bodyFetches is how often the double served message 1's body by the
		// end.
		bodyFetches int
	}{
		{point: "before-temp-create", faults: "before-temp-create=kill", bodyFetches: 2},
		{point: "mid-temp-write", faults: "mid-temp-write=kill", bodyFetches: 2},
		{point: "after-temp-fsync", faults: "after-temp-fsync=kill", bodyFetches: 2},
		{point: "after-rename", faults: "after-rename=kill", unreferenced: true, bodyFetches: 2},
		{point: "after-dir-fsync", faults: "after-dir-fsync=kill", unreferenced: true, bodyFetches: 2},
		{point: "before-commit", faults: "before-commit=kill", unreferenced: true, bodyFetches: 2},
		{point: "after-commit", faults: "after-commit=kill", listed: []int{0}, bodyFetches: 2},
		{point: "after-checkpoint-commit", faults: "after-checkpoint-commit=kill", listed: []int{0}, atMessage: true, bodyFetches: 1},
		{point: "checkpoint-commit", faults: "checkpoint-commit=EIO", failedWith: "catalog_write_failed", listed: []int{0}, bodyFetches: 2},
	}
	for _, tt := range tests {
		t.Run(tt.faults, func(t *testing.T) {
			c := newCollectCrash(t, tt.point)
			faulty := c.startFaults(t, tt.faults)
			_, token := attemptSignIn(faulty, adminAddress, accountPassword)
			c.addSource(t, faulty, token)
			if tt.failedWith == "" {
				faulty.waitForKill()
			} else {
				faulty.waitForSyncFailure(tt.failedWith)
				faulty.assertRetrying(token, tt.failedWith)
				if code := faulty.stop(syscall.SIGTERM); code != 0 {
					t.Fatalf("exit = %d, want 0:\n%s", code, faulty.stderr())
				}
			}

			sourceID, lastUID := c.checkpoint(t)
			want := imap.UID(0)
			if tt.atMessage {
				want = c.uids[0]
			}
			if lastUID != want {
				t.Errorf("after the stop last_uid = %d, want %d", lastUID, want)
			}

			// A restart without collection runs recovery and syncs nothing.
			recovered := startServer(t, c.dataDir)
			assertNoTempFiles(t, c.dataDir)
			if got, want := recovered.listedHashes(token), c.hashes(tt.listed...); !slices.Equal(got, want) {
				t.Errorf("after the stop the list holds %v, want %v", got, want)
			}
			status := recovered.status(token)
			var wantUnreferenced []unreferencedEntry
			if tt.unreferenced {
				wantUnreferenced = []unreferencedEntry{{ContentHash: contentHash(c.mail[0]), Size: collectCrashSize}}
			}
			if !slices.Equal(status.Unreferenced, wantUnreferenced) {
				t.Errorf("after the stop unreferenced = %+v, want %+v", status.Unreferenced, wantUnreferenced)
			}
			if code := recovered.stop(syscall.SIGTERM); code != 0 {
				t.Fatalf("exit = %d, want 0:\n%s", code, recovered.stderr())
			}

			server := startCollecting(t, c.dataDir, c.keyPath, c.caFile, true)
			server.waitForSuccessAfter(token, sourceID, "")

			if got, want := server.listedHashes(token), c.hashes(0, 1, 2); !slices.Equal(got, want) {
				t.Errorf("after the sync the list holds %v, want all three", got)
			}
			if blobs := blobFiles(t, c.dataDir); len(blobs) != 3 {
				t.Errorf("blobs/ holds %d files, want 3", len(blobs))
			}
			for _, body := range c.mail {
				stored, err := os.ReadFile(blobPath(c.dataDir, contentHash(body)))
				if err != nil || !bytes.Equal(stored, body) {
					t.Errorf("the original of %s differs from the double's bytes: %v", contentHash(body), err)
				}
			}
			assertNoTempFiles(t, c.dataDir)
			if unreferenced := server.status(token).Unreferenced; len(unreferenced) != 0 {
				t.Errorf("after the sync unreferenced = %+v, want none", unreferenced)
			}
			if n := server.collected(token, sourceID); n != 3 {
				t.Errorf("collected = %d, want 3", n)
			}
			if got := c.double.BodyFetches("INBOX", c.uids[0]); got != tt.bodyFetches {
				t.Errorf("message 1's body was fetched %d times, want %d", got, tt.bodyFetches)
			}
		})
	}
}

// waitForSyncFailure waits for the log line of a sync that failed with
// category.
func (p *serverProcess) waitForSyncFailure(category string) {
	p.t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		for line := range strings.Lines(p.stderr()) {
			var entry struct {
				Msg      string `json:"msg"`
				Category string `json:"category"`
			}
			if json.Unmarshal([]byte(line), &entry) == nil && entry.Msg == "sync failed" && entry.Category == category {
				return
			}
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("no sync failed with %s within %s:\n%s", category, processTimeout, p.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}
