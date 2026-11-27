package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

const (
	// This many crashes within crashWindow mark the index Needs rebuild.
	crashesForRebuild = 5
	crashWindow       = 30 * time.Minute
)

// needsRebuild is the search_needs_rebuild row: why the index was marked,
// and when.
type needsRebuild struct {
	cause opstatus.Cause
	since time.Time
}

// rebuildCauses are the categories search_needs_rebuild holds.
var rebuildCauses = map[string]opstatus.Cause{
	opstatus.CauseSearchProcessStopped.String():   opstatus.CauseSearchProcessStopped,
	opstatus.CauseIndexMaintenanceFailed.String(): opstatus.CauseIndexMaintenanceFailed,
	opstatus.CauseIndexDamaged.String():           opstatus.CauseIndexDamaged,
}

// loadSaved reads the Needs rebuild mark and the running rebuild, which
// outlive the server.
func (m *Module) loadSaved(ctx context.Context) error {
	var mark *needsRebuild
	var category string
	var since int64
	err := m.config.DB.Reader.QueryRowContext(ctx, "SELECT category, since FROM search_needs_rebuild").Scan(&category, &since)
	switch {
	case err == nil:
		mark = &needsRebuild{cause: rebuildCauses[category], since: time.UnixMilli(since).UTC()}
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read the needs rebuild mark: %w", err)
	}

	var rebuild *runningRebuild
	var r runningRebuild
	var startedAt int64
	err = m.config.DB.Reader.QueryRowContext(ctx, `
		SELECT r.id, r.started_at, a.email, g.id
		FROM search_rebuild r
		JOIN account a ON a.id = r.started_by
		JOIN search_generation g ON g.rebuild_id = r.id
		WHERE r.outcome IS NULL`).Scan(&r.ID, &startedAt, &r.StartedBy, &r.generationID)
	switch {
	case err == nil:
		r.StartedAt = time.UnixMilli(startedAt).UTC()
		rebuild = &r
	case !errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("read the running rebuild: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.needsRebuild = mark
	m.rebuild = rebuild
	m.refreshLocked()
	return nil
}

// recordCrash keeps the time of a stop nobody asked for, and marks the
// index Needs rebuild at the fifth within 30 minutes.
func (m *Module) recordCrash(ctx context.Context) {
	now := m.config.Now()
	m.mu.Lock()
	recent := m.crashes[:0]
	for _, at := range m.crashes {
		if now.Sub(at) <= crashWindow {
			recent = append(recent, at)
		}
	}
	m.crashes = append(recent, now)
	count := len(m.crashes)
	m.mu.Unlock()
	if count >= crashesForRebuild {
		m.markNeedsRebuild(ctx, opstatus.CauseSearchProcessStopped)
	}
}

// markNeedsRebuild writes search_needs_rebuild and records Index needs
// rebuild, unless the index is already marked. Only a finished rebuild
// removes the mark.
func (m *Module) markNeedsRebuild(ctx context.Context, cause opstatus.Cause) {
	m.markMu.Lock()
	defer m.markMu.Unlock()
	m.mu.Lock()
	marked := m.needsRebuild != nil
	m.mu.Unlock()
	if marked {
		return
	}
	since := m.config.Now()
	if _, err := m.config.DB.Writer.ExecContext(context.WithoutCancel(ctx),
		"INSERT OR IGNORE INTO search_needs_rebuild (id, category, since) VALUES (1, ?, ?)",
		cause.String(), since.UnixMilli()); err != nil {
		// The mark still stops indexing until the server stops.
		m.config.Logger.Error("save the needs rebuild mark", "error", err.Error())
	}
	m.config.Status.RecordFailure(opstatus.FailureEvent{
		Category: opstatus.IndexNeedsRebuild,
		Detail:   &opstatus.Detail{Cause: cause},
	})
	m.mu.Lock()
	defer m.mu.Unlock()
	m.needsRebuild = &needsRebuild{cause: cause, since: since}
	m.refreshLocked()
}

// mayBeIncomplete is true while the index is marked Needs rebuild.
func (m *Module) mayBeIncomplete() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.needsRebuild != nil
}
