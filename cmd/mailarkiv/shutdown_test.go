package main

import (
	"net/http"
	"syscall"
	"testing"
	"time"
)

// shutdownLimit is the 5 second grace period plus room for a busy machine. The
// task's "about 6 seconds" is the same bound without that room.
const shutdownLimit = 8 * time.Second

// A stalled upload can't hold the process past the grace period, and the client
// that sent it never sees success.
func TestShutdownDuringImport(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	const sentFirst = 8 << 10 // Past the 4 KiB file check.
	upload := startStalledUpload(t, server.baseURL, token, crashFile("shutdown")[:sentFirst])
	waitForTempFile(t, dataDir)

	started := time.Now()
	code := server.stop(syscall.SIGTERM)
	elapsed := time.Since(started)

	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, server.stderr())
	}
	if elapsed > shutdownLimit {
		t.Errorf("the server took %s to exit, want at most %s", elapsed, shutdownLimit)
	}
	attempt := upload.abandon()
	if attempt.err == nil && attempt.status < http.StatusMultipleChoices {
		t.Errorf("the client saw success: %d %s", attempt.status, attempt.body)
	}

	next := startServer(t, dataDir)
	assertNoTempFiles(t, dataDir)
	next.assertNothingListed(token)
	if blobs := blobFiles(t, dataDir); len(blobs) != 0 {
		t.Errorf("blobs/ holds %v, want nothing", blobs)
	}
}
