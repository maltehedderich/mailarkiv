package collect

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

func TestTimeoutValues(t *testing.T) {
	got := productionConfig()
	if got.connect != 30*time.Second {
		t.Errorf("connect limit = %v, want 30s", got.connect)
	}
	if got.idle != 60*time.Second {
		t.Errorf("idle limit = %v, want 60s", got.idle)
	}
	if got.check != 120*time.Second {
		t.Errorf("check limit = %v, want 120s", got.check)
	}
}

// script serves one TCP connection with serve, which reads the client's
// lines from lines. The connection closes when the test ends.
func script(t *testing.T, serve func(conn net.Conn, lines *bufio.Reader)) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var accepted net.Conn
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		mu.Lock()
		accepted = conn
		mu.Unlock()
		defer conn.Close()
		serve(conn, bufio.NewReader(conn))
	}()
	t.Cleanup(func() {
		listener.Close()
		mu.Lock()
		if accepted != nil {
			accepted.Close()
		}
		mu.Unlock()
		<-done
	})
	return listener.Addr().String()
}

// silent reads whatever arrives and never answers.
func silent(conn net.Conn, _ *bufio.Reader) {
	// Ends when either side closes the connection.
	_, _ = io.Copy(io.Discard, conn)
}

// readTag reads one command line and returns its tag, or "" once the
// connection closes, as it does when the test ends.
func readTag(t *testing.T, lines *bufio.Reader) string {
	line, err := lines.ReadString('\n')
	if errors.Is(err, io.EOF) || errors.Is(err, net.ErrClosed) {
		return ""
	}
	if err != nil {
		t.Errorf("read a command: %v", err)
		return ""
	}
	tag, _, _ := strings.Cut(line, " ")
	return tag
}

// plainClient connects without TLS through the wrapper and waits for the
// greeting, as dial does after its handshake.
func plainClient(t *testing.T, addr string, cfg config) (*imapclient.Client, *conn) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := newConn(raw, time.Now().Add(cfg.connect), cfg.idle)
	client := imapclient.New(wrapped, nil)
	t.Cleanup(func() { client.Close() })
	if err := client.WaitGreeting(); err != nil {
		t.Fatalf("greeting: %v", err)
	}
	wrapped.connected()
	return client, wrapped
}

func mailboxAt(t *testing.T, addr string, security Security) mailbox {
	t.Helper()
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	return mailbox{host: host, port: number, security: security, userName: testUser, password: testPassword}
}

func assertCategory(t *testing.T, err error, want Category) {
	t.Helper()
	var failed *failure
	if !errors.As(err, &failed) || failed.category != want {
		t.Fatalf("error = %v, want a failure with %v", err, want)
	}
}

// A server that accepts and then sends nothing fails at the connect limit,
// whether the TLS handshake or the plain greeting is what never comes.
func TestNoGreetingTimesOut(t *testing.T) {
	cfg := config{connect: 300 * time.Millisecond, idle: 5 * time.Second, check: 5 * time.Second}
	for _, security := range []Security{TLS, StartTLS} {
		t.Run(security.String(), func(t *testing.T) {
			addr := script(t, silent)

			started := time.Now()
			session, err := signIn(context.Background(), cfg, nil, mailboxAt(t, addr, security))
			took := time.Since(started)

			if err == nil {
				session.close()
				t.Fatal("signed in to a silent server")
			}
			assertCategory(t, err, TimedOut)
			if took < cfg.connect || took > cfg.connect+time.Second {
				t.Errorf("failed after %v, want the connect limit of %v", took, cfg.connect)
			}
		})
	}
}

// A server that greets and then stalls on LOGIN fails at the idle limit.
func TestStalledLoginTimesOut(t *testing.T) {
	cfg := config{connect: 5 * time.Second, idle: 300 * time.Millisecond, check: 5 * time.Second}
	double := newDouble(t, imaptest.ImplicitTLS)
	release := make(chan struct{})
	// Registered after the double's cleanup, so it runs first and lets the
	// session end before the double closes.
	t.Cleanup(func() { close(release) })
	double.SetHook(func(command imaptest.Command) error {
		if command.Name == "LOGIN" {
			<-release
		}
		return nil
	})
	f := newFixture(t, double)

	started := time.Now()
	session, err := signIn(context.Background(), cfg, f.roots, mailboxAt(t, double.Addr(), TLS))
	took := time.Since(started)

	if err == nil {
		session.close()
		t.Fatal("signed in while LOGIN stalled")
	}
	assertCategory(t, err, TimedOut)
	if took < cfg.idle || took > cfg.idle+time.Second {
		t.Errorf("failed after %v, want the idle limit of %v", took, cfg.idle)
	}
}

// A transfer that keeps sending isn't cut off, however long it takes in
// total.
func TestSlowLiteralArrivesWhole(t *testing.T) {
	cfg := config{connect: time.Second, idle: 200 * time.Millisecond, check: 5 * time.Second}
	// One byte every half idle limit, for three idle limits.
	const literal = "abcdef"
	addr := script(t, func(conn net.Conn, lines *bufio.Reader) {
		io.WriteString(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		tag := readTag(t, lines)
		io.WriteString(conn, "* 1 FETCH (UID 1 BODY[] {"+strconv.Itoa(len(literal))+"}\r\n")
		for i := range len(literal) {
			time.Sleep(cfg.idle / 2)
			if _, err := conn.Write([]byte{literal[i]}); err != nil {
				return
			}
		}
		io.WriteString(conn, ")\r\n"+tag+" OK FETCH completed\r\n")
		silent(conn, lines)
	})
	client, wrapped := plainClient(t, addr, cfg)

	started := time.Now()
	var body []byte
	err := wrapped.command(func() error {
		fetch := client.Fetch(imap.UIDSetNum(1), &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}})
		for message := fetch.Next(); message != nil; message = fetch.Next() {
			for item := message.Next(); item != nil; item = message.Next() {
				section, ok := item.(imapclient.FetchItemDataBodySection)
				if !ok || section.Literal == nil {
					continue
				}
				read, err := io.ReadAll(section.Literal)
				if err != nil {
					fetch.Close()
					return err
				}
				body = read
			}
		}
		return fetch.Close()
	})

	if err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if string(body) != literal {
		t.Errorf("body = %q, want %q", body, literal)
	}
	if took := time.Since(started); took < 3*cfg.idle {
		t.Errorf("the transfer took %v, so it didn't outlast three idle limits", took)
	}
}

// With no command outstanding there is no deadline, so a pause between
// commands doesn't fail the next one. The pause also outlasts the connect
// limit, which must end once connected.
func TestGapBetweenCommands(t *testing.T) {
	cfg := config{connect: 300 * time.Millisecond, idle: 200 * time.Millisecond, check: 5 * time.Second}
	addr := script(t, func(conn net.Conn, lines *bufio.Reader) {
		io.WriteString(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		for {
			tag := readTag(t, lines)
			if tag == "" {
				return
			}
			io.WriteString(conn, tag+" OK NOOP completed\r\n")
		}
	})
	client, wrapped := plainClient(t, addr, cfg)
	if err := wrapped.command(func() error { return client.Noop().Wait() }); err != nil {
		t.Fatalf("first NOOP: %v", err)
	}

	time.Sleep(3 * cfg.idle)

	if err := wrapped.command(func() error { return client.Noop().Wait() }); err != nil {
		t.Fatalf("NOOP after a gap: %v", err)
	}
	if wrapped.timedOut() {
		t.Error("the wrapper recorded a timeout")
	}
}

// After a timeout the connection is done: every later read returns the same
// error at once, rather than waiting out another limit.
func TestTimeoutIsSticky(t *testing.T) {
	cfg := config{connect: time.Second, idle: 200 * time.Millisecond, check: 5 * time.Second}
	addr := script(t, func(conn net.Conn, lines *bufio.Reader) {
		io.WriteString(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
		silent(conn, lines)
	})
	client, wrapped := plainClient(t, addr, cfg)

	started := time.Now()
	err := wrapped.command(func() error { return client.Noop().Wait() })
	if err == nil {
		t.Fatal("NOOP succeeded against a silent server")
	}
	if took := time.Since(started); took < cfg.idle || took > cfg.idle+time.Second {
		t.Errorf("NOOP failed after %v, want the idle limit of %v", took, cfg.idle)
	}
	if !wrapped.timedOut() {
		t.Error("the wrapper didn't record a timeout")
	}

	first := wrapped.stickyErr()
	for range 3 {
		started := time.Now()
		_, err := wrapped.Read(make([]byte, 1))
		if took := time.Since(started); took > cfg.idle/2 {
			t.Errorf("a later read took %v", took)
		}
		if err == nil || err != first {
			t.Errorf("a later read returned %v, want the first error %v", err, first)
		}
	}
}
