package imaptest_test

import (
	"bytes"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"net"
	"os"
	"path/filepath"
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

const (
	user     = "anna@example.test"
	password = "secret"
)

var securities = []struct {
	name     string
	security imaptest.Security
}{
	{"TLS", imaptest.ImplicitTLS},
	{"STARTTLS", imaptest.StartTLS},
}

func newDouble(t *testing.T, security imaptest.Security) *imaptest.Server {
	t.Helper()
	server, err := imaptest.New(imaptest.Options{Security: security, User: user, Password: password})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() {
		if err := server.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return server
}

// dial connects the way collection does: to the address's host, trusting
// only the roots in pool.
func dial(server *imaptest.Server, security imaptest.Security, pool *x509.CertPool) (*imapclient.Client, error) {
	host, _, err := net.SplitHostPort(server.Addr())
	if err != nil {
		return nil, err
	}
	options := &imapclient.Options{TLSConfig: &tls.Config{RootCAs: pool, ServerName: host, MinVersion: tls.VersionTLS12}}
	if security == imaptest.StartTLS {
		return imapclient.DialStartTLS(server.Addr(), options)
	}
	client, err := imapclient.DialTLS(server.Addr(), options)
	if err != nil {
		return nil, err
	}
	return client, client.WaitGreeting()
}

func trusting(t *testing.T, server *imaptest.Server) *x509.CertPool {
	t.Helper()
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(server.CAPEM()) {
		t.Fatal("CAPEM holds no certificate")
	}
	return pool
}

func signIn(t *testing.T, server *imaptest.Server, security imaptest.Security) *imapclient.Client {
	t.Helper()
	client, err := dial(server, security, trusting(t, server))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	t.Cleanup(func() { client.Close() })
	if err := client.Login(user, password).Wait(); err != nil {
		t.Fatalf("LOGIN: %v", err)
	}
	return client
}

func assertResponse(t *testing.T, err error, kind imap.StatusResponseType, code imap.ResponseCode) {
	t.Helper()
	var imapErr *imap.Error
	if !errors.As(err, &imapErr) || imapErr.Type != kind || imapErr.Code != code {
		t.Fatalf("error = %v, want %s [%s]", err, kind, code)
	}
}

func TestSignIn(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, tt.security)
			signIn(t, server, tt.security)

			client, err := dial(server, tt.security, trusting(t, server))
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			assertResponse(t, client.Login(user, "wrong").Wait(), imap.StatusResponseTypeNo, imap.ResponseCodeAuthenticationFailed)

			server.SetPassword("changed")
			assertResponse(t, client.Login(user, password).Wait(), imap.StatusResponseTypeNo, imap.ResponseCodeAuthenticationFailed)
			if err := client.Login(user, "changed").Wait(); err != nil {
				t.Errorf("LOGIN with the new password: %v", err)
			}
		})
	}
}

func TestCertificateTrust(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, tt.security)

			system, err := x509.SystemCertPool()
			if err != nil {
				t.Fatal(err)
			}
			client, err := dial(server, tt.security, system)
			if err == nil {
				client.Close()
				t.Fatal("a client trusting only the system's roots connected")
			}
			if !errors.As(err, new(x509.UnknownAuthorityError)) {
				t.Errorf("error = %v, want an unknown authority", err)
			}
			if logins := count(server.Commands(), "LOGIN"); logins != 0 {
				t.Errorf("the double saw %d LOGIN commands", logins)
			}

			path := filepath.Join(t.TempDir(), "ca.pem")
			if err := server.WriteCA(path); err != nil {
				t.Fatal(err)
			}
			written, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(written, server.CAPEM()) {
				t.Error("WriteCA wrote something other than CAPEM")
			}
		})
	}
}

func TestListDecodesFolderNames(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	for _, name := range []string{"Entwürfe", "Archiv"} {
		if err := server.AddFolder(name); err != nil {
			t.Fatal(err)
		}
	}
	client := signIn(t, server, imaptest.ImplicitTLS)

	listed, err := client.List("", "*", nil).Collect()
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, data := range listed {
		names = append(names, data.Mailbox)
	}
	slices.Sort(names)
	if want := []string{"Archiv", "Entwürfe", "INBOX"}; !slices.Equal(names, want) {
		t.Errorf("LIST = %q, want %q", names, want)
	}

	if err := server.DeleteFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Select("Archiv", &imap.SelectOptions{ReadOnly: true}).Wait(); err == nil {
		t.Error("EXAMINE of a deleted folder succeeded")
	}
}

func TestBodyPeekReturnsTheExactBytes(t *testing.T) {
	server := newDouble(t, imaptest.StartTLS)
	received := time.Date(2026, 11, 2, 9, 30, 15, 0, time.UTC)
	bodies := [][]byte{
		[]byte("From: ben@example.test\r\nSubject: CRLF\r\n\r\nBody\r\n"),
		[]byte("From: ben@example.test\r\nSubject: LF in the body\r\n\r\nLine one\nLine two\n"),
		[]byte("From: ben@example.test\r\nSubject: No final line break\r\n\r\nBody"),
	}
	var uids []imap.UID
	for _, body := range bodies {
		uid, err := server.AddMessage("INBOX", body, received)
		if err != nil {
			t.Fatal(err)
		}
		uids = append(uids, uid)
	}
	if want := []imap.UID{1, 2, 3}; !slices.Equal(uids, want) {
		t.Fatalf("UIDs = %v, want %v", uids, want)
	}
	before, err := server.Flags()
	if err != nil {
		t.Fatal(err)
	}

	client := signIn(t, server, imaptest.StartTLS)
	if _, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	fetched, err := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID:          true,
		RFC822Size:   true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched) != len(bodies) {
		t.Fatalf("fetched %d messages, want %d", len(fetched), len(bodies))
	}
	for _, message := range fetched {
		want := bodies[message.UID-1]
		if got := message.FindBodySection(&imap.FetchItemBodySection{Peek: true}); !bytes.Equal(got, want) {
			t.Errorf("UID %d body = %q, want %q", message.UID, got, want)
		}
		if message.RFC822Size != int64(len(want)) {
			t.Errorf("UID %d RFC822.SIZE = %d, want %d", message.UID, message.RFC822Size, len(want))
		}
		if !message.InternalDate.Equal(received) {
			t.Errorf("UID %d INTERNALDATE = %v, want %v", message.UID, message.InternalDate, received)
		}
	}

	after, err := server.Flags()
	if err != nil {
		t.Fatal(err)
	}
	if !equalFlags(before, after) {
		t.Errorf("flags changed from %v to %v", before, after)
	}
	if len(after["INBOX"]) != 3 {
		t.Errorf("the snapshot holds %v, want three messages in INBOX", after)
	}

	t.Run("a fetch without PEEK marks the message read", func(t *testing.T) {
		if _, err := client.Select("INBOX", nil).Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := client.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{BodySection: []*imap.FetchItemBodySection{{}}}).Collect(); err != nil {
			t.Fatal(err)
		}
		flags, err := server.Flags()
		if err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(flags["INBOX"][1], []imap.Flag{imap.FlagSeen}) || len(flags["INBOX"][2]) != 0 {
			t.Errorf("flags = %v, want \\Seen on UID 1 only", flags["INBOX"])
		}
	})

	t.Run("a deleted message is gone", func(t *testing.T) {
		if err := server.DeleteMessage("INBOX", 2); err != nil {
			t.Fatal(err)
		}
		flags, err := server.Flags()
		if err != nil {
			t.Fatal(err)
		}
		if _, found := flags["INBOX"][2]; found || len(flags["INBOX"]) != 2 {
			t.Errorf("INBOX holds %v after deleting UID 2", flags["INBOX"])
		}
		if err := server.DeleteMessage("INBOX", 2); err == nil {
			t.Error("deleting a missing UID succeeded")
		}
	})
}

// The in-memory server rewrites a header when it serves BODY[], so the
// double refuses a body it couldn't return byte for byte.
func TestUIDValidityMatchesExamine(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	if err := server.AddFolder("Entwürfe"); err != nil {
		t.Fatal(err)
	}
	client := signIn(t, server, imaptest.ImplicitTLS)

	for _, folder := range []string{"INBOX", "Entwürfe"} {
		selected, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait()
		if err != nil {
			t.Fatal(err)
		}
		got, err := server.UIDValidity(folder)
		if err != nil {
			t.Fatal(err)
		}
		if got == 0 || got != selected.UIDValidity {
			t.Errorf("UIDValidity(%q) = %d, want EXAMINE's %d", folder, got, selected.UIDValidity)
		}
	}
	if _, err := server.UIDValidity("Missing"); err == nil {
		t.Error("UIDValidity of a missing folder succeeded")
	}
}

func TestAddMessageRefusesBytesItWouldRewrite(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	for name, body := range map[string][]byte{
		"LF header": []byte("From: ben@example.test\nSubject: LF\n\nBody\n"),
		"no header": []byte("Body only\r\n"),
	} {
		if _, err := server.AddMessage("INBOX", body, time.Time{}); err == nil {
			t.Errorf("AddMessage accepted a body with %s", name)
		}
	}
	flags, err := server.Flags()
	if err != nil {
		t.Fatal(err)
	}
	if len(flags["INBOX"]) != 0 {
		t.Errorf("INBOX holds %v after only refused messages", flags["INBOX"])
	}
}

func equalFlags(a, b map[string]map[imap.UID][]imap.Flag) bool {
	if len(a) != len(b) {
		return false
	}
	for folder, messages := range a {
		if len(messages) != len(b[folder]) {
			return false
		}
		for uid, flags := range messages {
			other, found := b[folder][uid]
			if !found || !slices.Equal(flags, other) {
				return false
			}
		}
	}
	return true
}

func count(commands []imaptest.Command, name string) int {
	n := 0
	for _, command := range commands {
		if command.Name == name {
			n++
		}
	}
	return n
}

func TestCommandLogAndHook(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	if err := server.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.AddMessage("INBOX", []byte("Subject: Hi\r\n\r\nHi\r\n"), time.Time{}); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var seen []imaptest.Command
	server.SetHook(func(command imaptest.Command) error {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, command)
		if command.Name == "EXAMINE" && command.Mailbox == "Archiv" {
			return imaptest.No(imap.ResponseCodeUnavailable, "Archiv is busy")
		}
		return nil
	})

	first := signIn(t, server, imaptest.ImplicitTLS)
	if _, err := first.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.UIDSearch(&imap.SearchCriteria{}, nil).Wait(); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{UID: true}).Collect(); err != nil {
		t.Fatal(err)
	}
	_, err := first.Select("Archiv", &imap.SelectOptions{ReadOnly: true}).Wait()
	assertResponse(t, err, imap.StatusResponseTypeNo, imap.ResponseCodeUnavailable)

	second := signIn(t, server, imaptest.ImplicitTLS)
	if _, err := second.Select("INBOX", nil).Wait(); err != nil {
		t.Fatal(err)
	}

	want := []imaptest.Command{
		{Session: 1, Name: "LOGIN"},
		{Session: 1, Name: "EXAMINE", Mailbox: "INBOX"},
		{Session: 1, Name: "UID SEARCH"},
		{Session: 1, Name: "UID FETCH"},
		{Session: 1, Name: "EXAMINE", Mailbox: "Archiv"},
		{Session: 2, Name: "LOGIN"},
		{Session: 2, Name: "SELECT", Mailbox: "INBOX"},
	}
	if got := server.Commands(); !slices.Equal(got, want) {
		t.Errorf("log = %+v, want %+v", got, want)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(seen, want) {
		t.Errorf("the hook saw %+v, want %+v", seen, want)
	}
}

func TestOptionsAreChecked(t *testing.T) {
	for _, options := range []imaptest.Options{
		{User: user, Password: password},
		{Security: imaptest.ImplicitTLS, Password: password},
		{Security: imaptest.ImplicitTLS, User: user},
	} {
		if server, err := imaptest.New(options); err == nil {
			server.Close()
			t.Errorf("New(%+v) succeeded", options)
		}
	}
}
