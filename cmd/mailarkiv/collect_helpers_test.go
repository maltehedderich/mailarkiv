package main

import (
	"encoding/json"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// The mailbox the double serves to the binary.
const (
	mailboxUser     = "anna@example.test"
	mailboxPassword = "MARKER-PW-4f1c"
)

// newProcessDouble runs a mail server for mailboxUser in the test process and
// writes its CA to a file, for the binary's SSL_CERT_FILE.
func newProcessDouble(t *testing.T) (double *imaptest.Server, caFile string) {
	t.Helper()
	double = newMailServer(t, mailboxUser, mailboxPassword)
	return double, writeCAs(t, double)
}

// newMailServer runs a mail server in the test process for user, who signs
// in with password.
func newMailServer(t *testing.T, user, password string) *imaptest.Server {
	t.Helper()
	double, err := imaptest.New(imaptest.Options{Security: imaptest.ImplicitTLS, User: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := double.Close(); err != nil {
			t.Errorf("close the double: %v", err)
		}
	})
	return double
}

// writeCAs writes the CA of each double to one file, for the binary's
// SSL_CERT_FILE.
func writeCAs(t *testing.T, doubles ...*imaptest.Server) string {
	t.Helper()
	var pem []byte
	for _, double := range doubles {
		pem = append(pem, double.CAPEM()...)
	}
	caFile := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caFile, pem, 0o600); err != nil {
		t.Fatal(err)
	}
	return caFile
}

// createKey runs key create for dataDir with a path outside it, and returns
// the path.
func createKey(t *testing.T, dataDir string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", dataDir, path)
	if code != 0 || stderr != "" {
		t.Fatalf("key create = %d, stderr %q", code, stderr)
	}
	want := "Created the key at " + path + ". Back it up apart from the data directory. " +
		"Without it, every IMAP password must be saved again.\n"
	if stdout != want {
		t.Errorf("key create printed %q, want %q", stdout, want)
	}
	return path
}

// startCollecting runs the binary with the key and with the doubles' CAs as
// its only trusted roots. An empty keyPath gives no --key-file. preview adds
// --preview-collection.
func startCollecting(t *testing.T, dataDir, keyPath, caFile string, preview bool) *serverProcess {
	t.Helper()
	args := []string{"serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0"}
	if keyPath != "" {
		args = append(args, "--key-file", keyPath)
	}
	if preview {
		args = append(args, "--preview-collection")
	}
	cmd := exec.Command(serverBinary, args...)
	cmd.Env = append(os.Environ(), "SSL_CERT_FILE="+caFile)
	return startProcess(t, cmd)
}

// sourceRequest is a POST /api/sources body for the double's mailbox.
func sourceRequest(t *testing.T, double *imaptest.Server, folders ...string) string {
	t.Helper()
	return sourceRequestFor(t, double, "Anna", mailboxUser, mailboxPassword, folders...)
}

// sourceRequestFor is a POST /api/sources body named name, for user's mailbox
// on the double.
func sourceRequestFor(t *testing.T, double *imaptest.Server, name, user, password string, folders ...string) string {
	t.Helper()
	host, port, err := net.SplitHostPort(double.Addr())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]any{
		"name":      name,
		"host":      host,
		"port":      number,
		"security":  "tls",
		"user_name": user,
		"password":  password,
		"folders":   folders,
	})
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}

// addSource posts body to /api/sources and returns the new source's ID.
func (p *serverProcess) addSource(token, body string) int64 {
	p.t.Helper()
	response, answer := p.request(http.MethodPost, "/api/sources", body, token)
	if response.StatusCode != http.StatusCreated {
		p.t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, answer)
	}
	var added sourceAnswer
	if err := json.Unmarshal([]byte(answer), &added); err != nil {
		p.t.Fatalf("the source isn't JSON: %q: %v", answer, err)
	}
	return added.ID
}

// sourceAnswer is the part of a source the process tests read.
type sourceAnswer struct {
	ID          int64 `json:"id"`
	LastSuccess *struct {
		At       string `json:"at"`
		Archived int    `json:"archived"`
	} `json:"last_success"`
}

func (p *serverProcess) source(token string, id int64) sourceAnswer {
	p.t.Helper()
	response, body := p.request(http.MethodGet, "/api/sources/"+strconv.FormatInt(id, 10), "", token)
	if response.StatusCode != http.StatusOK {
		p.t.Fatalf("GET /api/sources/%d = %d %s, want 200", id, response.StatusCode, body)
	}
	var source sourceAnswer
	if err := json.Unmarshal([]byte(body), &source); err != nil {
		p.t.Fatalf("the source isn't JSON: %q: %v", body, err)
	}
	return source
}

// waitForSuccessAfter polls the source until its last success is set and
// differs from previous, which may be empty.
func (p *serverProcess) waitForSuccessAfter(token string, id int64, previous string) sourceAnswer {
	p.t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		source := p.source(token, id)
		if source.LastSuccess != nil && source.LastSuccess.At != previous {
			return source
		}
		if time.Now().After(deadline) {
			p.t.Fatalf("source %d had no new successful sync within %s:\n%s", id, processTimeout, p.stderr())
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func doubleLogins(double *imaptest.Server) int {
	var n int
	for _, command := range double.Commands() {
		if command.Name == "LOGIN" {
			n++
		}
	}
	return n
}
