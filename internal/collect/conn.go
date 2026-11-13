package collect

import (
	"context"
	"errors"
	"net"
	"os"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

// config holds the limits and the parts tests replace. productionConfig is
// what serve uses.
type config struct {
	// connect covers DNS, TCP, the TLS handshake, the greeting, and for
	// STARTTLS the plain greeting and the upgrade, from the dial's start.
	connect time.Duration
	// idle is the longest a command may go without a byte read or written.
	idle time.Duration
	// check is the longest a request that signs in may take.
	check time.Duration
	// commandBytes is the most a command's answer may read, and bodyBytes
	// the most a body fetch may. The client reads a whole reply into memory,
	// so these bound what a server can make it hold.
	commandBytes int64
	bodyBytes    int64
	// dial connects to address. Nil means a net.Dialer.
	dial func(ctx context.Context, address string) (net.Conn, error)
	// after returns a channel that receives once d has passed, and a func
	// that stops it. Nil means a time.Timer.
	after func(d time.Duration) (<-chan time.Time, func() bool)
}

func productionConfig() config {
	return config{
		connect:      30 * time.Second,
		idle:         60 * time.Second,
		check:        120 * time.Second,
		commandBytes: 16 << 20,
		// A body of the largest size stored, with room for the FETCH line
		// around it.
		bodyBytes: archive.MaxSize + 1<<20,
	}
}

// errReplyTooLarge fails a connection whose server sent more than the
// command may read.
var errReplyTooLarge = errors.New("the server sent more than the command may read")

// conn wraps the TCP connection below TLS, so its deadlines and byte bounds
// count raw bytes. While connecting, one absolute deadline applies. After
// that, a command that is outstanding fails once no byte moves for the idle
// limit, and with none outstanding reads wait without a deadline. A transfer
// that keeps moving is never cut off.
//
// The bytes read since the last command ended count against the current
// command's bound. The client reads whatever arrives between commands at
// once, so those bytes count toward the next command.
//
// The IMAP library sets its own deadlines, which conn ignores.
type conn struct {
	net.Conn
	idle         time.Duration
	commandBytes int64
	bodyBytes    int64

	mu          sync.Mutex
	outstanding bool
	bound       int64
	read        int64
	// err is the first read or write error. Every later read returns it at
	// once, so a failed connection can't wait out another limit.
	err     error
	timeout bool
}

func newConn(inner net.Conn, connectDeadline time.Time, cfg config) *conn {
	// Setting a deadline on a fresh TCP connection doesn't fail.
	_ = inner.SetDeadline(connectDeadline)
	return &conn{Conn: inner, idle: cfg.idle, commandBytes: cfg.commandBytes, bodyBytes: cfg.bodyBytes, bound: cfg.commandBytes}
}

// connected ends the connect deadline and starts counting bytes afresh.
func (c *conn) connected() {
	c.mu.Lock()
	defer c.mu.Unlock()
	_ = c.Conn.SetDeadline(time.Time{})
	c.read = 0
}

// command runs fn, which sends one command and waits for its answer, with
// the idle limit and the command bound in force.
func (c *conn) command(fn func() error) error {
	return c.run(c.commandBytes, fn)
}

// bodyCommand is command for a fetch of one message's body, which may read
// up to the body bound.
func (c *conn) bodyCommand(fn func() error) error {
	return c.run(c.bodyBytes, fn)
}

func (c *conn) run(bound int64, fn func() error) error {
	c.mu.Lock()
	c.outstanding = true
	c.bound = bound
	c.refresh()
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.outstanding = false
		c.bound = c.commandBytes
		c.read = 0
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
	c.read += int64(n)
	if over := c.read - c.bound; over > 0 && err == nil {
		// The bytes past the bound are dropped. The client reads buffered
		// bytes before an error, so it would otherwise act on a reply that
		// ends past the bound.
		n -= int(min(over, int64(n)))
		err = errReplyTooLarge
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
