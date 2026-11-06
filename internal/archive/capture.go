package archive

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"syscall"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// copyBufferSize is how much of the body capture reads at a time.
const copyBufferSize = 256 << 10

// Import stores one file in the capture order from ARCHITECTURE.md rule 3: temp
// file, fsync, rename, directory fsyncs, catalog commit. It returns a Stored
// result only after all of these finish, so the result survives a power loss
// (FR-012). Every other result leaves no list entry and no partial file (FR-013).
//
// An error means the server failed in a way no result covers. The caller
// answers 500.
func (a *Archive) Import(ctx context.Context, body io.Reader, meta ImportMeta) (Result, error) {
	head := make([]byte, headSize)
	n, err := io.ReadFull(body, head)
	head = head[:n]
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return Dropped{}, nil
	}

	if reason, refused := checkHead(head); refused {
		// Read the rest first. A browser still sending can miss an answer that
		// arrives before it finishes, and would show Unconfirmed (research.md).
		if _, err := io.Copy(io.Discard, io.LimitReader(body, MaxSize)); err != nil {
			return Dropped{}, nil
		}
		return Refused{Reason: reason}, nil
	}

	// Once the bytes are in, capture finishes even if the client leaves, so the
	// archive matches whatever the client might later learn.
	return a.capture(context.WithoutCancel(ctx), head, body, meta), nil
}

func (a *Archive) capture(ctx context.Context, head []byte, rest io.Reader, meta ImportMeta) Result {
	temp, err := os.CreateTemp(a.tmpDir, "up-*")
	if err != nil {
		return a.fail(storageCategory(err), "", fmt.Errorf("create a temp file: %w", err))
	}

	size, contentHash, result := a.receive(temp, head, rest)
	closeErr := temp.Close()
	if result == nil && closeErr != nil {
		result = a.fail(opstatus.StorageWriteFailed, "", fmt.Errorf("close the temp file: %w", closeErr))
	}
	if result != nil {
		a.removeTemp(temp.Name())
		return result
	}

	if err := a.storeBlob(temp.Name(), contentHash); err != nil {
		a.removeTemp(temp.Name())
		return a.fail(opstatus.StorageWriteFailed, contentHash, err)
	}

	outcome, message, err := a.commit(ctx, contentHash, size, meta)
	if err != nil {
		// The blob stays. Recovery lists it as unreferenced at the next start.
		return a.fail(opstatus.CatalogWriteFailed, contentHash, err)
	}
	return Stored{Outcome: outcome, Message: message}
}

// receive streams the file into temp, hashing it on the way, and fsyncs it. A
// nil result means the bytes are durable in temp. A read error is the client's,
// so it gives Dropped, apart from a write error, which is the server's.
func (a *Archive) receive(temp *os.File, head []byte, rest io.Reader) (int64, string, Result) {
	hash := sha256.New()
	write := func(p []byte) error {
		if _, err := temp.Write(p); err != nil {
			return err
		}
		// A hash.Hash never returns an error from Write.
		_, _ = hash.Write(p)
		return nil
	}

	if err := write(head); err != nil {
		return 0, "", a.fail(storageCategory(err), "", fmt.Errorf("write the temp file: %w", err))
	}
	size := int64(len(head))
	buffer := make([]byte, copyBufferSize)
	for {
		n, readErr := rest.Read(buffer)
		if n > 0 {
			size += int64(n)
			if size > MaxSize {
				return 0, "", Refused{Reason: TooLarge}
			}
			if err := write(buffer[:n]); err != nil {
				return 0, "", a.fail(storageCategory(err), "", fmt.Errorf("write the temp file: %w", err))
			}
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return 0, "", Dropped{}
		}
	}

	if err := temp.Sync(); err != nil {
		return 0, "", a.fail(storageCategory(err), "", fmt.Errorf("sync the temp file: %w", err))
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

// storeBlob renames the temp file to its blob path, then fsyncs every directory
// from the blob's up to the data directory. MkdirAll may have just created
// blobs/ab and blobs/ab/cd, and their entries must be durable too.
func (a *Archive) storeBlob(tempPath, contentHash string) error {
	blob := a.blobPath(contentHash)
	leaf := filepath.Dir(blob)
	if err := os.MkdirAll(leaf, 0o700); err != nil {
		return fmt.Errorf("create the blob directory: %w", err)
	}
	if err := os.Rename(tempPath, blob); err != nil {
		return fmt.Errorf("move the temp file into place: %w", err)
	}
	for _, dir := range []string{leaf, filepath.Dir(leaf), a.blobsDir, a.dataDir} {
		if err := syncDir(dir); err != nil {
			return err
		}
	}
	return nil
}

// commit records the import in one transaction. The message row and its
// latest_observation_id are set together, so a committed row is never NULL
// there, and the list's join can't hide it.
func (a *Archive) commit(ctx context.Context, contentHash string, size int64, meta ImportMeta) (Outcome, Message, error) {
	observedAt := a.now().UnixMilli()
	message := Message{
		ContentHash: contentHash,
		Size:        size,
		FileName:    meta.FileName,
		ImportedAt:  time.UnixMilli(observedAt).UTC(),
	}
	outcome := AlreadyArchived

	err := sqlitedb.InTx(ctx, a.db.Writer, func(tx *sql.Tx) error {
		inserted, err := tx.ExecContext(ctx,
			"INSERT INTO message (content_hash, size) VALUES (?, ?) ON CONFLICT (content_hash) DO NOTHING",
			contentHash, size)
		if err != nil {
			return err
		}
		added, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		if added == 1 {
			outcome = Archived
		}

		if err := tx.QueryRowContext(ctx,
			"SELECT id FROM message WHERE content_hash = ?", contentHash).Scan(&message.ID); err != nil {
			return err
		}
		observation, err := tx.ExecContext(ctx,
			`INSERT INTO source_observation (message_id, source_type, observed_at, account_email, file_name)
			 VALUES (?, 'manual_import', ?, ?, ?)`,
			message.ID, observedAt, meta.AccountEmail, nullable(meta.FileName))
		if err != nil {
			return err
		}
		observationID, err := observation.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx,
			"UPDATE message SET latest_observation_id = ? WHERE id = ?", observationID, message.ID); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM source_observation WHERE message_id = ?", message.ID).Scan(&message.ImportCount)
	})
	if err != nil {
		return Outcome{}, Message{}, fmt.Errorf("record the import: %w", err)
	}
	return outcome, message, nil
}

// fail records a failure event, which opstatus also logs (FR-028), and logs the
// cause beside it for the operator. Neither line holds anything from the file
// or the request.
func (a *Archive) fail(category opstatus.Category, contentHash string, cause error) Failed {
	event := opstatus.FailureEvent{Category: category}
	if contentHash != "" {
		event.ContentHash = &contentHash
	}
	a.logger.Error("import failed", "error", cause.Error())
	a.status.RecordFailure(event)
	return Failed{Category: category}
}

// removeTemp deletes a temp file that capture no longer needs. A file left
// behind is harmless, and recovery removes it at the next start.
func (a *Archive) removeTemp(path string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		a.logger.Error("remove a temp file", "error", err.Error())
	}
}

// storageCategory maps an error from creating, writing, or syncing the temp
// file. A full disk is the one cause an operator fixes differently.
func storageCategory(err error) opstatus.Category {
	if errors.Is(err, syscall.ENOSPC) {
		return opstatus.StorageFull
	}
	return opstatus.StorageWriteFailed
}

func syncDir(path string) error {
	dir, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open a directory to sync it: %w", err)
	}
	syncErr := dir.Sync()
	closeErr := dir.Close()
	if syncErr != nil {
		return fmt.Errorf("sync a directory: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close a synced directory: %w", closeErr)
	}
	return nil
}

// nullable turns a missing name into SQL NULL.
func nullable(s *string) sql.NullString {
	if s == nil {
		return sql.NullString{}
	}
	return sql.NullString{String: *s, Valid: true}
}
