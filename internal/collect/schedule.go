package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	// successWait is how long after a successful sync ends the next is due.
	successWait = 5 * time.Minute
	// maxRetryWait is the longest wait after a failed sync.
	maxRetryWait = time.Hour
)

// retryWait is the wait after the nth failed sync in a row: min(2^(n−1), 60)
// minutes.
func retryWait(n int) time.Duration {
	// The shift stops at 2^6, already past the cap, so it can't overflow.
	return min(time.Minute<<min(n-1, 6), maxRetryWait)
}

// loop runs r's syncs, each when it is due or when woken, until ctx ends or
// the source needs an administrator.
func (c *Collector) loop(ctx context.Context, r *runner) {
	for {
		due, stop := c.after(c.untilDue(r))
		select {
		case <-ctx.Done():
			stop()
			return
		case <-r.wake:
			stop()
		case <-due:
		}
		if !c.sync(ctx, r) {
			return
		}
	}
}

func (c *Collector) untilDue(r *runner) time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return r.due.Sub(c.deps.Now())
}

func (c *Collector) after(d time.Duration) (<-chan time.Time, func() bool) {
	if c.cfg.after != nil {
		return c.cfg.after(d)
	}
	timer := time.NewTimer(d)
	return timer.C, timer.Stop
}

// wake asks r's loop to sync at once. A source that is syncing ignores it.
func (c *Collector) wake(id int64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r, ok := c.runners[id]
	if !ok || r.syncing {
		return
	}
	select {
	case r.wake <- struct{}{}:
	default:
	}
}

// sync runs one sync of r's source and records how it ended. It returns
// false when the loop ends.
func (c *Collector) sync(ctx context.Context, r *runner) bool {
	c.setSyncing(r, true)
	archived, err := c.collectFn(ctx, r)
	ended := c.deps.Now()
	// Recording outlives a stop, so a sync that ended is recorded whole.
	record := context.WithoutCancel(ctx)
	logger := c.deps.Logger.With("source_id", r.id)

	var failed *failure
	switch {
	case ctx.Err() != nil:
		// Stopped. That isn't a failure, so nothing is recorded.
		c.setSyncing(r, false)
		return false
	case errors.Is(err, errPasswordUnreadable):
		// No sign-in was tried, so it isn't a failed attempt.
		c.endUnreadable(r, ended)
		logger.Warn("password unreadable", "category", PasswordUnreadable.String())
		return false
	case err == nil:
		// The state leaves syncing before the success is recorded, so a
		// reader never sees the new success beside the syncing state.
		c.finish(r, 0, ended.Add(successWait))
		if err := c.recordSuccess(record, r.id, archived, ended); err != nil {
			logger.Error("sync failed", "category", CatalogWriteFailed.String(), "error", err.Error())
		}
		return true
	case !errors.As(err, &failed):
		failed = &failure{category: CatalogWriteFailed, err: err}
		logger = logger.With("error", err.Error())
	}

	attempts := c.attempts(r) + 1
	// The failure is recorded while the state is still syncing, so a reader
	// never sees the new count beside an old state.
	if err := c.recordFailure(record, r.id, failed, attempts, ended); err != nil {
		logger.Error("record a failed sync", "error", err.Error())
	}
	fields := []any{"category", failed.category.String(), "failed_attempts", attempts}
	rejected := failed.category == SignInRejected
	if rejected {
		c.end(r)
	} else {
		due := ended.Add(retryWait(attempts))
		c.finish(r, attempts, due)
		fields = append(fields, "next_attempt_at", due.UTC())
	}
	// The error may hold the server's reply, which stays out of the log.
	logger.Warn("sync failed", fields...)
	return !rejected
}

// setSyncing marks r as syncing or not. A wake-up already queued when a sync
// starts is dropped, so it can't start a second sync after this one.
func (c *Collector) setSyncing(r *runner, syncing bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.syncing = syncing
	r.progress = Progress{}
	if syncing {
		select {
		case <-r.wake:
		default:
		}
	}
}

// finish ends r's sync with the failure count and the next due time.
func (c *Collector) finish(r *runner, attempts int, due time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.syncing = false
	r.progress = Progress{}
	r.failedAttempts = attempts
	r.due = due
}

// end ends r's sync and its loop. An administrator's action starts a new
// one.
func (c *Collector) end(r *runner) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeRunner(r)
}

// endUnreadable ends r's sync and its loop, and marks its password as found
// unreadable at at.
func (c *Collector) endUnreadable(r *runner, at time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.removeRunner(r)
	c.unreadable[r.id] = at
}

// removeRunner ends r's sync and drops it. The caller holds mu.
func (c *Collector) removeRunner(r *runner) {
	r.syncing = false
	if c.runners[r.id] == r {
		delete(c.runners, r.id)
	}
}

func (c *Collector) attempts(r *runner) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return r.failedAttempts
}

// recordSuccess clears the source's failure columns and records the
// success.
func (c *Collector) recordSuccess(ctx context.Context, sourceID int64, archived int, at time.Time) error {
	_, err := c.deps.DB.Writer.ExecContext(ctx, `
UPDATE imap_source SET failed_attempts = 0, failing_since = NULL, last_failure_category = NULL,
       last_failure_at = NULL, last_server_reply = NULL, last_success_at = ?, last_success_archived = ?
WHERE id = ?`, at.UnixMilli(), archived, sourceID)
	if err != nil {
		return fmt.Errorf("record source %d's success: %w", sourceID, err)
	}
	return nil
}

// recordFailure records the source's attempts-th failed sync in a row, at
// at. A rejected sign-in also stops the source.
func (c *Collector) recordFailure(ctx context.Context, sourceID int64, failed *failure, attempts int, at time.Time) error {
	var rejectedAt sql.NullInt64
	if failed.category == SignInRejected {
		rejectedAt = sql.NullInt64{Int64: at.UnixMilli(), Valid: true}
	}
	reply := sql.NullString{String: failed.reply, Valid: failed.reply != ""}
	_, err := c.deps.DB.Writer.ExecContext(ctx, `
UPDATE imap_source SET failed_attempts = ?, failing_since = COALESCE(failing_since, ?),
       last_failure_category = ?, last_failure_at = ?, last_server_reply = ?,
       sign_in_rejected_at = COALESCE(?, sign_in_rejected_at)
WHERE id = ?`, attempts, at.UnixMilli(), failed.category.String(), at.UnixMilli(), reply, rejectedAt, sourceID)
	if err != nil {
		return fmt.Errorf("record source %d's failure: %w", sourceID, err)
	}
	return nil
}
