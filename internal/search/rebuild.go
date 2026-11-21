package search

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// A rebuild fails when one batch for it fails this many times in a row.
// Low disk space and restarts don't count.
const rebuildFailuresInARow = 5

var (
	// ErrRebuildRunning means a rebuild already runs.
	ErrRebuildRunning = errors.New("a rebuild is running")
	// ErrNoIndex means no index is active, because the first build still
	// runs.
	ErrNoIndex = errors.New("no index is active")
)

// NotEnoughSpaceError means free space isn't above 2 GiB plus the active
// index's size.
type NotEnoughSpaceError struct {
	Needed int64
	Free   int64
}

func (e *NotEnoughSpaceError) Error() string {
	return fmt.Sprintf("a rebuild needs over %d free bytes, and %d are free", e.Needed, e.Free)
}

// Rebuild is one run that builds a new index while the old one answers.
type Rebuild struct {
	ID        int64
	StartedAt time.Time
	// StartedBy is the address of the account that started it.
	StartedBy string
}

// runningRebuild is the rebuild that runs, with its generation.
type runningRebuild struct {
	Rebuild
	generationID int64
}

// Progress is a build's cursor out of the highest processed_seq.
type Progress struct {
	Done int64
	ToDo int64
}

type sizeRequest struct {
	ID         int64  `json:"id"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
}

type sizeAnswer struct {
	Bytes int64 `json:"bytes"`
}

type closeRequest struct {
	ID         int64  `json:"id"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
	Delete     bool   `json:"delete"`
}

// StartRebuild starts a rebuild for accountID. It checks free space against
// the active index's size, then writes the rebuild and its generation in
// one transaction.
func (m *Module) StartRebuild(ctx context.Context, accountID int64) (Rebuild, error) {
	m.startMu.Lock()
	defer m.startMu.Unlock()
	m.mu.Lock()
	s := m.session
	running := m.rebuild != nil
	unavailable := m.unavailableLocked()
	m.mu.Unlock()
	if s == nil {
		return Rebuild{}, unavailable
	}
	if running {
		return Rebuild{}, ErrRebuildRunning
	}

	var active int64
	err := m.config.DB.Reader.QueryRowContext(ctx,
		"SELECT id FROM search_generation WHERE role = ?", roleActive).Scan(&active)
	if errors.Is(err, sql.ErrNoRows) {
		return Rebuild{}, ErrNoIndex
	}
	if err != nil {
		return Rebuild{}, fmt.Errorf("read the active index: %w", err)
	}
	var size sizeAnswer
	err = s.client.call(ctx, opstatus.OperationSize, func(id int64) any {
		return sizeRequest{ID: id, Op: "size", Generation: active}
	}, &size)
	if errors.Is(err, errStopped) {
		return Rebuild{}, &UnavailableError{Cause: opstatus.SearchProcessStopped}
	}
	if err != nil {
		return Rebuild{}, fmt.Errorf("measure the active index: %w", err)
	}
	free, err := m.freeSpace(filepath.Join(m.config.DataDir, "index"))
	if err != nil {
		return Rebuild{}, fmt.Errorf("read free space: %w", err)
	}
	if needed := pauseBelow + size.Bytes; free <= needed {
		return Rebuild{}, &NotEnoughSpaceError{Needed: needed, Free: free}
	}

	started := m.config.Now()
	r := runningRebuild{Rebuild: Rebuild{StartedAt: started}}
	err = sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		if err := tx.QueryRowContext(ctx, "SELECT email FROM account WHERE id = ?", accountID).Scan(&r.StartedBy); err != nil {
			return err
		}
		result, err := tx.ExecContext(ctx,
			"INSERT INTO search_rebuild (started_by, started_at) VALUES (?, ?)", accountID, started.UnixMilli())
		if err != nil {
			return err
		}
		if r.ID, err = result.LastInsertId(); err != nil {
			return err
		}
		result, err = tx.ExecContext(ctx,
			"INSERT INTO search_generation (role, schema_version, cursor, rebuild_id, created_at) VALUES (?, ?, 0, ?, ?)",
			roleBuilding, schemaVersion, r.ID, started.UnixMilli())
		if err != nil {
			return err
		}
		r.generationID, err = result.LastInsertId()
		return err
	})
	if err != nil {
		return Rebuild{}, fmt.Errorf("start a rebuild: %w", err)
	}

	m.mu.Lock()
	m.rebuild = &r
	m.rebuildFailures = 0
	m.refreshLocked()
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
	m.config.Logger.Info("rebuild started", "rebuild_id", r.ID, "generation_id", r.generationID)
	return r.Rebuild, nil
}

// Progress is the building generation's cursor out of the highest
// processed_seq, or zero when nothing builds.
func (m *Module) Progress(ctx context.Context) (Progress, error) {
	var done int64
	err := m.config.DB.Reader.QueryRowContext(ctx,
		"SELECT cursor FROM search_generation WHERE role = ?", roleBuilding).Scan(&done)
	if errors.Is(err, sql.ErrNoRows) {
		return Progress{}, nil
	}
	if err != nil {
		return Progress{}, fmt.Errorf("read the build's cursor: %w", err)
	}
	highest, err := m.config.Archive.HighestProcessedSeq(ctx)
	if err != nil {
		return Progress{}, err
	}
	return Progress{Done: done, ToDo: highest}, nil
}

// openRebuild opens a rebuild's generation that started after the others
// were opened, and adds its feed. A generation that can't be created fails
// the rebuild.
func (m *Module) openRebuild(ctx context.Context, s *session, feeds []*feed) ([]*feed, error) {
	m.mu.Lock()
	r := m.rebuild
	m.mu.Unlock()
	if r == nil || slices.ContainsFunc(feeds, func(f *feed) bool { return f.gen.id == r.generationID }) {
		return feeds, nil
	}
	rebuildID := r.ID
	g := &generation{id: r.generationID, role: roleBuilding, rebuildID: &rebuildID}
	if err := m.open(ctx, s, g); err != nil {
		if errors.Is(err, errStopped) || ctx.Err() != nil {
			return feeds, err
		}
		m.failRebuild(ctx, s, g)
		return feeds, nil
	}
	if s.search() == 0 {
		s.setSearch(g.id)
	}
	return append(feeds, m.newFeed(g)), nil
}

// swap makes f's building generation active. One transaction deletes the
// active row, makes f's row active, ends its rebuild as finished, and
// removes the Needs rebuild mark. Then searches go to f's index, and the
// search process closes the old one and removes its directory.
func (m *Module) swap(ctx context.Context, s *session, feeds []*feed, f *feed) ([]*feed, error) {
	var old sql.NullInt64
	var messages int64
	err := sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT id FROM search_generation WHERE role = ?", roleActive).Scan(&old)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if err := tx.QueryRowContext(ctx,
			"SELECT count(*) FROM message WHERE processed_seq <= ?", f.gen.cursor).Scan(&messages); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM search_generation WHERE role = ?", roleActive); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "UPDATE search_generation SET role = ? WHERE id = ?", roleActive, f.gen.id); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, "DELETE FROM search_needs_rebuild"); err != nil {
			return err
		}
		if f.gen.rebuildID == nil {
			return nil
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE search_rebuild SET ended_at = ?, outcome = 'finished' WHERE id = ?",
			m.config.Now().UnixMilli(), *f.gen.rebuildID); err != nil {
			return err
		}
		return fault.At("before-swap-commit")
	})
	if err != nil {
		return feeds, fmt.Errorf("activate the new index: %w", err)
	}
	if f.gen.rebuildID != nil {
		if err := fault.At("after-swap-commit"); err != nil {
			return feeds, err
		}
	}
	f.gen.role = roleActive
	s.setSearch(f.gen.id)
	m.mu.Lock()
	m.needsRebuild = nil
	m.rebuild = nil
	m.rebuildFailures = 0
	m.refreshLocked()
	m.mu.Unlock()
	if f.gen.rebuildID != nil {
		m.config.Logger.Info("rebuild finished",
			"rebuild_id", *f.gen.rebuildID, "generation_id", f.gen.id, "messages", messages)
	}
	if !old.Valid {
		return feeds, nil
	}
	feeds = slices.DeleteFunc(feeds, func(other *feed) bool { return other.gen.id == old.Int64 })
	m.closeGeneration(ctx, s, old.Int64)
	return feeds, nil
}

// countRebuildFailure counts a failed batch for a rebuild's generation, and
// fails the rebuild at the fifth in a row. A batch the search process
// didn't answer because it stopped doesn't count.
func (m *Module) countRebuildFailure(ctx context.Context, s *session, f *feed, err error) {
	if f.gen.rebuildID == nil || errors.Is(err, errStopped) || ctx.Err() != nil {
		return
	}
	m.mu.Lock()
	m.rebuildFailures++
	failures := m.rebuildFailures
	m.mu.Unlock()
	if failures >= rebuildFailuresInARow {
		m.failRebuild(ctx, s, f.gen)
	}
}

// failRebuild deletes g's row and ends its rebuild as failed in one
// transaction, records Rebuild failed, and has the search process remove
// g's directory. The old index keeps answering.
func (m *Module) failRebuild(ctx context.Context, s *session, g *generation) {
	err := sqlitedb.InTx(ctx, m.config.DB.Writer, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, "DELETE FROM search_generation WHERE id = ?", g.id); err != nil {
			return err
		}
		_, err := tx.ExecContext(ctx,
			"UPDATE search_rebuild SET ended_at = ?, outcome = 'failed' WHERE id = ?",
			m.config.Now().UnixMilli(), *g.rebuildID)
		return err
	})
	if err != nil {
		m.config.Logger.Error("end a failed rebuild", "error", err.Error())
		return
	}
	m.config.Status.RecordFailure(opstatus.FailureEvent{
		Category: opstatus.RebuildFailed,
		Detail:   &opstatus.Detail{RebuildID: g.rebuildID, GenerationID: &g.id},
	})
	m.endRebuild()
	if s.search() == g.id {
		s.setSearch(0)
	}
	m.closeGeneration(ctx, s, g.id)
}

// endRebuild forgets the running rebuild.
func (m *Module) endRebuild() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.rebuild = nil
	m.rebuildFailures = 0
	m.refreshLocked()
}

// closeGeneration waits for a maintenance run on generation id, then has the
// search process close it and remove its directory. If that fails, Prepare
// removes the directory at the next start.
func (m *Module) closeGeneration(ctx context.Context, s *session, id int64) {
	s.waitForMaintenance(ctx, id)
	err := s.client.call(ctx, opstatus.OperationClose, func(requestID int64) any {
		return closeRequest{ID: requestID, Op: "close", Generation: id, Delete: true}
	}, nil)
	if err != nil && !errors.Is(err, errStopped) && !errors.Is(err, ErrDidNotAnswer) && ctx.Err() == nil {
		m.config.Logger.Error("close an old index", "generation_id", id, "error", err.Error())
	}
}
