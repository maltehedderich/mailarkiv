package search

import (
	"context"
	"errors"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

const (
	// A generation is maintained after this many commits, or this long
	// after its last run once it has a commit since.
	maintainEveryCommits = 50
	maintainAfterTime    = 10 * time.Minute
	// This many failed runs in a row mark the index Needs rebuild.
	maintenanceFailuresForRebuild = 3
)

type maintainRequest struct {
	ID         int64  `json:"id"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
}

type maintainAnswer struct {
	Merges int `json:"merges"`
	Failed int `json:"failed"`
}

// maybeMaintain starts a maintenance run for f's generation once one is
// due and none runs for it.
func (m *Module) maybeMaintain(ctx context.Context, s *session, f *feed) {
	if f.commits == 0 {
		return
	}
	now := m.config.Now()
	if f.commits < m.maintainEvery && now.Sub(f.lastMaintained) < m.maintainAfter {
		return
	}
	done, started := s.startMaintenance(f.gen.id)
	if !started {
		return
	}
	f.commits = 0
	f.lastMaintained = now
	go func() {
		defer done()
		m.maintain(ctx, s, f.gen.id)
	}()
}

// maintain runs one maintenance run and counts it. A run the search
// process didn't finish because it stopped doesn't count.
func (m *Module) maintain(ctx context.Context, s *session, generationID int64) {
	var answer maintainAnswer
	err := s.client.call(ctx, opstatus.OperationMaintain, func(id int64) any {
		return maintainRequest{ID: id, Op: "maintain", Generation: generationID}
	}, &answer)
	if errors.Is(err, errStopped) || ctx.Err() != nil {
		return
	}
	if err == nil && m.maintained != nil {
		m.maintained(generationID, answer)
	}
	if err != nil && !errors.Is(err, ErrDidNotAnswer) {
		m.config.Logger.Error("index maintenance", "generation_id", generationID, "error", err.Error())
	}
	failed := err != nil || answer.Failed > 0

	m.mu.Lock()
	if failed {
		m.maintenanceFailures++
	} else {
		m.maintenanceFailures = 0
	}
	inARow := m.maintenanceFailures
	m.mu.Unlock()
	if !failed {
		m.config.Status.RecordSuccess(opstatus.IndexMaintenanceFailed)
		return
	}
	m.config.Status.RecordFailure(opstatus.FailureEvent{
		Category: opstatus.IndexMaintenanceFailed,
		Detail:   &opstatus.Detail{GenerationID: &generationID},
	})
	if inARow >= maintenanceFailuresForRebuild {
		m.markNeedsRebuild(ctx, opstatus.CauseIndexMaintenanceFailed)
	}
}
