package collect

import (
	"context"
	"errors"
	"net"
	"os"
	"syscall"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// failingDial is a dial option that fails with err.
func failingDial(err error) func(context.Context, string) (net.Conn, error) {
	return func(context.Context, string) (net.Conn, error) { return nil, err }
}

// closedPort returns an address on 127.0.0.1 where nothing listens.
func closedPort(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return addr
}

// Each answer to LOGIN, and each way the server can't be reached, gives its
// category. The reply kept is the server's code and text as it sent them.
func TestSignInResults(t *testing.T) {
	answers := []struct {
		name     string
		answer   error
		category Category
		reply    string
	}{
		{"NO [UNAVAILABLE]", imaptest.No(imap.ResponseCodeUnavailable, "Try again later"), ServerError, "[UNAVAILABLE] Try again later"},
		{"NO with no code", imaptest.No("", "Go away"), SignInRejected, "Go away"},
		{"NO [AUTHENTICATIONFAILED]", imaptest.No(imap.ResponseCodeAuthenticationFailed, "Invalid credentials"), SignInRejected, "[AUTHENTICATIONFAILED] Invalid credentials"},
		{"NO [AUTHORIZATIONFAILED]", imaptest.No(imap.ResponseCodeAuthorizationFailed, "Not allowed"), SignInRejected, "[AUTHORIZATIONFAILED] Not allowed"},
		{"NO [EXPIRED]", imaptest.No(imap.ResponseCodeExpired, "Password expired"), SignInRejected, "[EXPIRED] Password expired"},
		{"NO [CONTACTADMIN]", imaptest.No(imap.ResponseCodeContactAdmin, "Call the help desk"), SignInRejected, "[CONTACTADMIN] Call the help desk"},
		{"BAD", imaptest.Bad("", "Command syntax error"), ServerError, "Command syntax error"},
		{"a closed connection", imaptest.ErrHangUp, ServerError, ""},
	}
	for _, tt := range answers {
		t.Run(tt.name, func(t *testing.T) {
			double := newDouble(t, imaptest.ImplicitTLS)
			double.SetHook(func(command imaptest.Command) error {
				if command.Name == "LOGIN" {
					return tt.answer
				}
				return nil
			})
			f := newFixture(t, double)

			session, err := signIn(context.Background(), testConfig, f.roots, mailboxAt(t, double.Addr(), TLS))

			assertFailure(t, session, err, tt.category, tt.reply)
		})
	}

	unreachable := []struct {
		name string
		dial func(context.Context, string) (net.Conn, error)
		addr func(*testing.T) string
	}{
		{name: "a name that doesn't resolve", dial: failingDial(&net.DNSError{Err: "no such host", Name: "mail.example.test", IsNotFound: true})},
		{name: "a refused connection", addr: closedPort},
		{name: "no route to the host", dial: failingDial(&net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", syscall.EHOSTUNREACH)})},
	}
	for _, tt := range unreachable {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testConfig
			cfg.dial = tt.dial
			addr := "127.0.0.1:993"
			if tt.addr != nil {
				addr = tt.addr(t)
			}

			session, err := signIn(context.Background(), cfg, nil, mailboxAt(t, addr, TLS))

			assertFailure(t, session, err, ServerNotReachable, "")
		})
	}
}

func assertFailure(t *testing.T, session *session, err error, category Category, reply string) {
	t.Helper()
	if err == nil {
		session.close()
		t.Fatal("signed in")
	}
	var failed *failure
	if !errors.As(err, &failed) {
		t.Fatalf("error = %v, want a failure", err)
	}
	if failed.category != category {
		t.Errorf("category = %v, want %v", failed.category, category)
	}
	if failed.reply != reply {
		t.Errorf("reply = %q, want %q", failed.reply, reply)
	}
}
