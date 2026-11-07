package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// ErrCatalogUnusable marks a recovery error caused by the catalog, so startup
// can name it as the cause (FR-026). Any other recovery error is storage.
var ErrCatalogUnusable = errors.New("the catalog can't be read")

// probeName is the fixed name of the startup probe. The lock is held while it
// exists, and a fixed name means a crash can leave at most one per directory.
const probeName = ".probe"

// CheckStorage creates tmp/ and blobs/ if they're missing and writes and removes
// a probe file in each, so unusable storage stops the start instead of the first
// import (FR-026). The caller holds the data directory's lock.
func CheckStorage(dataDir string) error {
	for _, name := range []string{"tmp", "blobs"} {
		dir := filepath.Join(dataDir, name)
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", dir, err)
		}
		probe := filepath.Join(dir, probeName)
		if err := os.WriteFile(probe, []byte("probe"), 0o600); err != nil {
			return fmt.Errorf("write a probe file: %w", err)
		}
		if err := os.Remove(probe); err != nil {
			return fmt.Errorf("remove a probe file: %w", err)
		}
	}
	return nil
}

// Recover runs once at startup, before the server accepts imports. It removes
// leftover temp files and lists stored originals with no catalog entry. It never
// hashes or deletes a stored original (FR-024), and running it again after an
// interruption gives the same result (FR-025).
//
// An error means the catalog or the storage can't be used, and the server must
// not start. A temp file that can't be removed isn't an error: it's a failure
// event, and startup continues.
func (a *Archive) Recover(ctx context.Context) error {
	started := a.now()

	removed, notRemoved, err := a.removeTempFiles()
	if err != nil {
		return err
	}
	// Only a kill means anything here. No step is in progress to fail.
	_ = fault.At("recovery-after-temp-removal")

	unreferenced, err := a.findUnreferenced(ctx)
	if err != nil {
		return err
	}

	finished := a.now()
	a.status.RecordRecovery(opstatus.RecoveryResult{
		FinishedAt:          finished,
		TempFilesRemoved:    removed,
		TempFilesNotRemoved: notRemoved,
		UnreferencedFound:   unreferenced,
		Duration:            finished.Sub(started),
	})
	return nil
}

func (a *Archive) removeTempFiles() (removed, notRemoved int, err error) {
	entries, err := os.ReadDir(a.tmpDir)
	if err != nil {
		return 0, 0, fmt.Errorf("read the temp directory: %w", err)
	}
	for _, entry := range entries {
		if err := a.removeTempFile(filepath.Join(a.tmpDir, entry.Name())); err != nil {
			notRemoved++
			a.logger.Error("remove a leftover temp file", "error", err.Error())
			a.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.TempFileNotRemoved})
			continue
		}
		removed++
	}
	return removed, notRemoved, nil
}

func (a *Archive) removeTempFile(path string) error {
	if err := fault.At("temp-remove"); err != nil {
		return err
	}
	return os.Remove(path)
}

// blobName is a stored original's name: lowercase hex SHA-256.
var blobName = regexp.MustCompile(`^[0-9a-f]{64}$`)

// validBlobPath reports whether a path relative to blobs/ is ab/cd/<hash>, with
// directories that match the hash.
func validBlobPath(relative string) bool {
	parts := strings.Split(filepath.ToSlash(relative), "/")
	if len(parts) != 3 || !blobName.MatchString(parts[2]) {
		return false
	}
	return parts[0] == parts[2][0:2] && parts[1] == parts[2][2:4]
}

// findUnreferenced walks blobs/, and adds each valid blob with no message row to
// the unreferenced list. It logs only the count of invalid names, because they
// aren't ours and could hold anything.
func (a *Archive) findUnreferenced(ctx context.Context) (int, error) {
	lookup, err := a.db.Reader.PrepareContext(ctx, "SELECT 1 FROM message WHERE content_hash = ?")
	if err != nil {
		return 0, fmt.Errorf("%w: %w", ErrCatalogUnusable, err)
	}
	// A prepared read holds nothing a failed close could lose.
	defer lookup.Close()

	unreferenced, invalid := 0, 0
	err = filepath.WalkDir(a.blobsDir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return fmt.Errorf("walk the blob directory: %w", err)
		}
		if entry.IsDir() {
			return nil
		}
		relative, err := filepath.Rel(a.blobsDir, path)
		if err != nil {
			return fmt.Errorf("walk the blob directory: %w", err)
		}
		if !entry.Type().IsRegular() || !validBlobPath(relative) {
			invalid++
			return nil
		}

		contentHash := entry.Name()
		var present int
		err = lookup.QueryRowContext(ctx, contentHash).Scan(&present)
		if err == nil {
			return nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("%w: %w", ErrCatalogUnusable, err)
		}
		info, err := entry.Info()
		if err != nil {
			return fmt.Errorf("read a stored original's size: %w", err)
		}
		a.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: contentHash, Size: info.Size()})
		unreferenced++
		return nil
	})
	if err != nil {
		return 0, err
	}
	if invalid > 0 {
		a.logger.Warn("invalid names in blobs", "count", invalid)
	}
	return unreferenced, nil
}
