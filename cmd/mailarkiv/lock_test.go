package main

import (
	"bytes"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Risk 11: a second server on one data directory would delete the first's temp
// files in its recovery, and lose an upload in progress.
func TestSecondServerRefused(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	first := startServer(t, dataDir)
	token := signInOverHTTP(t, first.baseURL, adminAddress)

	content := crashFile("second server")
	const sentFirst = 8 << 10 // Past the 4 KiB file check.
	upload := startStalledUpload(t, first.baseURL, token, content[:sentFirst])
	inProgress := waitForTempFile(t, dataDir)

	code, stderr := runToExit(t, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := startupFailure(t, stderr)["cause"]; got != "lock" {
		t.Errorf("cause = %v, want lock", got)
	}
	if left := tempFiles(t, dataDir); !slices.Equal(left, []string{inProgress}) {
		t.Fatalf("tmp/ holds %v, want the first server's %s left alone", left, inProgress)
	}

	// The first server never noticed.
	upload.send(content[sentFirst:])
	attempt := upload.finish()
	if attempt.err != nil {
		t.Fatalf("the upload got no answer: %v", attempt.err)
	}
	if attempt.status != http.StatusCreated {
		t.Fatalf("the upload = %d %s, want 201", attempt.status, attempt.body)
	}
	first.assertListedOnce(token, content, 1)
	assertOneBlob(t, dataDir, content)
}

// User Story 5 scenario 6, FR-026: the server refuses to start on storage or a
// catalog it can't use, and the log says which. Plain files in the way keep this
// true when the test runs as root.
func TestUnusableStartup(t *testing.T) {
	garbage := bytes.Repeat([]byte("this is not a database\n"), 200)
	tests := []struct {
		name    string
		path    string
		content []byte
		cause   string
	}{
		{"a plain file where tmp belongs", "tmp", nil, "storage"},
		{"a plain file where blobs belongs", "blobs", nil, "storage"},
		{"a catalog that isn't a database", "catalog.db", garbage, "catalog"},
		// Both directories exist, so only writing the probe can find this.
		{"a probe that can't be written in tmp", "tmp/.probe/in-the-way", nil, "storage"},
		{"a probe that can't be written in blobs", "blobs/.probe/in-the-way", nil, "storage"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := filepath.Join(dataDir, filepath.FromSlash(tt.path))
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, tt.content, 0o600); err != nil {
				t.Fatal(err)
			}

			code, stderr := runToExit(t, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")

			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if got := startupFailure(t, stderr)["cause"]; got != tt.cause {
				t.Errorf("cause = %v, want %s", got, tt.cause)
			}
		})
	}
}

// FR-026: a database can open and still fail when recovery reads it, and the log
// has to name the catalog then too.
func TestCatalogUnusableInRecovery(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec("DROP TABLE source_observation; DROP TABLE message"); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	code, stderr := runToExit(t, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if got := startupFailure(t, stderr)["cause"]; got != "catalog" {
		t.Errorf("cause = %v, want catalog", got)
	}
}

// User Story 5 scenario 5: a temp file recovery can't remove is harmless, so the
// server still starts and reports it.
func TestTempFileNotRemoved(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	seedTempFile(t, dataDir)

	server := startFaultServer(t, dataDir, "temp-remove=EACCES")
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	if left := tempFiles(t, dataDir); len(left) != 1 {
		t.Errorf("tmp/ holds %v, want the file that couldn't be removed", left)
	}
	status := server.status(token)
	if status.Recovery.TempFilesRemoved != 0 || status.Recovery.TempFilesNotRemoved != 1 {
		t.Errorf("recovery = %+v, want none removed and 1 not removed", status.Recovery)
	}
	if len(status.Failures) != 1 {
		t.Fatalf("failures = %+v, want one", status.Failures)
	}
	failure := status.Failures[0]
	if failure.Category != "temp_file_not_removed" || failure.At == "" {
		t.Errorf("failure = %+v, want temp_file_not_removed with a time", failure)
	}
	if failure.MessageID != nil || failure.ContentHash != nil {
		t.Errorf("failure = %+v, want no message ID or content hash", failure)
	}
	server.assertLogged("temp_file_not_removed")
}
