package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
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

	t.Run("collection", testCollectionMarkers)
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

// Markers for collection. The mail is markerMail, with its own markers.
const (
	markerIMAPUser     = "usermarkerf3k8"
	markerIMAPAddress  = markerIMAPUser + "@example.test"
	markerIMAPPassword = "imappasswordmarkerv2q7"
	markerSourceName   = "sourcenamemarkerj6w3"
	markerFolder       = "foldermarkerc8n1"
	// The double's NO to LOGIN and BAD to UID SEARCH.
	markerReplyNo  = "noreplymarkerk9e5"
	markerReplyBad = "badreplymarkerd2u6"
)

// collectionMarkers must stay out of the status and the log. The status
// screen names each problem source, so the source name is checked in the
// log only.
var collectionMarkers = []string{
	markerIMAPUser, markerIMAPPassword, markerFolder, markerReplyNo, markerReplyBad,
	markerSubject, markerFrom, markerTo, markerBody, markerAttachment,
}

// markerMailbox is a data directory with an account and a key, and a double
// whose user name, password, folder, and mail hold markers. INBOX holds two
// messages and the marker folder one.
type markerMailbox struct {
	double  *imaptest.Server
	caFile  string
	dataDir string
	keyPath string
	inbox   []imap.UID
	// servers are the processes started on the data directory, in order.
	servers []*serverProcess
}

func newMarkerMailbox(t *testing.T) *markerMailbox {
	t.Helper()
	double := newMailServer(t, markerIMAPAddress, markerIMAPPassword)
	if err := double.AddFolder(markerFolder); err != nil {
		t.Fatal(err)
	}
	m := &markerMailbox{double: double, caFile: writeCAs(t, double), dataDir: filepath.Join(t.TempDir(), "data")}
	for i, folder := range []string{"INBOX", "INBOX", markerFolder} {
		uid, err := double.AddMessage(folder, markerMail(10+i), time.Date(2026, 11, 5, 9, i, 0, 0, time.UTC))
		if err != nil {
			t.Fatal(err)
		}
		if folder == "INBOX" {
			m.inbox = append(m.inbox, uid)
		}
	}
	m.keyPath = createKey(t, m.dataDir)
	createAdmin(t, m.dataDir)
	return m
}

// start runs the binary on the data directory with keyPath, or the faults
// build when faults isn't empty.
func (m *markerMailbox) start(t *testing.T, keyPath, faults string) *serverProcess {
	t.Helper()
	var server *serverProcess
	if faults == "" {
		server = startCollecting(t, m.dataDir, keyPath, m.caFile)
	} else {
		cmd := exec.Command(faultsBinary, "serve", "--data-dir", m.dataDir, "--listen", "127.0.0.1:0",
			"--key-file", keyPath)
		cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+m.caFile, "MAILARKIV_FAULTS="+faults)
		server = startProcess(t, cmd)
	}
	m.servers = append(m.servers, server)
	return server
}

// syncNow asks for a sync of the source at once.
func (p *serverProcess) syncNow(token string, id int64) {
	p.t.Helper()
	response, body := p.request(http.MethodPost, fmt.Sprintf("/api/sources/%d/sync", id), "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("POST /api/sources/%d/sync = %d %s, want 200", id, response.StatusCode, body)
	}
}

// forcer makes a category happen after the source's first sync, and returns
// the server and session to read from.
type forcer func(t *testing.T, m *markerMailbox, server *serverProcess, token string, id int64) (*serverProcess, string)

// syncFailing changes the double, asks for a sync, and waits until it fails
// with category.
func syncFailing(category string, change func(t *testing.T, m *markerMailbox)) forcer {
	return func(t *testing.T, m *markerMailbox, server *serverProcess, token string, id int64) (*serverProcess, string) {
		t.Helper()
		change(t, m)
		server.syncNow(token, id)
		// The connect limit alone is 30 seconds.
		server.waitForSyncFailureWithin(category, 2*processTimeout)
		return server, token
	}
}

// syncPassing changes the double, asks for a sync, and waits until it
// succeeds.
func syncPassing(change func(t *testing.T, m *markerMailbox)) forcer {
	return func(t *testing.T, m *markerMailbox, server *serverProcess, token string, id int64) (*serverProcess, string) {
		t.Helper()
		previous := server.source(token, id).LastSuccess.At
		change(t, m)
		server.syncNow(token, id)
		server.waitForSuccessAfter(token, id, previous)
		return server, token
	}
}

// restartWithAnotherKey stops the server and starts it with a new key.
func restartWithAnotherKey(t *testing.T, m *markerMailbox, server *serverProcess, _ string, _ int64) (*serverProcess, string) {
	t.Helper()
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}
	restarted := m.start(t, createKey(t, m.dataDir), "")
	_, token := attemptSignIn(restarted, adminAddress, accountPassword)
	return restarted, token
}

// faultInbox gives the first INBOX message a fault before the first sync.
func faultInbox(fault imaptest.Fault) func(t *testing.T, m *markerMailbox) {
	return func(t *testing.T, m *markerMailbox) {
		t.Helper()
		if err := m.double.SetFault("INBOX", m.inbox[0], fault); err != nil {
			t.Fatal(err)
		}
	}
}

// sourcePage is the part of GET /api/sources/{id} that shows categories.
type sourcePage struct {
	Failure *struct {
		Category string  `json:"category"`
		Reply    *string `json:"reply"`
	} `json:"failure"`
	Folders []struct {
		UnavailableSince *string `json:"unavailable_since"`
	} `json:"folders"`
	Messages map[string]struct {
		Items []struct {
			Category string `json:"category"`
		} `json:"items"`
	} `json:"messages"`
}

// categories lists what the page shows: the failure's category,
// folder_unavailable for each unavailable folder, and each listed message's
// category.
func (p sourcePage) categories() []string {
	var shown []string
	if p.Failure != nil {
		shown = append(shown, p.Failure.Category)
	}
	for _, folder := range p.Folders {
		if folder.UnavailableSince != nil {
			shown = append(shown, "folder_unavailable")
		}
	}
	for _, list := range p.Messages {
		for _, item := range list.Items {
			shown = append(shown, item.Category)
		}
	}
	return shown
}

// problemSourceAnswer is a source in GET /api/status.
type problemSourceAnswer struct {
	ID             int64  `json:"id"`
	Name           string `json:"name"`
	Category       string `json:"category"`
	Since          string `json:"since"`
	FailedAttempts *int   `json:"failed_attempts"`
}

// Each collection category reaches the source's page, the status, the
// problem count, and the log with the source's ID. The status and the log
// hold none of the mailbox's markers, and the server's reply reaches the
// source's page alone. Each case has its own data directory and double, so
// they run in parallel.
func testCollectionMarkers(t *testing.T) {
	cases := []struct {
		category string
		faults   string
		// prepare changes the double before the source is added.
		prepare func(t *testing.T, m *markerMailbox)
		force   forcer
		// event is set when the status lists a failure event, not the source.
		event bool
		// attempts is the failed attempts the status shows, with 0 for null.
		attempts int
		// reply is the marker in the server's reply, if it sent one.
		reply string
	}{
		{
			category: "server_not_reachable", attempts: 1,
			force: syncFailing("server_not_reachable", func(t *testing.T, m *markerMailbox) {
				if err := m.double.StopAccepting(); err != nil {
					t.Fatal(err)
				}
			}),
		},
		{
			category: "secure_connection_failed", attempts: 1,
			force: syncFailing("secure_connection_failed", func(t *testing.T, m *markerMailbox) {
				if err := m.double.SetCertificate(imaptest.OtherName); err != nil {
					t.Fatal(err)
				}
			}),
		},
		{
			category: "sign_in_rejected", attempts: 1, reply: markerReplyNo,
			force: syncFailing("sign_in_rejected", func(t *testing.T, m *markerMailbox) {
				m.double.SetPassword("changed-on-the-server")
				// The double words the rejection itself, with a marker.
				m.double.SetHook(func(command imaptest.Command) error {
					if command.Name == "LOGIN" {
						return imaptest.No(imap.ResponseCodeAuthenticationFailed, "Wrong password for "+markerReplyNo)
					}
					return nil
				})
			}),
		},
		{category: "password_unreadable", force: restartWithAnotherKey},
		{
			category: "timed_out", attempts: 1,
			force: syncFailing("timed_out", func(_ *testing.T, m *markerMailbox) { m.double.WithholdGreeting(true) }),
		},
		{
			category: "server_error", attempts: 1, reply: markerReplyBad,
			force: syncFailing("server_error", func(t *testing.T, m *markerMailbox) {
				// Only a folder with new mail is searched.
				if _, err := m.double.AddMessage("INBOX", markerMail(20), time.Time{}); err != nil {
					t.Fatal(err)
				}
				m.double.SetHook(func(command imaptest.Command) error {
					if command.Name == "UID SEARCH" {
						return imaptest.Bad("", "Search failed for "+markerReplyBad)
					}
					return nil
				})
			}),
		},
		{
			category: "folder_unavailable",
			force: syncPassing(func(t *testing.T, m *markerMailbox) {
				if err := m.double.DeleteFolder(markerFolder); err != nil {
					t.Fatal(err)
				}
			}),
		},
		{category: "storage_full", faults: "temp-write=ENOSPC", attempts: 1},
		{category: "storage_write_failed", faults: "temp-write=EACCES", attempts: 1},
		{category: "catalog_write_failed", faults: "commit=EIO", attempts: 1},
		{category: "message_unavailable", prepare: faultInbox(imaptest.Fault{Body: imaptest.BodyNil})},
		{category: "message_too_large", prepare: faultInbox(imaptest.Fault{Size: 150_000_001}), event: true},
		{
			category: "message_removed", event: true,
			prepare: faultInbox(imaptest.Fault{Body: imaptest.BodyNil}),
			force: syncPassing(func(t *testing.T, m *markerMailbox) {
				if err := m.double.DeleteMessage("INBOX", m.inbox[0]); err != nil {
					t.Fatal(err)
				}
			}),
		},
	}
	for _, tc := range cases {
		t.Run(tc.category, func(t *testing.T) {
			t.Parallel()
			m := newMarkerMailbox(t)
			if tc.prepare != nil {
				tc.prepare(t, m)
			}
			server := m.start(t, m.keyPath, tc.faults)
			_, token := attemptSignIn(server, adminAddress, accountPassword)
			id := server.addSource(token, sourceRequestFor(t, m.double, markerSourceName,
				markerIMAPAddress, markerIMAPPassword, "INBOX", markerFolder))
			if tc.faults != "" {
				server.waitForSyncFailure(tc.category)
			} else {
				server.waitForSuccessAfter(token, id, "")
			}
			if tc.force != nil {
				server, token = tc.force(t, m, server, token, id)
			}

			response, pageBody := server.request(http.MethodGet, fmt.Sprintf("/api/sources/%d", id), "", token)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/sources/%d = %d %s, want 200", id, response.StatusCode, pageBody)
			}
			var page sourcePage
			if err := json.Unmarshal([]byte(pageBody), &page); err != nil {
				t.Fatalf("the source isn't JSON: %q: %v", pageBody, err)
			}
			if !slices.Contains(page.categories(), tc.category) {
				t.Errorf("the source's page shows %v, want %s: %s", page.categories(), tc.category, pageBody)
			}
			if tc.reply != "" && (page.Failure == nil || page.Failure.Reply == nil || !strings.Contains(*page.Failure.Reply, tc.reply)) {
				t.Errorf("the source's page has no reply with %q: %s", tc.reply, pageBody)
			}

			response, statusBody := server.request(http.MethodGet, "/api/status", "", token)
			if response.StatusCode != http.StatusOK {
				t.Fatalf("GET /api/status = %d %s, want 200", response.StatusCode, statusBody)
			}
			var status struct {
				Sources  []problemSourceAnswer `json:"sources"`
				Failures []failureEntry        `json:"failures"`
			}
			if err := json.Unmarshal([]byte(statusBody), &status); err != nil {
				t.Fatalf("the status isn't JSON: %q: %v", statusBody, err)
			}
			if tc.event {
				if len(status.Sources) != 0 || len(status.Failures) != 1 || status.Failures[0].Category != tc.category {
					t.Errorf("status = %s, want one %s failure and no source", statusBody, tc.category)
				}
			} else {
				var attempts *int
				if tc.attempts != 0 {
					attempts = &tc.attempts
				}
				ok := len(status.Failures) == 0 && len(status.Sources) == 1
				if ok {
					got := status.Sources[0]
					ok = got.ID == id && got.Name == markerSourceName && got.Category == tc.category && got.Since != "" &&
						reflect.DeepEqual(got.FailedAttempts, attempts)
				}
				if !ok {
					t.Errorf("status = %s, want source %d alone with %s and failed attempts %v", statusBody, id, tc.category, attempts)
				}
			}

			response, sessionBody := server.request(http.MethodGet, "/api/session", "", token)
			if response.StatusCode != http.StatusOK || sessionBody != `{"problem_count":1}` {
				t.Errorf("GET /api/session = %d %s, want a problem count of 1", response.StatusCode, sessionBody)
			}

			if code := server.stop(syscall.SIGTERM); code != 0 {
				t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
			}
			var logs []string
			for _, started := range m.servers {
				logs = append(logs, started.stderr())
			}
			stderr := strings.Join(logs, "\n")
			logged := slices.ContainsFunc(logEntries(t, stderr), func(entry map[string]any) bool {
				at, _ := entry["time"].(string)
				return entry["category"] == tc.category && entry["source_id"] == float64(id) && at != ""
			})
			if !logged {
				t.Errorf("no log line has %s, source_id %d, and the time:\n%s", tc.category, id, stderr)
			}

			for _, marker := range append(slices.Clone(collectionMarkers), markerSourceName) {
				if strings.Contains(strings.ToLower(stderr), marker) {
					t.Errorf("the log holds %q:\n%s", marker, stderr)
				}
			}
			for _, marker := range collectionMarkers {
				if strings.Contains(strings.ToLower(statusBody), marker) {
					t.Errorf("the status holds %q: %s", marker, statusBody)
				}
			}
		})
	}
}
