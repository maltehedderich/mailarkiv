package collect

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Deps are what a collector works with.
type Deps struct {
	DB      *sqlitedb.DB
	Archive *archive.Archive
	// Status takes the message events: too large, and removed while waiting.
	Status *opstatus.Status
	// Key is nil when serve has no key. Sources can then be listed, but not
	// added or synced.
	Key    *passwordkey.Key
	Logger *slog.Logger
	Now    func() time.Time
	// Roots are the trusted certificates. Nil means the system's.
	Roots *x509.CertPool
}

// Collector runs one loop per source, and each loop runs one sync at a time.
// It is safe for concurrent use.
type Collector struct {
	deps Deps
	cfg  config
	// ctx ends when Stop is called, which stops every sync and closes its
	// connection.
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// manage lets one administrator action at a time read and change a
	// source and its loop.
	manage sync.Mutex

	mu      sync.Mutex
	started bool
	stopped bool
	runners map[int64]*runner
	// unreadable holds when each source's password was found not to open,
	// at this start or at a later sign-in. Such a source has no runner.
	unreadable map[int64]time.Time
	// collectFn runs one sync. Tests replace it.
	collectFn func(ctx context.Context, r *runner) (int, error)
}

// runner is one source's loop. Its fields other than id and wake are
// guarded by Collector.mu.
type runner struct {
	id int64
	// wake holds at most one pending request to sync.
	wake chan struct{}
	// cancel stops the loop and its sync, and done closes when the loop has
	// ended. Both are nil until the loop starts.
	cancel   context.CancelFunc
	done     chan struct{}
	syncing  bool
	progress Progress
	// due is when the next sync starts. It starts at the runner's creation,
	// so every source syncs as soon as its loop starts.
	due time.Time
	// failedAttempts counts the failed syncs in a row, as the row stores it.
	failedAttempts int
}

func newRunner(id int64, now time.Time, failedAttempts int) *runner {
	return &runner{id: id, wake: make(chan struct{}, 1), due: now, failedAttempts: failedAttempts}
}

// Open loads the sources and tries to open each stored password. A source
// whose password doesn't open needs attention and gets no loop. None syncs
// before Start.
func Open(ctx context.Context, deps Deps) (*Collector, error) {
	return open(ctx, deps, productionConfig())
}

func open(ctx context.Context, deps Deps, cfg config) (*Collector, error) {
	rows, err := deps.DB.Reader.QueryContext(ctx, `
SELECT id, host, user_name, sealed_password, failed_attempts,
       paused_at IS NOT NULL OR sign_in_rejected_at IS NOT NULL
FROM imap_source
WHERE removed_at IS NULL
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}
	defer rows.Close()
	now := deps.Now()
	runners := map[int64]*runner{}
	unreadable := map[int64]time.Time{}
	for rows.Next() {
		var id int64
		var host, userName string
		var sealed []byte
		var failedAttempts int
		var waitsForAdmin bool
		if err := rows.Scan(&id, &host, &userName, &sealed, &failedAttempts, &waitsForAdmin); err != nil {
			return nil, fmt.Errorf("load sources: %w", err)
		}
		if _, err := openPassword(deps.Key, sealed, host, userName); err != nil {
			unreadable[id] = now
			deps.Logger.Warn("password unreadable", "source_id", id, "category", PasswordUnreadable.String())
			continue
		}
		if !waitsForAdmin {
			runners[id] = newRunner(id, now, failedAttempts)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}

	c := &Collector{deps: deps, cfg: cfg, runners: runners, unreadable: unreadable}
	c.collectFn = c.collect
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Start starts every loop, and each syncs at once. A source whose sign-in
// was rejected, or whose password doesn't open, has no loop until an
// administrator acts.
func (c *Collector) Start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.started || c.stopped {
		return
	}
	c.started = true
	for _, r := range c.runners {
		c.launch(r)
	}
}

// Stop stops every sync, which closes its connection, and waits for the
// loops to end. A stopped sync records nothing, because it isn't a failure.
func (c *Collector) Stop() {
	c.mu.Lock()
	c.stopped = true
	c.mu.Unlock()
	c.cancel()
	c.wg.Wait()
}

// launch runs r's loop. The caller holds mu.
func (c *Collector) launch(r *runner) {
	ctx, cancel := context.WithCancel(c.ctx)
	r.cancel = cancel
	r.done = make(chan struct{})
	c.wg.Go(func() {
		defer close(r.done)
		defer cancel()
		c.loop(ctx, r)
	})
}

// startRunner starts a loop for a source that has none, which syncs at
// once. failedAttempts is the row's count, which sets the wait after the
// next failure.
func (c *Collector) startRunner(id int64, failedAttempts int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := newRunner(id, c.deps.Now(), failedAttempts)
	c.runners[id] = r
	if c.started && !c.stopped {
		// The first sync starts at once, so the source shows as syncing
		// from now.
		r.syncing = true
		c.launch(r)
	}
}

// stopRunner ends the source's loop and waits for it, which stops a running
// sync and closes its connection. A stopped sync records nothing.
func (c *Collector) stopRunner(id int64) {
	c.mu.Lock()
	r, ok := c.runners[id]
	if ok {
		delete(c.runners, id)
	}
	c.mu.Unlock()
	if !ok || r.cancel == nil {
		return
	}
	r.cancel()
	<-r.done
}

// requestSync asks r's loop to sync at once, and shows the source as
// syncing from now. During a sync, the next one starts when it ends, so it
// reads what changed meanwhile. The caller holds mu.
func (c *Collector) requestSync(r *runner) {
	if r.cancel == nil {
		// The loop hasn't started, and syncs when it does.
		return
	}
	if !r.syncing {
		r.syncing = true
		r.progress = Progress{}
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}
