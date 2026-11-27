package search

import (
	"context"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// Report is what the status screen shows about search and indexing.
type Report struct {
	State State
	// NextStartAt is set while no search process runs and the next start
	// waits.
	NextStartAt  *time.Time
	NeedsRebuild *NeedsRebuild
	// Restarts counts starts after the first since the server started.
	Restarts int
	// Waiting counts processed messages the searched index hasn't saved:
	// the active one, or the building one while none is active.
	Waiting int64
	// Progress is set while a generation builds, for the first build or a
	// rebuild, also while indexing is paused or stopped.
	Progress *Progress
	// FreeBytes is the latest free space reading, nil before the first.
	FreeBytes *int64
	Rebuild   *Rebuild
}

// NeedsRebuild is the mark that stops indexing until a rebuild succeeds.
type NeedsRebuild struct {
	Cause opstatus.Cause
	Since time.Time
}

// Free space thresholds where the index lives, in bytes.
const (
	PauseBelowBytes = pauseBelow
	ResumeAtBytes   = resumeAt
)

// Report reads the state, the counts, and the running rebuild.
func (m *Module) Report(ctx context.Context) (Report, error) {
	m.mu.Lock()
	report := Report{State: m.state, Restarts: max(m.restarts-1, 0)}
	if m.nextStartAt != nil && m.session == nil {
		next := *m.nextStartAt
		report.NextStartAt = &next
	}
	if m.needsRebuild != nil {
		report.NeedsRebuild = &NeedsRebuild{Cause: m.needsRebuild.cause, Since: m.needsRebuild.since}
	}
	if m.diskRead {
		free := m.freeBytes
		report.FreeBytes = &free
	}
	if m.rebuild != nil {
		rebuild := m.rebuild.Rebuild
		report.Rebuild = &rebuild
	}
	m.mu.Unlock()

	highest, err := m.config.Archive.HighestProcessedSeq(ctx)
	if err != nil {
		return Report{}, err
	}
	cursors := map[string]int64{}
	rows, err := m.config.DB.Reader.QueryContext(ctx, "SELECT role, cursor FROM search_generation")
	if err != nil {
		return Report{}, fmt.Errorf("read the index cursors: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var role string
		var cursor int64
		if err := rows.Scan(&role, &cursor); err != nil {
			return Report{}, fmt.Errorf("read the index cursors: %w", err)
		}
		cursors[role] = cursor
	}
	if err := rows.Err(); err != nil {
		return Report{}, fmt.Errorf("read the index cursors: %w", err)
	}

	searched, active := cursors[roleActive]
	building, builds := cursors[roleBuilding]
	if !active {
		searched = building
	}
	report.Waiting = max(highest-searched, 0)
	if builds {
		report.Progress = &Progress{Done: building, ToDo: highest}
	}
	return report, nil
}
