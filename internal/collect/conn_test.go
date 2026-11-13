package collect

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"slices"
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
	return plainClientWith(t, addr, cfg, nil)
}

func plainClientWith(t *testing.T, addr string, cfg config, options *imapclient.Options) (*imapclient.Client, *conn) {
	t.Helper()
	raw, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	wrapped := newConn(raw, time.Now().Add(cfg.connect), cfg)
	client := imapclient.New(wrapped, options)
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
	cfg := shortLimits(300*time.Millisecond, 5*time.Second)
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
	cfg := shortLimits(5*time.Second, 300*time.Millisecond)
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
	cfg := shortLimits(time.Second, 200*time.Millisecond)
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
	cfg := shortLimits(300*time.Millisecond, 200*time.Millisecond)
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
	cfg := shortLimits(time.Second, 200*time.Millisecond)
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

// stallPoint stalls one step of a sync, and undoes the stall.
type stallPoint struct {
	name     string
	security imaptest.Security
	// connecting is whether the connect limit applies rather than the idle
	// limit.
	connecting bool
	stall      func(t *testing.T, double *imaptest.Server, cfg *config, held imap.UID)
	undo       func(t *testing.T, double *imaptest.Server, held imap.UID)
}

// stallCommand stalls each command named name.
func stallCommand(name string) func(*testing.T, *imaptest.Server, *config, imap.UID) {
	return func(_ *testing.T, double *imaptest.Server, _ *config, _ imap.UID) {
		double.SetHook(func(command imaptest.Command) error {
			if command.Name == name {
				return imaptest.ErrStall
			}
			return nil
		})
	}
}

func clearHook(_ *testing.T, double *imaptest.Server, _ imap.UID) { double.SetHook(nil) }

var stallPoints = []stallPoint{
	{
		name: "the dial", security: imaptest.ImplicitTLS, connecting: true,
		stall: func(_ *testing.T, _ *imaptest.Server, cfg *config, _ imap.UID) {
			cfg.dial = func(ctx context.Context, _ string) (net.Conn, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			}
		},
		undo: func(*testing.T, *imaptest.Server, imap.UID) {},
	},
	{
		name: "the TLS handshake", security: imaptest.ImplicitTLS, connecting: true,
		stall: func(_ *testing.T, double *imaptest.Server, _ *config, _ imap.UID) { double.StallHandshake(true) },
		undo:  func(_ *testing.T, double *imaptest.Server, _ imap.UID) { double.StallHandshake(false) },
	},
	{
		name: "the greeting", security: imaptest.ImplicitTLS, connecting: true,
		stall: func(_ *testing.T, double *imaptest.Server, _ *config, _ imap.UID) { double.WithholdGreeting(true) },
		undo:  func(_ *testing.T, double *imaptest.Server, _ imap.UID) { double.WithholdGreeting(false) },
	},
	{
		name: "STARTTLS", security: imaptest.StartTLS, connecting: true,
		stall: func(_ *testing.T, double *imaptest.Server, _ *config, _ imap.UID) { double.StallHandshake(true) },
		undo:  func(_ *testing.T, double *imaptest.Server, _ imap.UID) { double.StallHandshake(false) },
	},
	{name: "LOGIN", security: imaptest.StartTLS, stall: stallCommand("LOGIN"), undo: clearHook},
	{name: "EXAMINE", security: imaptest.ImplicitTLS, stall: stallCommand("EXAMINE"), undo: clearHook},
	{name: "UID SEARCH", security: imaptest.ImplicitTLS, stall: stallCommand("UID SEARCH"), undo: clearHook},
	// The metadata fetch is the sync's first UID FETCH.
	{name: "the metadata fetch", security: imaptest.ImplicitTLS, stall: stallCommand("UID FETCH"), undo: clearHook},
	{
		name: "partway through a literal", security: imaptest.StartTLS,
		stall: func(t *testing.T, double *imaptest.Server, _ *config, held imap.UID) {
			if err := double.SetFault("INBOX", held, imaptest.Fault{Body: imaptest.BodyStalled}); err != nil {
				t.Fatal(err)
			}
		},
		undo: func(t *testing.T, double *imaptest.Server, held imap.UID) {
			if err := double.SetFault("INBOX", held, imaptest.Fault{}); err != nil {
				t.Fatal(err)
			}
		},
	},
}

// A sync that stalls at any step ends as timed_out once its limit passes,
// and changes nothing in the archive or the checkpoints. The next sync
// archives the message the stall held back.
func TestStallAtEachStep(t *testing.T) {
	for _, point := range stallPoints {
		t.Run(point.name, func(t *testing.T) {
			t.Parallel()
			double := newDouble(t, point.security)
			mail := threeInInbox()[:2]
			addMessages(t, double, mail[:1])
			f := newFixture(t, double)
			security := map[imaptest.Security]string{imaptest.ImplicitTLS: "tls", imaptest.StartTLS: "starttls"}[point.security]
			collector := f.start(t)
			id := addSource(t, f, collector, sourceFor(t, double, security, "INBOX"))
			inbox := f.folderIDs(t, id)["INBOX"]
			addMessages(t, double, mail[1:])
			uidValidity, lastUID := f.checkpoint(t, inbox)

			cfg := testConfig
			point.stall(t, double, &cfg, mail[1].uid)
			limit := cfg.idle
			if point.connecting {
				limit = cfg.connect
			}
			collector.Stop()
			f.clock.Advance(time.Minute)
			before := len(f.logEntries(t, "sync failed"))
			started := time.Now()
			collector = f.startWith(t, cfg)
			category := f.waitForSyncFailure(t, before)
			took := time.Since(started)

			if category != TimedOut.String() {
				t.Errorf("the sync failed with %q, want timed_out", category)
			}
			if took < limit || took > limit+time.Second {
				t.Errorf("the sync failed after %v, want the limit of %v", took, limit)
			}
			if row := f.sourceRow(t, id); row.LastFailureAt != nullInt(f.clock.Now().UnixMilli()) {
				t.Errorf("last_failure_at = %v, want the sync's time", row.LastFailureAt)
			}
			if got, want := f.listed(t), hashesOf(mail[0].body); !slices.Equal(got, want) {
				t.Errorf("listed %v, want the first message alone", got)
			}
			if temp := f.tempFiles(t); len(temp) != 0 {
				t.Errorf("tmp/ holds %v", temp)
			}
			if gotValidity, gotUID := f.checkpoint(t, inbox); gotValidity != uidValidity || gotUID != lastUID {
				t.Errorf("checkpoint = %v %d, want %v %d", gotValidity, gotUID, uidValidity, lastUID)
			}

			point.undo(t, double, mail[1].uid)
			collector = f.syncAgain(t, collector)
			waitForSuccess(t, collector, id, f.clock)
			if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body); !slices.Equal(got, want) {
				t.Errorf("listed %v after the stall ended, want both messages", got)
			}
		})
	}
}
