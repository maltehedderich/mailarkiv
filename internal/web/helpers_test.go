package web

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"
)

const goodPassword = "correct horse battery"

func (h *harness) createAccount(t *testing.T, address string) {
	t.Helper()
	if err := h.identity.CreateAccount(context.Background(), address, goodPassword); err != nil {
		t.Fatalf("create %s: %v", address, err)
	}
}

// sessionCookie matches the contract's Set-Cookie value and captures the token.
var sessionCookie = regexp.MustCompile(`^__Host-mailarkiv_session=([A-Za-z0-9_-]+); Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Strict$`)

// signIn signs in over HTTP and returns a function that sends the session
// cookie by hand.
func (h *harness) signIn(t *testing.T, address string) func(*http.Request) {
	t.Helper()
	return withSession(h.signInToken(t, address))
}

// signInToken signs in over HTTP and returns the session token.
func (h *harness) signInToken(t *testing.T, address string) string {
	t.Helper()
	body := `{"email":` + quote(address) + `,"password":` + quote(goodPassword) + `}`
	response, _ := h.send(t, http.MethodPost, "/api/session", strings.NewReader(body), nil)
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("sign in as %s: status %d, want 204", address, response.StatusCode)
	}
	match := sessionCookie.FindStringSubmatch(response.Header.Get("Set-Cookie"))
	if match == nil {
		t.Fatalf("Set-Cookie = %q", response.Header.Get("Set-Cookie"))
	}
	return match[1]
}

// archiveCounts is what the archive holds: catalog rows and stored files.
type archiveCounts struct {
	messages     int
	observations int
	blobs        int
}

func (h *harness) counts(t *testing.T) archiveCounts {
	t.Helper()
	var c archiveCounts
	for query, into := range map[string]*int{
		"SELECT COUNT(*) FROM message":            &c.messages,
		"SELECT COUNT(*) FROM source_observation": &c.observations,
	} {
		if err := h.db.Reader.QueryRowContext(context.Background(), query).Scan(into); err != nil {
			t.Fatal(err)
		}
	}
	err := filepath.WalkDir(filepath.Join(h.dataDir, "blobs"), func(_ string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Type().IsRegular() {
			c.blobs++
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func withSession(token string) func(*http.Request) {
	return func(request *http.Request) {
		request.Header.Set("Cookie", "__Host-mailarkiv_session="+token)
	}
}

// both applies each change in order.
func both(changes ...func(*http.Request)) func(*http.Request) {
	return func(request *http.Request) {
		for _, change := range changes {
			change(request)
		}
	}
}

func withFileName(encoded string) func(*http.Request) {
	return func(request *http.Request) {
		request.Header.Set("MailArkiv-File-Name", encoded)
	}
}

func quote(s string) string {
	encoded, err := json.Marshal(s)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// assertJSONResponse checks the status, that the body is JSON, and that it
// holds exactly the values in want, whatever the key order.
func assertJSONResponse(t *testing.T, response *http.Response, body string, status int, want string) {
	t.Helper()
	if response.StatusCode != status {
		t.Fatalf("status = %d, want %d; body %s", response.StatusCode, status, body)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	if got := response.Header.Get("MailArkiv-Response"); got != "1" {
		t.Errorf("MailArkiv-Response = %q, want 1", got)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal([]byte(body), &gotValue); err != nil {
		t.Fatalf("body isn't JSON: %q: %v", body, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("the test's want isn't JSON: %q: %v", want, err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("body = %s\nwant   %s", body, want)
	}
}

func hashOf(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// mail is a small synthetic message.
func mail(subject string) []byte {
	return []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: " + subject + "\r\n\r\nBody.\r\n")
}

// generated is a mail-shaped reader of exactly size bytes, so a 150 MB body
// never sits in memory.
type generated struct {
	size    int64
	offset  int64
	pattern []byte
}

func newGenerated(size int64) *generated {
	return &generated{size: size, pattern: []byte("Subject: generated\r\n\r\n0123456789abcdefghijklmnopqrstuvwxyz\r\n")}
}

func (g *generated) Read(p []byte) (int, error) {
	if g.offset >= g.size {
		return 0, io.EOF
	}
	n := int64(len(p))
	if remaining := g.size - g.offset; n > remaining {
		n = remaining
	}
	for i := range n {
		p[i] = g.pattern[(g.offset+i)%int64(len(g.pattern))]
	}
	g.offset += n
	return int(n), nil
}

// blobFile is where the stored original with this hash lives.
func (h *harness) blobFile(hash string) string {
	return filepath.Join(h.dataDir, "blobs", hash[0:2], hash[2:4], hash)
}

// writeBlob puts bytes at the hash's blob path, the way an earlier crash or a
// damaged disk would leave them.
func (h *harness) writeBlob(t *testing.T, hash string, body []byte) {
	t.Helper()
	path := h.blobFile(hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

// makeBlobUnreadable leaves a link to itself at the hash's blob path. Opening it
// fails for every user, root included, and a rename replaces it.
func (h *harness) makeBlobUnreadable(t *testing.T, hash string) {
	t.Helper()
	path := h.blobFile(hash)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	if err := os.Symlink(path, path); err != nil {
		t.Fatal(err)
	}
}

// tempFileCount counts what's in tmp/.
func (h *harness) tempFileCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(h.dataDir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

// waitFor polls until the condition holds, and fails the test at the deadline.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("%s didn't happen within 10 seconds", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
