package imaptest_test

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// quiet is how long a test waits to see that the server sends nothing. A
// server that answers late passes, but one that answers at all fails.
const quiet = 200 * time.Millisecond

func newDoubleWith(t *testing.T, options imaptest.Options) *imaptest.Server {
	t.Helper()
	options.User, options.Password = user, password
	server, err := imaptest.New(options)
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

// unclosed starts a server that the test closes itself.
func unclosed(t *testing.T, security imaptest.Security) *imaptest.Server {
	t.Helper()
	server, err := imaptest.New(imaptest.Options{Security: security, User: user, Password: password})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return server
}

// raw speaks IMAP by hand, so a test sees each byte the server sends and
// can tell when it sends nothing.
type raw struct {
	t      *testing.T
	conn   net.Conn
	reader *bufio.Reader
}

func tlsConfig(t *testing.T, server *imaptest.Server) *tls.Config {
	t.Helper()
	return &tls.Config{RootCAs: trusting(t, server), ServerName: "127.0.0.1", MinVersion: tls.VersionTLS12}
}

// connect opens a connection without TLS or a greeting.
func connect(t *testing.T, server *imaptest.Server) net.Conn {
	t.Helper()
	conn, err := net.Dial("tcp", server.Addr())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func newRaw(t *testing.T, conn net.Conn) *raw {
	return &raw{t: t, conn: conn, reader: bufio.NewReader(conn)}
}

// dialRaw connects, reads the greeting, and has TLS running.
func dialRaw(t *testing.T, server *imaptest.Server, security imaptest.Security, config *tls.Config) *raw {
	t.Helper()
	plain := connect(t, server)
	if security == imaptest.StartTLS {
		r := newRaw(t, plain)
		r.line()
		r.command("s", "STARTTLS")
	}
	encrypted := tls.Client(plain, config)
	if err := encrypted.Handshake(); err != nil {
		t.Fatalf("handshake: %v", err)
	}
	r := newRaw(t, encrypted)
	if security == imaptest.ImplicitTLS {
		r.line()
	}
	return r
}

func signInRaw(t *testing.T, server *imaptest.Server, security imaptest.Security) *raw {
	t.Helper()
	r := dialRaw(t, server, security, tlsConfig(t, server))
	r.command("l", fmt.Sprintf("LOGIN %s %s", user, password))
	return r
}

func (r *raw) send(tag, command string) {
	r.t.Helper()
	if _, err := fmt.Fprintf(r.conn, "%s %s\r\n", tag, command); err != nil {
		r.t.Fatal(err)
	}
}

func (r *raw) line() string {
	r.t.Helper()
	if err := r.conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		r.t.Fatal(err)
	}
	line, err := r.reader.ReadString('\n')
	if err != nil {
		r.t.Fatalf("read: %v", err)
	}
	return line
}

// command sends a command and returns the lines before its tagged OK.
func (r *raw) command(tag, command string) []string {
	r.t.Helper()
	r.send(tag, command)
	var untagged []string
	for {
		line := r.line()
		if strings.HasPrefix(line, tag+" ") {
			if !strings.HasPrefix(line, tag+" OK") {
				r.t.Fatalf("%s: %q", command, line)
			}
			return untagged
		}
		untagged = append(untagged, line)
	}
}

// assertSilent fails if the server sends a byte within quiet.
func (r *raw) assertSilent() {
	r.t.Helper()
	if err := r.conn.SetReadDeadline(time.Now().Add(quiet)); err != nil {
		r.t.Fatal(err)
	}
	if b, err := r.reader.ReadByte(); !isTimeout(err) {
		r.t.Fatalf("read %q, %v, want nothing", b, err)
	}
}

// assertEnded fails unless the connection ends within 5 seconds.
func assertEnded(t *testing.T, conn net.Conn) {
	t.Helper()
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, conn); isTimeout(err) {
		t.Fatal("the connection is still open")
	}
}

// stalledHandshake fails unless the handshake goes unanswered for quiet.
func stalledHandshake(t *testing.T, conn *tls.Conn) {
	t.Helper()
	if err := conn.SetDeadline(time.Now().Add(quiet)); err != nil {
		t.Fatal(err)
	}
	if err := conn.Handshake(); !isTimeout(err) {
		t.Fatalf("handshake: %v, want a timeout", err)
	}
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func TestStallCommand(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, tt.security)
			server.SetHook(func(command imaptest.Command) error {
				if command.Session == 1 && command.Name == "EXAMINE" {
					return imaptest.ErrStall
				}
				return nil
			})
			stalled := signInRaw(t, server, tt.security)
			stalled.send("e", "EXAMINE INBOX")
			stalled.assertSilent()

			// Other sessions go on.
			other := signIn(t, server, tt.security)
			if _, err := other.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
				t.Fatal(err)
			}
			stalled.assertSilent()
		})
	}
}

func TestCloseEndsStalls(t *testing.T) {
	stalls := []struct {
		name  string
		stall func(*testing.T, *imaptest.Server) net.Conn
	}{
		{"a command", func(t *testing.T, server *imaptest.Server) net.Conn {
			server.SetHook(func(command imaptest.Command) error {
				if command.Name == "EXAMINE" {
					return imaptest.ErrStall
				}
				return nil
			})
			r := signInRaw(t, server, imaptest.ImplicitTLS)
			r.send("e", "EXAMINE INBOX")
			r.assertSilent()
			return r.conn
		}},
		{"a literal", func(t *testing.T, server *imaptest.Server) net.Conn {
			withThreeMessages(t, server)
			if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
				t.Fatal(err)
			}
			r := signInRaw(t, server, imaptest.ImplicitTLS)
			r.command("e", "EXAMINE INBOX")
			r.send("f", "UID FETCH 2 (BODY.PEEK[])")
			r.line()
			return r.conn
		}},
		{"the greeting", func(t *testing.T, server *imaptest.Server) net.Conn {
			server.WithholdGreeting(true)
			conn := tls.Client(connect(t, server), tlsConfig(t, server))
			if err := conn.Handshake(); err != nil {
				t.Fatal(err)
			}
			newRaw(t, conn).assertSilent()
			return conn
		}},
		{"the handshake", func(t *testing.T, server *imaptest.Server) net.Conn {
			server.StallHandshake(true)
			conn := connect(t, server)
			stalledHandshake(t, tls.Client(conn, tlsConfig(t, server)))
			return conn
		}},
	}
	for _, tt := range stalls {
		t.Run(tt.name, func(t *testing.T) {
			server := unclosed(t, imaptest.ImplicitTLS)
			conn := tt.stall(t, server)
			if err := server.Close(); err != nil {
				t.Fatalf("Close: %v", err)
			}
			assertEnded(t, conn)
		})
	}
}

func TestHangUp(t *testing.T) {
	server := newDouble(t, imaptest.StartTLS)
	server.SetHook(func(command imaptest.Command) error {
		if command.Session == 1 && command.Name == "EXAMINE" {
			return imaptest.ErrHangUp
		}
		return nil
	})
	client := signIn(t, server, imaptest.StartTLS)
	_, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	if err == nil || errors.As(err, new(*imap.Error)) {
		t.Fatalf("EXAMINE: %v, want the connection closed", err)
	}
	if _, err := signIn(t, server, imaptest.StartTLS).Select("INBOX", nil).Wait(); err != nil {
		t.Errorf("SELECT in a new session: %v", err)
	}
}

func TestBad(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	server.SetHook(func(command imaptest.Command) error {
		if command.Name == "EXAMINE" {
			return imaptest.Bad(imap.ResponseCodeServerBug, "the index is broken")
		}
		return nil
	})
	client := signIn(t, server, imaptest.ImplicitTLS)
	_, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait()
	assertResponse(t, err, imap.StatusResponseTypeBad, imap.ResponseCodeServerBug)
	var imapErr *imap.Error
	if errors.As(err, &imapErr) && imapErr.Text != "the index is broken" {
		t.Errorf("text = %q, want the hook's", imapErr.Text)
	}
}

func TestWithholdGreeting(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, tt.security)
			server.WithholdGreeting(true)
			conn := connect(t, server)
			if tt.security == imaptest.ImplicitTLS {
				encrypted := tls.Client(conn, tlsConfig(t, server))
				if err := encrypted.Handshake(); err != nil {
					t.Fatalf("the handshake before a withheld greeting: %v", err)
				}
				conn = encrypted
			}
			newRaw(t, conn).assertSilent()

			server.WithholdGreeting(false)
			signIn(t, server, tt.security)
		})
	}
}

func TestStallHandshake(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, tt.security)
			server.StallHandshake(true)
			conn := connect(t, server)
			if tt.security == imaptest.StartTLS {
				r := newRaw(t, conn)
				r.line()
				r.command("s", "STARTTLS")
			}
			stalledHandshake(t, tls.Client(conn, tlsConfig(t, server)))

			server.StallHandshake(false)
			signIn(t, server, tt.security)
		})
	}
}

func TestBodyStalled(t *testing.T) {
	server := newDouble(t, imaptest.StartTLS)
	withThreeMessages(t, server)
	if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
		t.Fatal(err)
	}
	r := signInRaw(t, server, imaptest.StartTLS)
	r.command("e", "EXAMINE INBOX")
	r.send("f", "UID FETCH 2 (BODY.PEEK[])")

	body := faultBodies[1]
	want := fmt.Sprintf("* 2 FETCH (UID 2 BODY[] {%d}\r\n", len(body))
	if line := r.line(); line != want {
		t.Fatalf("line %q, want %q", line, want)
	}
	half := make([]byte, len(body)/2)
	if _, err := io.ReadFull(r.reader, half); err != nil {
		t.Fatal(err)
	}
	if string(half) != string(body[:len(body)/2]) {
		t.Errorf("sent %q, want the first half of the body", half)
	}
	r.assertSilent()
}

func TestFloodList(t *testing.T) {
	const size = 64 << 10
	server := newDouble(t, imaptest.ImplicitTLS)
	server.FloodList("EXAMINE", size)
	r := signInRaw(t, server, imaptest.ImplicitTLS)

	var listed, bytes int
	for _, line := range r.command("e", "EXAMINE INBOX") {
		if strings.HasPrefix(line, "* LIST ") {
			listed++
			bytes += len(line)
		}
	}
	if bytes < size {
		t.Errorf("%d LIST replies of %d bytes, want at least %d bytes", listed, bytes, size)
	}

	// The client drops LIST replies it didn't ask for.
	client := signIn(t, server, imaptest.ImplicitTLS)
	if _, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Errorf("EXAMINE through a flood: %v", err)
	}

	server.FloodList("EXAMINE", 0)
	for _, line := range r.command("e", "EXAMINE INBOX") {
		if strings.HasPrefix(line, "* LIST ") {
			t.Fatalf("a LIST reply after the flood stopped: %q", line)
		}
	}
}

func TestCertificates(t *testing.T) {
	kinds := []struct {
		name        string
		certificate imaptest.Certificate
		failed      func(error) bool
	}{
		{"another CA", imaptest.OtherCA, func(err error) bool {
			return errors.As(err, new(x509.UnknownAuthorityError))
		}},
		{"expired", imaptest.Expired, func(err error) bool {
			var invalid x509.CertificateInvalidError
			return errors.As(err, &invalid) && invalid.Reason == x509.Expired
		}},
		{"another name", imaptest.OtherName, func(err error) bool {
			return errors.As(err, new(x509.HostnameError))
		}},
	}
	for _, kind := range kinds {
		for _, tt := range securities {
			t.Run(kind.name+"/"+tt.name, func(t *testing.T) {
				server := newDoubleWith(t, imaptest.Options{Security: tt.security, Certificate: kind.certificate})
				client, err := dial(server, tt.security, trusting(t, server))
				if err == nil {
					client.Close()
					t.Fatal("connected")
				}
				if !kind.failed(err) {
					t.Errorf("error = %v", err)
				}
				if logins := server.Logins(); logins != 0 {
					t.Errorf("the double saw %d LOGIN commands", logins)
				}
			})

			// A server that presented a trusted certificate presents the new
			// one from the next handshake.
			t.Run(kind.name+" set later/"+tt.name, func(t *testing.T) {
				server := newDoubleWith(t, imaptest.Options{Security: tt.security})
				client, err := dial(server, tt.security, trusting(t, server))
				if err != nil {
					t.Fatalf("the trusted certificate failed: %v", err)
				}
				client.Close()
				if err := server.SetCertificate(kind.certificate); err != nil {
					t.Fatal(err)
				}
				client, err = dial(server, tt.security, trusting(t, server))
				if err == nil {
					client.Close()
					t.Fatal("connected")
				}
				if !kind.failed(err) {
					t.Errorf("error = %v", err)
				}
			})
		}
	}
}

func TestOnlyTLS11(t *testing.T) {
	for _, tt := range securities {
		t.Run(tt.name, func(t *testing.T) {
			server := newDoubleWith(t, imaptest.Options{Security: tt.security, OnlyTLS11: true})
			if client, err := dial(server, tt.security, trusting(t, server)); err == nil {
				client.Close()
				t.Fatal("a client that wants TLS 1.2 connected")
			}

			config := tlsConfig(t, server)
			config.MinVersion, config.MaxVersion = tls.VersionTLS11, tls.VersionTLS11
			r := dialRaw(t, server, tt.security, config)
			if version := r.conn.(*tls.Conn).ConnectionState().Version; version != tls.VersionTLS11 {
				t.Errorf("version = %s, want TLS 1.1", tls.VersionName(version))
			}
			r.command("l", fmt.Sprintf("LOGIN %s %s", user, password))
		})
	}
}

func TestNoStartTLS(t *testing.T) {
	server := newDoubleWith(t, imaptest.Options{Security: imaptest.StartTLS, NoStartTLS: true})

	client := imapclient.New(connect(t, server), nil)
	defer client.Close()
	caps, err := client.Capability().Wait()
	if err != nil {
		t.Fatal(err)
	}
	if caps.Has(imap.CapStartTLS) {
		t.Errorf("capabilities %v name STARTTLS", caps)
	}

	if client, err := dial(server, imaptest.StartTLS, trusting(t, server)); err == nil {
		client.Close()
		t.Fatal("STARTTLS succeeded")
	} else {
		assertResponse(t, err, imap.StatusResponseTypeNo, "")
	}
}

func TestStopAccepting(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	client := signIn(t, server, imaptest.ImplicitTLS)
	if err := server.StopAccepting(); err != nil {
		t.Fatal(err)
	}
	if conn, err := net.Dial("tcp", server.Addr()); !errors.Is(err, syscall.ECONNREFUSED) {
		if err == nil {
			conn.Close()
		}
		t.Fatalf("dial: %v, want connection refused", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		t.Errorf("SELECT in the open session: %v", err)
	}
}

func TestLogins(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	signIn(t, server, imaptest.ImplicitTLS)
	signIn(t, server, imaptest.ImplicitTLS)
	client, err := dial(server, imaptest.ImplicitTLS, trusting(t, server))
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	if err := client.Login(user, "wrong").Wait(); err == nil {
		t.Fatal("LOGIN with the wrong password succeeded")
	}
	if got := server.Logins(); got != 3 {
		t.Errorf("Logins = %d, want 3", got)
	}
}
