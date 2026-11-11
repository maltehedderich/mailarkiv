package collect

import (
	"context"
	"crypto/x509"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// Deps are what a collector works with.
type Deps struct {
	DB      *sqlitedb.DB
	Archive *archive.Archive
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

	mu      sync.Mutex
	started bool
	stopped bool
	runners map[int64]*runner
}

// runner is one source's loop. Its fields other than id and wake are
// guarded by Collector.mu.
type runner struct {
	id int64
	// wake holds at most one pending request to sync.
	wake     chan struct{}
	syncing  bool
	progress Progress
}

func newRunner(id int64) *runner {
	r := &runner{id: id, wake: make(chan struct{}, 1)}
	// Every source syncs as soon as its loop starts.
	r.wake <- struct{}{}
	return r
}

// Open loads the sources that can sync. None syncs before Start.
func Open(ctx context.Context, deps Deps) (*Collector, error) {
	return open(ctx, deps, productionConfig())
}

func open(ctx context.Context, deps Deps, cfg config) (*Collector, error) {
	rows, err := deps.DB.Reader.QueryContext(ctx, `
SELECT id FROM imap_source
WHERE removed_at IS NULL AND paused_at IS NULL AND sign_in_rejected_at IS NULL
ORDER BY id`)
	if err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}
	defer rows.Close()
	runners := map[int64]*runner{}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("load sources: %w", err)
		}
		runners[id] = newRunner(id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("load sources: %w", err)
	}

	c := &Collector{deps: deps, cfg: cfg, runners: runners}
	c.ctx, c.cancel = context.WithCancel(context.Background())
	return c, nil
}

// Start starts every loop, and each syncs at once.
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
	c.wg.Go(func() {
		for {
			select {
			case <-c.ctx.Done():
				return
			case <-r.wake:
				c.sync(c.ctx, r)
			}
		}
	})
}

// addRunner starts a loop for a new source, which syncs at once.
func (c *Collector) addRunner(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r := newRunner(id)
	c.runners[id] = r
	if c.started && !c.stopped {
		c.launch(r)
	}
}
