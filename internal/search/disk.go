package search

import (
	"path/filepath"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

const (
	// Indexing and rebuilding pause when free space where the index lives
	// falls below pauseBelow, and resume once it reaches resumeAt.
	pauseBelow = 2 << 30
	resumeAt   = 3 << 30
	// diskCheckEvery is how often free space is read besides before each
	// batch.
	diskCheckEvery = 30 * time.Second
)

// statfsFree is the free space on path's file system that an unprivileged
// process may use.
func statfsFree(path string) (int64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * stat.Bsize, nil
}

// checkDisk reads free space in the index directory, pauses or resumes
// indexing, and returns whether it is paused. A failed reading changes
// nothing.
func (m *Module) checkDisk() bool {
	free, err := m.freeSpace(filepath.Join(m.config.DataDir, "index"))
	m.mu.Lock()
	if err != nil {
		paused := m.paused
		m.mu.Unlock()
		m.config.Logger.Error("read free space", "error", err.Error())
		return paused
	}
	m.freeBytes, m.diskRead = free, true
	was := m.paused
	switch {
	case !was && free < pauseBelow:
		m.paused = true
	case was && free >= resumeAt:
		m.paused = false
	}
	paused := m.paused
	m.refreshLocked()
	m.mu.Unlock()
	if paused && !was {
		m.config.Status.RecordFailure(opstatus.FailureEvent{
			Category: opstatus.LowDiskSpace,
			Detail:   &opstatus.Detail{FreeBytes: &free},
		})
	}
	if free >= resumeAt {
		m.config.Status.RecordSuccess(opstatus.LowDiskSpace)
	}
	return paused
}
