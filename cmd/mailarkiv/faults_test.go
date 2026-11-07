package main

import (
	"net/http"
	"os"
	"os/exec"
	"slices"
	"syscall"
	"testing"
)

// faultMail is a small synthetic message. These cases test how a failure
// reaches the client, so its size doesn't matter.
func faultMail(subject string) []byte {
	return []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: " + subject + "\r\n\r\nBody.\r\n")
}

// User Story 3 scenario 6, FR-013, risk 12: a storage failure shows as Failed,
// lists nothing, leaves no partial file, and reaches the status and the log.
func TestStorageFailures(t *testing.T) {
	const (
		storageFull  = "storage_full"
		storageWrite = "storage_write_failed"
		catalogWrite = "catalog_write_failed"
	)
	tests := []struct {
		fault  string
		status int
		reason string
		// noBlob is set where the failure comes before the rename. A failed
		// directory fsync comes after it, so that row leaves a whole blob.
		noBlob bool
		// blobKept is set where FR-038 says the original was stored.
		blobKept bool
	}{
		{fault: "temp-create=ENOSPC", status: http.StatusInsufficientStorage, reason: storageFull, noBlob: true},
		{fault: "temp-write=ENOSPC", status: http.StatusInsufficientStorage, reason: storageFull, noBlob: true},
		{fault: "temp-fsync=ENOSPC", status: http.StatusInsufficientStorage, reason: storageFull, noBlob: true},
		{fault: "temp-create=EACCES", status: http.StatusInternalServerError, reason: storageWrite, noBlob: true},
		{fault: "temp-write=EACCES", status: http.StatusInternalServerError, reason: storageWrite, noBlob: true},
		{fault: "temp-fsync=EACCES", status: http.StatusInternalServerError, reason: storageWrite, noBlob: true},
		{fault: "rename=EACCES", status: http.StatusInternalServerError, reason: storageWrite, noBlob: true},
		{fault: "dir-fsync=EIO", status: http.StatusInternalServerError, reason: storageWrite},
		{fault: "commit=EIO", status: http.StatusInternalServerError, reason: catalogWrite, blobKept: true},
	}
	for _, tt := range tests {
		t.Run(tt.fault, func(t *testing.T) {
			dataDir := t.TempDir()
			createAdmin(t, dataDir)
			server := startFaultServer(t, dataDir, tt.fault)
			token := signInOverHTTP(t, server.baseURL, adminAddress)
			content := faultMail(tt.fault)

			attempt := tryImport(server.baseURL, token, content)

			if attempt.err != nil {
				t.Fatalf("the import got no answer: %v", attempt.err)
			}
			if attempt.status != tt.status {
				t.Fatalf("the import = %d %s, want %d", attempt.status, attempt.body, tt.status)
			}
			answer := parseImportAnswer(t, attempt.body)
			if answer.Outcome != "failed" || answer.Reason != tt.reason {
				t.Errorf("the answer = %s, want outcome failed and reason %s", attempt.body, tt.reason)
			}
			if answer.Message != nil {
				t.Errorf("a failed import returned a message: %s", attempt.body)
			}

			server.assertNothingListed(token)
			assertNoTempFiles(t, dataDir)
			if tt.noBlob {
				if blobs := blobFiles(t, dataDir); len(blobs) != 0 {
					t.Errorf("blobs/ holds %v, want nothing", blobs)
				}
			}
			if tt.blobKept {
				assertOneBlob(t, dataDir, content)
			}

			failures := server.status(token).Failures
			if len(failures) != 1 || failures[0].Category != tt.reason || failures[0].At == "" {
				t.Errorf("failures = %+v, want one %s with a time", failures, tt.reason)
			}
			server.assertLogged(tt.reason)

			if !tt.blobKept {
				return
			}
			// FR-038: the stored original is listed as unreferenced at the next start.
			next := restart(t, dataDir)
			nextToken := signInOverHTTP(t, next.baseURL, adminAddress)
			want := []unreferencedEntry{{ContentHash: contentHash(content), Size: int64(len(content))}}
			if got := next.status(nextToken).Unreferenced; !slices.Equal(got, want) {
				t.Errorf("after a restart, unreferenced = %+v, want %+v", got, want)
			}
			next.assertNothingListed(nextToken)
		})
	}
}

// The data model's capture states: a failed catalog read is no failure event,
// because FR-038's Catalog write failed says the original was stored. The client
// shows Unconfirmed.
func TestRowReadFailure(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startFaultServer(t, dataDir, "row-read=EIO")
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	attempt := tryImport(server.baseURL, token, faultMail("row read"))

	if attempt.err != nil {
		t.Fatalf("the import got no answer: %v", attempt.err)
	}
	if attempt.status != http.StatusInternalServerError || attempt.body != `{"error":"internal_error"}` {
		t.Fatalf("the import = %d %s, want 500 internal_error", attempt.status, attempt.body)
	}
	server.assertNothingListed(token)
	assertNoTempFiles(t, dataDir)
	if blobs := blobFiles(t, dataDir); len(blobs) != 0 {
		t.Errorf("blobs/ holds %v, want nothing", blobs)
	}
	if failures := server.status(token).Failures; len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
	response, body := server.request(http.MethodGet, "/api/session", "", token)
	if response.StatusCode != http.StatusOK || body != `{"problem_count":0}` {
		t.Errorf("GET /api/session = %d %s, want a problem count of 0", response.StatusCode, body)
	}

	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}
	var logged bool
	for _, entry := range logEntries(t, server.stderr()) {
		if entry["category"] != nil {
			t.Errorf("a log line records a failure event: %v", entry)
		}
		if entry["level"] == "ERROR" && entry["route"] == "POST /api/imports" && entry["error"] != nil {
			logged = true
		}
	}
	if !logged {
		t.Errorf("no error line names the route:\n%s", server.stderr())
	}
}

// Only the faults build obeys the setting, so what ships can't be made to crash
// through its environment.
func TestDefaultBuildIgnoresFaults(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	cmd := exec.Command(serverBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")
	cmd.Env = append(os.Environ(), "MAILARKIV_FAULTS=after-rename=kill")
	server := startProcess(t, cmd)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	content := faultMail("default build")

	attempt := tryImport(server.baseURL, token, content)

	if attempt.err != nil {
		t.Fatalf("the import got no answer: %v", attempt.err)
	}
	if attempt.status != http.StatusCreated || parseImportAnswer(t, attempt.body).Outcome != "archived" {
		t.Fatalf("the import = %d %s, want 201 archived", attempt.status, attempt.body)
	}
	server.assertListedOnce(token, content, 1)
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}
}
