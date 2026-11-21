package main

import (
	"bytes"
	"net/http"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// shutdownLimit is the 5 second grace period plus room for a busy machine.
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

// A sync held partway through a message can't hold the process past the
// grace period. The message isn't archived, and the next start archives it
// once.
func TestShutdownDuringSync(t *testing.T) {
	// The 5 second grace period plus one second.
	const limit = 6 * time.Second
	double, caFile := newProcessDouble(t)
	// Half of it is past capture's 4 KiB file check, so a temp file exists.
	content := append(faultMail("held partway through"), bytes.Repeat([]byte("Body.\r\n"), 8<<10)...)
	uid, err := double.AddMessage("INBOX", content, time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := double.SetFault("INBOX", uid, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	server := startCollecting(t, dataDir, keyPath, caFile)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	response, body := server.request(http.MethodPost, "/api/sources", sourceRequest(t, double, "INBOX"), token)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, body)
	}
	waitForTempFile(t, dataDir)

	started := time.Now()
	code := server.stop(syscall.SIGTERM)
	elapsed := time.Since(started)

	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, server.stderr())
	}
	if elapsed > limit {
		t.Errorf("the server took %s to exit, want at most %s", elapsed, limit)
	}
	assertNoTempFiles(t, dataDir)

	if err := double.SetFault("INBOX", uid, imaptest.Fault{}); err != nil {
		t.Fatal(err)
	}
	next := startCollecting(t, dataDir, keyPath, caFile)
	next.waitForSuccessAfter(token, next.onlySource(token).ID, "")
	if got := next.listedHashes(token); !slices.Equal(got, []string{contentHash(content)}) {
		t.Errorf("after a restart the list holds %v, want the message once", got)
	}
}

// SIGTERM ends the search process and the parse workers inside the grace
// period, and the server leaves no child process behind.
func TestShutdownWithSearchRunning(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	importFixture(t, server, token, plainFixture(t, "html-only.eml"))
	server.waitForSearch(token, "quartals")

	children := childProcesses(t, server.cmd.Process.Pid)
	workers, searchProcesses := 0, 0
	for _, child := range children {
		switch {
		case len(child.args) == 2 && filepath.Base(child.args[0]) == "mailarkiv" && child.args[1] == "parse-worker":
			workers++
		case len(child.args) == 1 && filepath.Base(child.args[0]) == "mailarkiv-search":
			searchProcesses++
		}
	}
	// Two parse workers for processing and two for the message view.
	if len(children) != 5 || workers != 4 || searchProcesses != 1 {
		t.Fatalf("the server's children are %+v, want four parse workers and one search process", children)
	}

	started := time.Now()
	code := server.stop(syscall.SIGTERM)
	elapsed := time.Since(started)
	if code != 0 {
		t.Errorf("exit code = %d, want 0:\n%s", code, server.stderr())
	}
	if elapsed > shutdownLimit {
		t.Errorf("the server took %s to exit, want at most %s", elapsed, shutdownLimit)
	}
	for _, child := range children {
		if isAlive(child.pid) {
			t.Errorf("child %d %v still runs after the server exited", child.pid, child.args)
		}
	}
}
