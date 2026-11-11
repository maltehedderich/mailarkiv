package collect

import (
	"errors"
	"net"
	"os"
	"sync"
	"time"
)

// config holds the time limits. Tests shorten them; productionConfig is what
// serve uses.
type config struct {
	// connect covers DNS, TCP, the TLS handshake, the greeting, and for
	// STARTTLS the plain greeting and the upgrade, from the dial's start.
	connect time.Duration
	// idle is the longest a command may go without a byte read or written.
	idle time.Duration
	// check is the longest a request that signs in may take.
	check time.Duration
}

func productionConfig() config {
	return config{connect: 30 * time.Second, idle: 60 * time.Second, check: 120 * time.Second}
}

// conn wraps the TCP connection below TLS, so its deadlines count raw bytes.
// While connecting, one absolute deadline applies. After that, a command
// that is outstanding fails once no byte moves for the idle limit, and with
// none outstanding reads wait without a deadline. A transfer that keeps
// moving is never cut off.
//
// The IMAP library sets its own deadlines, which conn ignores.
type conn struct {
	net.Conn
	idle time.Duration

	mu          sync.Mutex
	outstanding bool
	// err is the first read or write error. Every later read returns it at
	// once, so a failed connection can't wait out another limit.
	err     error
	timeout bool
}

func newConn(inner net.Conn, connectDeadline time.Time, idle time.Duration) *conn {
	// Setting a deadline on a fresh TCP connection doesn't fail.
	_ = inner.SetDeadline(connectDeadline)
	return &conn{Conn: inner, idle: idle}
}

// connected ends the connect deadline.
func (c *conn) connected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.Conn.SetDeadline(time.Time{})
}

// command runs fn, which sends one command and waits for its answer, with
// the idle limit in force.
func (c *conn) command(fn func() error) error {
	c.mu.Lock()
	c.outstanding = true
	c.refresh()
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.outstanding = false
		_ = c.Conn.SetDeadline(time.Time{})
		c.mu.Unlock()
	}()
	return fn()
}

// refresh restarts the idle limit. The caller holds mu.
func (c *conn) refresh() {
	_ = c.Conn.SetDeadline(time.Now().Add(c.idle))
}

func (c *conn) Read(p []byte) (int, error) {
	c.mu.Lock()
	if c.err != nil {
		err := c.err
		c.mu.Unlock()
		return 0, err
	}
	c.mu.Unlock()

	n, err := c.Conn.Read(p)

	c.mu.Lock()
	defer c.mu.Unlock()
	if n > 0 && c.outstanding {
		c.refresh()
	}
	c.record(err)
	return n, err
}

func (c *conn) Write(p []byte) (int, error) {
	c.mu.Lock()
	if c.outstanding {
		c.refresh()
	}
	c.mu.Unlock()

	n, err := c.Conn.Write(p)

	c.mu.Lock()
	defer c.mu.Unlock()
	c.record(err)
	return n, err
}

// record keeps the connection's first error. The caller holds mu.
func (c *conn) record(err error) {
	if err != nil && c.err == nil {
		c.err = err
		c.timeout = isTimeout(err)
	}
}

func (c *conn) SetDeadline(time.Time) error      { return nil }
func (c *conn) SetReadDeadline(time.Time) error  { return nil }
func (c *conn) SetWriteDeadline(time.Time) error { return nil }

// timedOut reports whether the first error was a deadline passing.
func (c *conn) timedOut() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.timeout
}

func (c *conn) stickyErr() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func isTimeout(err error) bool {
	var netErr net.Error
	return errors.Is(err, os.ErrDeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}
