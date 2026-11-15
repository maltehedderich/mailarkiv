package collect

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net"
	"strconv"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"
)

// Security is how a source's connection is encrypted.
type Security struct{ name string }

var (
	// TLS starts TLS at once, usually on port 993.
	TLS = Security{"tls"}
	// StartTLS greets in plain text and upgrades, usually on port 143.
	StartTLS = Security{"starttls"}
)

// String returns the name the API and the catalog share.
func (s Security) String() string { return s.name }

// mailbox is where a session signs in.
type mailbox struct {
	host     string
	port     int
	security Security
	userName string
	password string
}

// failure is a sync or check that ended with a category. reply is the
// server's NO or BAD code and text, if it sent one.
type failure struct {
	category Category
	err      error
	reply    string
}

func (f *failure) Error() string { return f.category.String() + ": " + f.err.Error() }
func (f *failure) Unwrap() error { return f.err }

// session is a signed-in connection. Every command goes through do.
type session struct {
	client *imapclient.Client
	conn   *conn
	ctx    context.Context
	// stop cancels the close that the context's end would trigger.
	stop func() bool
}

// signIn connects, sets up TLS, waits for the greeting, and sends LOGIN.
// Ending ctx closes the connection. roots nil means the system's trusted
// certificates.
func signIn(ctx context.Context, cfg config, roots *x509.CertPool, box mailbox) (*session, error) {
	deadline := time.Now().Add(cfg.connect)
	dialCtx, cancel := context.WithDeadline(ctx, deadline)
	raw, err := cfg.dialer()(dialCtx, net.JoinHostPort(box.host, strconv.Itoa(box.port)))
	cancel()
	if err != nil {
		return nil, classify(ctx, nil, dialing, fmt.Errorf("connect: %w", err))
	}
	wrapped := newConn(raw, deadline, cfg)
	stop := context.AfterFunc(ctx, func() { raw.Close() })

	client, at, err := greet(ctx, wrapped, roots, box)
	if err != nil {
		stop()
		raw.Close()
		return nil, classify(ctx, wrapped, at, err)
	}
	wrapped.connected()

	s := &session{client: client, conn: wrapped, ctx: ctx, stop: stop}
	if err := wrapped.command(func() error { return client.Login(box.userName, box.password).Wait() }); err != nil {
		s.close()
		return nil, classify(ctx, wrapped, signingIn, fmt.Errorf("sign in: %w", err))
	}
	return s, nil
}

// dialer returns the dial option, or a net.Dialer.
func (cfg config) dialer() func(ctx context.Context, address string) (net.Conn, error) {
	if cfg.dial != nil {
		return cfg.dial
	}
	return func(ctx context.Context, address string) (net.Conn, error) {
		var dialer net.Dialer
		return dialer.DialContext(ctx, "tcp", address)
	}
}

// greet sets up TLS and waits for the greeting, within the connect deadline.
// An error comes with the step it came from.
func greet(ctx context.Context, wrapped *conn, roots *x509.CertPool, box mailbox) (*imapclient.Client, step, error) {
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, ServerName: box.host}
	if box.security == StartTLS {
		// The library sends STARTTLS without checking the capabilities. A
		// server that doesn't offer it answers NO, before any LOGIN.
		client, err := imapclient.NewStartTLS(wrapped, &imapclient.Options{TLSConfig: tlsConfig})
		if err != nil {
			return nil, securing, fmt.Errorf("upgrade with STARTTLS: %w", err)
		}
		return client, securing, nil
	}

	encrypted := tls.Client(wrapped, tlsConfig)
	if err := encrypted.HandshakeContext(ctx); err != nil {
		return nil, securing, fmt.Errorf("TLS handshake: %w", err)
	}
	client := imapclient.New(encrypted, nil)
	if err := client.WaitGreeting(); err != nil {
		client.Close()
		return nil, greeting, fmt.Errorf("wait for the greeting: %w", err)
	}
	return client, greeting, nil
}

// do runs one command with the idle limit and the command bound in force,
// and classifies its error.
func (s *session) do(fn func() error) error {
	if err := s.conn.command(fn); err != nil {
		return classify(s.ctx, s.conn, commanding, err)
	}
	return nil
}

// doBody is do for a fetch of one message's body, which has the body bound.
func (s *session) doBody(fn func() error) error {
	if err := s.conn.bodyCommand(fn); err != nil {
		return classify(s.ctx, s.conn, commanding, err)
	}
	return nil
}

// logout ends the session politely. It sends no CLOSE, which would expunge.
func (s *session) logout() error {
	err := s.do(func() error { return s.client.Logout().Wait() })
	s.close()
	return err
}

func (s *session) close() {
	// The connection is done either way. Close reports the decoder's last
	// error, which the caller already has.
	_ = s.client.Close()
	s.stop()
}
