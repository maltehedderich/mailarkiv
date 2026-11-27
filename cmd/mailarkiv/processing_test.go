package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"
)

// Parse workers that hang never hold up capture. Imports and a sync still
// store their messages, which wait unprocessed, and SIGTERM ends the server
// and its hung workers inside the grace period.
func TestHungWorkers(t *testing.T) {
	double, caFile := newProcessDouble(t)
	received := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	collected := []byte("From: ben@example.test\r\nSubject: Abgeholt\r\n\r\nAus dem Postfach\r\n")
	if _, err := double.AddMessage("INBOX", collected, received); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	cmd := exec.Command(faultsBinary, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0", "--key-file", keyPath)
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caFile, "MAILARKIV_FAULTS=parse-worker=hang")
	server := startProcess(t, cmd)
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	imported := importFixture(t, server, token, plainFixture(t, "html-only.eml"))
	source := server.addSource(token, sourceRequest(t, double, "INBOX"))
	if sync := server.waitForSuccessAfter(token, source, ""); sync.LastSuccess.Archived != 1 {
		t.Fatalf("the sync archived %d, want 1", sync.LastSuccess.Archived)
	}

	// A working worker reads either message in milliseconds, so after a
	// second only a hang leaves them waiting.
	time.Sleep(time.Second)
	items, total := server.messages(token)
	if total != 2 {
		t.Fatalf("the list holds %d messages, want 2", total)
	}
	for _, item := range items {
		if item.Processing != "not_processed" {
			t.Errorf("message %d is %s, want not_processed", item.ID, item.Processing)
		}
	}
	if items[0].ID != imported && items[1].ID != imported {
		t.Errorf("the list %+v lacks the imported message %d", items, imported)
	}

	children := childProcesses(t, server.cmd.Process.Pid)
	workers := 0
	for _, child := range children {
		if len(child.args) == 2 && child.args[1] == "parse-worker" {
			workers++
		}
	}
	// Two for processing and two for the message view.
	if workers != 4 {
		t.Fatalf("the server's children are %+v, want four parse workers", children)
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
