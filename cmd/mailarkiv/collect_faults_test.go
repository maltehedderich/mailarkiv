package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"syscall"
	"testing"
)

// sourceState is the part of a source that shows how its syncs go.
type sourceState struct {
	ID      int64  `json:"id"`
	State   string `json:"state"`
	Failure *struct {
		Category string `json:"category"`
	} `json:"failure"`
}

// onlySource reads GET /api/sources, which must list one source.
func (p *serverProcess) onlySource(token string) sourceState {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/sources", "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/sources = %d %s, want 200", response.StatusCode, body)
	}
	var answer struct {
		Sources []sourceState `json:"sources"`
	}
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		p.t.Fatalf("the list isn't JSON: %q: %v", body, err)
	}
	if len(answer.Sources) != 1 {
		p.t.Fatalf("GET /api/sources = %s, want one source", body)
	}
	return answer.Sources[0]
}

// assertRetrying checks that the only source is Retrying after a failure
// with category.
func (p *serverProcess) assertRetrying(token, category string) {
	p.t.Helper()
	source := p.onlySource(token)
	if source.State != "retrying" || source.Failure == nil || source.Failure.Category != category {
		p.t.Errorf("source = %+v, want retrying with %s", source, category)
	}
}

// A storage or catalog failure while collecting fails the sync with its
// category, which the source shows, and leaves the checkpoint before the
// message. The failure events on the status screen are for imports, so it
// lists none. A restart without the fault archives the message.
func TestCollectStorageFailures(t *testing.T) {
	tests := []struct {
		faults   string
		category string
	}{
		{"temp-write=ENOSPC", "storage_full"},
		{"temp-write=EACCES", "storage_write_failed"},
		{"commit=EIO", "catalog_write_failed"},
	}
	for _, tt := range tests {
		t.Run(tt.faults, func(t *testing.T) {
			c := newCollectCrash(t, tt.faults)
			faulty := c.startFaults(t, tt.faults)
			_, token := attemptSignIn(faulty, adminAddress, accountPassword)
			response, body := faulty.request(http.MethodPost, "/api/sources", sourceRequest(t, c.double, "INBOX"), token)
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, body)
			}
			faulty.waitForSyncFailure(tt.category)

			faulty.assertRetrying(token, tt.category)
			if failures := faulty.status(token).Failures; len(failures) != 0 {
				t.Errorf("failures = %+v, want none", failures)
			}
			if code := faulty.stop(syscall.SIGTERM); code != 0 {
				t.Fatalf("exit = %d, want 0:\n%s", code, faulty.stderr())
			}
			assertArchiveLogged(t, faulty.stderr(), tt.category)

			sourceID, lastUID := c.checkpoint(t)
			if lastUID != 0 {
				t.Errorf("last_uid = %d, want 0", lastUID)
			}

			server := startCollecting(t, c.dataDir, c.keyPath, c.caFile, true)
			server.waitForSuccessAfter(token, sourceID, "")
			if got, want := server.listedHashes(token), c.hashes(0, 1, 2); !slices.Equal(got, want) {
				t.Errorf("after a restart the list holds %v, want all three", got)
			}
		})
	}
}

// assertArchiveLogged checks for archive's line about a collected message it
// didn't store.
func assertArchiveLogged(t *testing.T, stderr, category string) {
	t.Helper()
	for _, entry := range logEntries(t, stderr) {
		if entry["msg"] == "collected message not stored" && entry["category"] == category {
			return
		}
	}
	t.Errorf("no collected message not stored line with %s:\n%s", category, stderr)
}
