package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Each marker sits in one place that mail content or an address could leak
// from. They're lowercase, so a check on a lowercased haystack also catches a
// copy whose case changed on the way.
const (
	// The account signs in and imports. Lowercase is the stored form.
	markerAccount = "accountmarkerq7z1@example.test"
	// A failed sign-in submits this address, which has no account.
	markerSignIn       = "signinmarkerw8j4@example.test"
	markerWrongPass    = "passwordmarkerr1n5"
	markerSubject      = "subjectmarkerx9v3"
	markerFrom         = "frommarkerh2d6"
	markerTo           = "tomarkerm7c1"
	markerBody         = "bodymarkerz4p8"
	markerAttachment   = "attachmentmarkert6y2"
	markerFileName     = "filenamemarkerb3g9"
	markerFileNameFull = markerFileName + ".eml"
)

var markers = []string{
	markerAccount, markerSignIn, markerWrongPass, markerSubject, markerFrom,
	markerTo, markerBody, markerAttachment, markerFileName,
}

// markerMail is a synthetic message with a marker in its subject, both
// addresses, its body, and its attachment's name. n makes the bytes differ, so
// each import is new to the archive.
func markerMail(n int) []byte {
	return []byte(strings.Join([]string{
		"From: Sender <" + markerFrom + "@example.test>",
		"To: " + markerTo + "@example.test",
		fmt.Sprintf("Subject: %s %d", markerSubject, n),
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="b"`,
		"",
		"--b",
		"Content-Type: text/plain; charset=utf-8",
		"",
		markerBody,
		"--b",
		"Content-Type: application/pdf",
		`Content-Disposition: attachment; filename="` + markerAttachment + `.pdf"`,
		"Content-Transfer-Encoding: base64",
		"",
		"JVBERi0xLjQK",
		"--b--",
		"",
	}, "\r\n"))
}

// importNamed imports content with the marker file name, as the web interface
// sends it.
func importNamed(t *testing.T, baseURL, token string, content []byte) importAttempt {
	t.Helper()
	request, err := http.NewRequest(http.MethodPost, baseURL+"/api/imports", bytes.NewReader(content))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	request.Header.Set("MailArkiv-File-Name", url.PathEscape(markerFileNameFull))
	response, err := processClient.Do(request)
	if err != nil {
		return importAttempt{err: err}
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	return importAttempt{status: response.StatusCode, body: string(read), err: err}
}

// importFailing imports one new marker message and expects it to fail with the
// status the contract gives this category.
func importFailing(n, status int) func(t *testing.T, server *serverProcess, token, dataDir string) {
	return func(t *testing.T, server *serverProcess, token, _ string) {
		t.Helper()
		attempt := importNamed(t, server.baseURL, token, markerMail(n))
		if attempt.err != nil || attempt.status != status {
			t.Fatalf("the import = %d %s %v, want %d", attempt.status, attempt.body, attempt.err, status)
		}
	}
}

// Every failure category reaches the status and the log with its time, and
// neither holds mail content, an account address, a password, or a session
// token.
func TestNoMarkersInLogOrStatus(t *testing.T) {
	dataDir := t.TempDir()
	if code, _, stderr := runAccount(t, accountPassword+"\n", "account", "create", "--data-dir", dataDir, markerAccount); code != 0 {
		t.Fatalf("account create = %d: %s", code, stderr)
	}

	// The faults setting is fixed for a process, and one setting can't force
	// both storage categories at the same point, so each run gets its own
	// start on the same data directory. The status covers only one start, so
	// each run checks its own categories.
	runs := []struct {
		faults string
		seed   func(t *testing.T, dataDir string)
		force  func(t *testing.T, server *serverProcess, token, dataDir string)
		want   []string
	}{
		{
			faults: "temp-remove=EACCES",
			seed:   func(t *testing.T, dataDir string) { seedTempFile(t, dataDir) },
			force:  forceStoredOriginalFailures,
			want:   []string{"temp_file_not_removed", "stored_original_missing", "stored_original_damaged"},
		},
		{faults: "temp-write=ENOSPC", force: importFailing(3, http.StatusInsufficientStorage), want: []string{"storage_full"}},
		{faults: "temp-write=EACCES", force: importFailing(4, http.StatusInternalServerError), want: []string{"storage_write_failed"}},
		{faults: "commit=EIO", force: importFailing(5, http.StatusInternalServerError), want: []string{"catalog_write_failed"}},
	}

	for _, run := range runs {
		t.Run(run.faults, func(t *testing.T) {
			if run.seed != nil {
				run.seed(t, dataDir)
			}
			started := time.Now().Truncate(time.Millisecond)
			server := startFaultServer(t, dataDir, run.faults)
			token := signInOverHTTP(t, server.baseURL, markerAccount)

			// Submitted sign-in addresses must also stay out of logs.
			if status, _ := attemptSignIn(server, markerSignIn, markerWrongPass); status != http.StatusUnauthorized {
				t.Fatalf("a failed sign-in = %d, want 401", status)
			}

			run.force(t, server, token, dataDir)

			response, statusBody := server.request(http.MethodGet, "/api/status", "", token)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/status = %d %s, want 200", response.StatusCode, statusBody)
			}
			var status statusAnswer
			if err := json.Unmarshal([]byte(statusBody), &status); err != nil {
				t.Fatalf("the status isn't JSON: %q: %v", statusBody, err)
			}
			finished := time.Now()

			var categories []string
			for _, failure := range status.Failures {
				categories = append(categories, failure.Category)
				at, err := time.Parse(time.RFC3339Nano, failure.At)
				if err != nil || at.Before(started) || at.After(finished) {
					t.Errorf("%s at %q, want a time between %s and %s", failure.Category, failure.At, started, finished)
				}
			}
			if !slices.Equal(categories, run.want) {
				t.Errorf("status failures = %v, want %v", categories, run.want)
			}

			// The count is failure events plus unreferenced originals.
			response, sessionBody := server.request(http.MethodGet, "/api/session", "", token)
			wantCount := fmt.Sprintf(`{"problem_count":%d}`, len(run.want)+len(status.Unreferenced))
			if response.StatusCode != http.StatusOK || sessionBody != wantCount {
				t.Errorf("GET /api/session = %d %s, want %s", response.StatusCode, sessionBody, wantCount)
			}

			if code := server.stop(syscall.SIGTERM); code != 0 {
				t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
			}
			stderr := server.stderr()
			var logged []string
			for _, entry := range logEntries(t, stderr) {
				category, ok := entry["category"].(string)
				if !ok {
					continue
				}
				logged = append(logged, category)
				if at, ok := entry["time"].(string); !ok || at == "" {
					t.Errorf("the %s log line has no time: %v", category, entry)
				}
			}
			if !slices.Equal(logged, run.want) {
				t.Errorf("logged categories = %v, want %v:\n%s", logged, run.want, stderr)
			}

			secrets := append(slices.Clone(markers), accountPassword, token)
			for _, secret := range secrets {
				if strings.Contains(strings.ToLower(stderr), strings.ToLower(secret)) {
					t.Errorf("the log holds %q:\n%s", secret, stderr)
				}
				if strings.Contains(strings.ToLower(statusBody), strings.ToLower(secret)) {
					t.Errorf("the status holds %q: %s", secret, statusBody)
				}
			}
		})
	}
}

// forceStoredOriginalFailures imports two marker messages, deletes the first's
// stored original and edits the second's, then downloads both.
func forceStoredOriginalFailures(t *testing.T, server *serverProcess, token, dataDir string) {
	t.Helper()
	cases := []struct {
		change func(path string) error
		status int
	}{
		{change: os.Remove, status: http.StatusNotFound},
		{change: func(path string) error {
			return os.WriteFile(path, []byte("Subject: not the original\r\n\r\n"), 0o600)
		}, status: http.StatusInternalServerError},
	}
	for n, tc := range cases {
		content := markerMail(n + 1)
		attempt := importNamed(t, server.baseURL, token, content)
		if attempt.err != nil || attempt.status != http.StatusCreated {
			t.Fatalf("the import = %d %s %v, want 201", attempt.status, attempt.body, attempt.err)
		}
		id := parseImportAnswer(t, attempt.body).Message.ID
		if err := tc.change(blobPath(dataDir, contentHash(content))); err != nil {
			t.Fatal(err)
		}
		response, _ := server.request(http.MethodGet, fmt.Sprintf("/api/messages/%d/original", id), "", token)
		if response.StatusCode != tc.status {
			t.Fatalf("the download = %d, want %d", response.StatusCode, tc.status)
		}
	}
}
