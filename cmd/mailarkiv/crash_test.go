package main

import (
	"bytes"
	"fmt"
	"net/http"
	"slices"
	"testing"
)

const crashFileSize = 1 << 20

// crashFile is a synthetic message of 1 MiB. Each one differs, so no row can
// depend on bytes another row left behind.
func crashFile(point string) []byte {
	head := fmt.Sprintf("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Crash at %s\r\n\r\n", point)
	return append([]byte(head), bytes.Repeat([]byte("x"), crashFileSize-len(head))...)
}

// crashWithNoAnswer imports content on the faults binary with point set to
// kill. It fails unless the client gets no answer and SIGKILL ends the server.
// It returns the session token, which a restart keeps valid.
func crashWithNoAnswer(t *testing.T, dataDir, point string, content []byte) string {
	t.Helper()
	createAdmin(t, dataDir)
	server := startFaultServer(t, dataDir, point+"=kill")
	token := signInOverHTTP(t, server.baseURL, adminAddress)

	attempt := tryImport(server.baseURL, token, content)
	if attempt.err == nil {
		t.Fatalf("the import answered %d %s, want no answer, because %s stops the server", attempt.status, attempt.body, point)
	}
	server.waitForKill()
	return token
}

// restart starts the default binary on what a crash left. Recovery finishes
// before the server listens, so comparing the blobs around the start shows
// whether it deleted or added any (FR-024, risk 4).
func restart(t *testing.T, dataDir string) *serverProcess {
	t.Helper()
	before := blobFiles(t, dataDir)
	server := startServer(t, dataDir)
	if after := blobFiles(t, dataDir); !slices.Equal(before, after) {
		t.Fatalf("a restart changed blobs/ from %v to %v", before, after)
	}
	return server
}

// assertImported imports content on a running server and checks the outcome and
// the history size the answer reports.
func assertImported(t *testing.T, server *serverProcess, token string, content []byte, status int, outcome string, importCount int) {
	t.Helper()
	attempt := tryImport(server.baseURL, token, content)
	if attempt.err != nil {
		t.Fatalf("the import got no answer: %v", attempt.err)
	}
	if attempt.status != status {
		t.Fatalf("the import = %d %s, want %d", attempt.status, attempt.body, status)
	}
	answer := parseImportAnswer(t, attempt.body)
	if answer.Outcome != outcome {
		t.Errorf("outcome = %q, want %q", answer.Outcome, outcome)
	}
	if answer.Message == nil {
		t.Fatalf("the answer holds no message: %s", attempt.body)
	}
	if answer.Message.ContentHash != contentHash(content) || answer.Message.ImportCount != importCount {
		t.Errorf("message = %+v, want hash %s and %d imports", answer.Message, contentHash(content), importCount)
	}
}

func assertNoProblems(t *testing.T, status statusAnswer) {
	t.Helper()
	if len(status.Unreferenced) != 0 {
		t.Errorf("unreferenced = %+v, want none", status.Unreferenced)
	}
	if len(status.Failures) != 0 {
		t.Errorf("failures = %+v, want none", status.Failures)
	}
}

// The plan's crash matrix (FR-012, FR-013, FR-018, FR-024, FR-025, SC-003): the
// server stops at each capture step, and what the archive holds after a restart
// must match what the client was told.
func TestCrashMatrix(t *testing.T) {
	// User Story 3 scenario 2: capture stopped before the original was stored.
	beforeStored := []struct {
		point string
		// leftover is whether the stop leaves a temp file for recovery to remove.
		leftover int
	}{
		{"before-temp-create", 0},
		{"mid-temp-write", 1},
		{"after-temp-fsync", 1},
	}
	for _, tt := range beforeStored {
		t.Run(tt.point, func(t *testing.T) {
			dataDir := t.TempDir()
			token := crashWithNoAnswer(t, dataDir, tt.point, crashFile(tt.point))

			if left := tempFiles(t, dataDir); len(left) != tt.leftover {
				t.Fatalf("after the crash tmp/ holds %v, want %d files", left, tt.leftover)
			}
			if blobs := blobFiles(t, dataDir); len(blobs) != 0 {
				t.Fatalf("after the crash blobs/ holds %v, want nothing", blobs)
			}

			server := restart(t, dataDir)

			assertNoTempFiles(t, dataDir)
			server.assertNothingListed(token)
			status := server.status(token)
			assertNoProblems(t, status)
			if status.Recovery.TempFilesRemoved != tt.leftover || status.Recovery.TempFilesNotRemoved != 0 {
				t.Errorf("recovery = %+v, want %d temp files removed and none left", status.Recovery, tt.leftover)
			}
		})
	}

	// User Story 3 scenarios 3 and 4: the original is stored, with no catalog
	// entry and no confirmation.
	for _, point := range []string{"after-rename", "after-dir-fsync", "before-commit"} {
		t.Run(point, func(t *testing.T) {
			dataDir := t.TempDir()
			content := crashFile(point)
			token := crashWithNoAnswer(t, dataDir, point, content)
			assertOneBlob(t, dataDir, content)

			server := restart(t, dataDir)

			assertNoTempFiles(t, dataDir)
			assertOneBlob(t, dataDir, content)
			server.assertNothingListed(token)
			status := server.status(token)
			want := []unreferencedEntry{{ContentHash: contentHash(content), Size: crashFileSize}}
			if !slices.Equal(status.Unreferenced, want) {
				t.Errorf("unreferenced = %+v, want %+v", status.Unreferenced, want)
			}
			if status.Recovery.UnreferencedFound != 1 {
				t.Errorf("recovery found %d unreferenced originals, want 1", status.Recovery.UnreferencedFound)
			}

			assertImported(t, server, token, content, http.StatusCreated, "archived", 1)

			assertOneBlob(t, dataDir, content)
			server.assertListedOnce(token, content, 1)
			assertNoProblems(t, server.status(token))
			assertNoTempFiles(t, dataDir)
		})
	}

	// User Story 3 scenario 5: the entry is durable, and the answer never came.
	t.Run("after-commit", func(t *testing.T) {
		dataDir := t.TempDir()
		content := crashFile("after-commit")
		token := crashWithNoAnswer(t, dataDir, "after-commit", content)

		server := restart(t, dataDir)

		assertNoTempFiles(t, dataDir)
		assertOneBlob(t, dataDir, content)
		server.assertListedOnce(token, content, 1)
		assertNoProblems(t, server.status(token))

		assertImported(t, server, token, content, http.StatusOK, "already_archived", 2)

		server.assertListedOnce(token, content, 2)
		assertOneBlob(t, dataDir, content)
		assertNoProblems(t, server.status(token))
	})

	// User Story 3 scenario 1: a confirmation the client received survives.
	t.Run("after-response", func(t *testing.T) {
		dataDir := t.TempDir()
		content := crashFile("after-response")
		createAdmin(t, dataDir)
		crashing := startFaultServer(t, dataDir, "after-response=kill")
		token := signInOverHTTP(t, crashing.baseURL, adminAddress)

		attempt := tryImport(crashing.baseURL, token, content)

		// The whole answer must arrive, though the server dies right after sending it.
		if attempt.err != nil {
			t.Fatalf("the client didn't get the whole answer: %v", attempt.err)
		}
		if attempt.status != http.StatusCreated {
			t.Fatalf("the import = %d %s, want 201", attempt.status, attempt.body)
		}
		answer := parseImportAnswer(t, attempt.body)
		if answer.Outcome != "archived" || answer.Message == nil {
			t.Fatalf("the answer = %s, want archived with its message", attempt.body)
		}
		crashing.waitForKill()

		server := restart(t, dataDir)

		assertNoTempFiles(t, dataDir)
		message := server.assertListedOnce(token, content, 1)
		if message.ID != answer.Message.ID {
			t.Errorf("listed ID = %d, want the confirmed ID %d", message.ID, answer.Message.ID)
		}
		response, body := server.request(http.MethodGet, fmt.Sprintf("/api/messages/%d/original", message.ID), "", token)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("the download = %d, want 200", response.StatusCode)
		}
		if !bytes.Equal([]byte(body), content) {
			t.Errorf("the download differs from the imported bytes (%d bytes, want %d)", len(body), len(content))
		}
		assertNoProblems(t, server.status(token))
	})

	// FR-025: recovery stopped partway gives the same result when it runs again.
	t.Run("recovery-after-temp-removal", func(t *testing.T) {
		dataDir := t.TempDir()
		createAdmin(t, dataDir)
		content := crashFile("recovery-after-temp-removal")
		hash := seedBlob(t, dataDir, content)
		seedTempFile(t, dataDir)

		runFaultsUntilKilled(t, dataDir, "recovery-after-temp-removal=kill")

		// The stop came after the removal, so this run did reach that point.
		assertNoTempFiles(t, dataDir)
		assertOneBlob(t, dataDir, content)

		server := restart(t, dataDir)
		token := signInOverHTTP(t, server.baseURL, adminAddress)

		assertNoTempFiles(t, dataDir)
		assertOneBlob(t, dataDir, content)
		server.assertNothingListed(token)
		status := server.status(token)
		want := []unreferencedEntry{{ContentHash: hash, Size: crashFileSize}}
		if !slices.Equal(status.Unreferenced, want) {
			t.Errorf("unreferenced = %+v, want %+v", status.Unreferenced, want)
		}
		if len(status.Failures) != 0 {
			t.Errorf("failures = %+v, want none", status.Failures)
		}
	})
}
