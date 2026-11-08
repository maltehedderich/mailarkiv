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

	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// copyBufferSize is how much of the body capture reads at a time.
const copyBufferSize = 256 << 10

// Import stores one file in the capture order from ARCHITECTURE.md rule 3:
// temp file, fsync, rename, directory fsyncs, catalog commit. It returns a
// Stored result only after all of these finish, so the result survives a
// power loss. Every other result leaves no list entry and no partial file.
//
// An error means the server failed in a way no result covers, with nothing
// stored. The caller answers 500, and the client shows Unconfirmed.
func (a *Archive) Import(ctx context.Context, body io.Reader, meta ImportMeta) (Result, error) {
	// A complete short file ends with EOF. Preserve transport errors such as
	// ErrUnexpectedEOF so an interrupted upload cannot become an original.
	head, err := io.ReadAll(io.LimitReader(body, headSize))
	if err != nil {
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
	return a.capture(context.WithoutCancel(ctx), head, body, meta)
}

func (a *Archive) capture(ctx context.Context, head []byte, rest io.Reader, meta ImportMeta) (Result, error) {
	temp, err := a.createTemp()
	if err != nil {
		return a.fail(storageCategory(err), "", fmt.Errorf("create a temp file: %w", err)), nil
	}

	size, contentHash, result := a.receive(temp, head, rest)
	closeErr := temp.Close()
	if result == nil && closeErr != nil {
		result = a.fail(opstatus.StorageWriteFailed, "", fmt.Errorf("close the temp file: %w", closeErr))
	}
	if result != nil {
		a.removeTemp(temp.Name())
		return result, nil
	}

	// Read the row before checking the blob. Two imports of the same new bytes
	// can both find no blob, and this separates that race from a blob that
	// really went missing. This isn't Catalog write failed, because that
	// category means the original was stored.
	rowBefore, err := a.messageExists(ctx, contentHash)
	if err != nil {
		a.removeTemp(temp.Name())
		return nil, fmt.Errorf("read whether the message exists: %w", err)
	}

	blob, err := a.ensureBlob(temp.Name(), contentHash)
	if err != nil {
		a.removeTemp(temp.Name())
		return a.fail(opstatus.StorageWriteFailed, contentHash, err), nil
	}

	message, rowAtCommit, err := a.commit(ctx, contentHash, size, meta)
	if err != nil {
		// The blob stays. Recovery lists it as unreferenced at the next start.
		return a.fail(opstatus.CatalogWriteFailed, contentHash, err), nil
	}
	// Only a kill means anything here. The commit is done, so no error path is left.
	_ = fault.At("after-commit")

	outcome, event := decideOutcome(rowBefore, rowAtCommit, blob)
	if event != noEvent {
		a.status.RecordFailure(opstatus.FailureEvent{
			Category:    event,
			MessageID:   &message.ID,
			ContentHash: &contentHash,
			Restored:    true,
		})
	}
	a.status.RemoveUnreferenced(contentHash)
	return Stored{Outcome: outcome, Message: message}, nil
}

func (a *Archive) createTemp() (*os.File, error) {
	if err := fault.At("before-temp-create"); err != nil {
		return nil, err
	}
	if err := fault.At("temp-create"); err != nil {
		return nil, err
	}
	return os.CreateTemp(a.tmpDir, "up-*")
}

// receive streams the file into temp, hashing it on the way, and fsyncs it. A
// nil result means the bytes are durable in temp. A read error is the client's,
// so it gives Dropped, apart from a write error, which is the server's.
func (a *Archive) receive(temp *os.File, head []byte, rest io.Reader) (int64, string, Result) {
	hash := sha256.New()
	write := func(p []byte) error {
		if err := fault.At("temp-write"); err != nil {
			return err
		}
		if _, err := temp.Write(p); err != nil {
			return err
		}
		// A hash.Hash never returns an error from Write.
		_, _ = hash.Write(p)
		return fault.At("mid-temp-write")
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

	if err := syncTemp(temp); err != nil {
		return 0, "", a.fail(storageCategory(err), "", fmt.Errorf("sync the temp file: %w", err))
	}
	return size, hex.EncodeToString(hash.Sum(nil)), nil
}

func syncTemp(temp *os.File) error {
	if err := fault.At("temp-fsync"); err != nil {
		return err
	}
	if err := temp.Sync(); err != nil {
		return err
	}
	return fault.At("after-temp-fsync")
}

// messageExists reads whether a message row holds this hash.
func (a *Archive) messageExists(ctx context.Context, contentHash string) (bool, error) {
	if err := fault.At("row-read"); err != nil {
		return false, err
	}
	var exists bool
	err := a.db.Reader.QueryRowContext(ctx,
		"SELECT EXISTS (SELECT 1 FROM message WHERE content_hash = ?)", contentHash).Scan(&exists)
	return exists, err
}

// ensureBlob leaves a durable, matching blob at the hash's path and returns what
// it found there. A blob that already matches is reused and the temp file goes.
// It may be visible only because of an earlier crash, so the blob and every
// directory up to the data directory get an fsync before the commit that makes
// them part of a confirmation. Anything else is replaced by the temp file.
func (a *Archive) ensureBlob(tempPath, contentHash string) (blobState, error) {
	blob := a.blobPath(contentHash)
	leaf := filepath.Dir(blob)

	found := a.inspectBlob(blob, contentHash)
	if found == blobMatching {
		a.removeTemp(tempPath)
		if err := syncFile(blob); err != nil {
			return found, err
		}
	} else if err := moveIntoPlace(tempPath, blob); err != nil {
		return found, err
	}

	if err := fault.At("dir-fsync"); err != nil {
		return found, fmt.Errorf("sync a directory: %w", err)
	}
	for _, dir := range []string{leaf, filepath.Dir(leaf), a.blobsDir, a.dataDir} {
		if err := syncDir(dir); err != nil {
			return found, err
		}
	}
	if err := fault.At("after-dir-fsync"); err != nil {
		return found, fmt.Errorf("sync a directory: %w", err)
	}
	return found, nil
}

// inspectBlob hashes the whole file. A file that can't be read counts as not
// matching, because Stored original damaged covers both.
func inspectBlob(path, contentHash string) blobState {
	file, _, err := openMatching(path, contentHash)
	if errors.Is(err, fs.ErrNotExist) {
		return blobAbsent
	}
	if err != nil {
		return blobNotMatching
	}
	// Opened for reading only, so closing it can't lose anything.
	file.Close()
	return blobMatching
}

// moveIntoPlace renames the temp file to its blob path. MkdirAll may create
// blobs/ab and blobs/ab/cd, which ensureBlob's directory fsyncs then cover.
func moveIntoPlace(tempPath, blob string) error {
	if err := os.MkdirAll(filepath.Dir(blob), 0o700); err != nil {
		return fmt.Errorf("create the blob directory: %w", err)
	}
	if err := fault.At("rename"); err != nil {
		return fmt.Errorf("move the temp file into place: %w", err)
	}
	if err := os.Rename(tempPath, blob); err != nil {
		return fmt.Errorf("move the temp file into place: %w", err)
	}
	if err := fault.At("after-rename"); err != nil {
		return fmt.Errorf("move the temp file into place: %w", err)
	}
	return nil
}

// decideOutcome classifies the import from catalog and blob state. The second
// result is the failure event to record, or noEvent. A row that was there
// before and is gone at commit can't happen, because no operation deletes
// message rows.
func decideOutcome(rowBefore, rowAtCommit bool, blob blobState) (Outcome, opstatus.Category) {
	switch {
	case !rowAtCommit && blob == blobNotMatching:
		return Archived, opstatus.StoredOriginalDamaged
	case !rowAtCommit:
		return Archived, noEvent
	case blob == blobMatching:
		return AlreadyArchived, noEvent
	case blob == blobNotMatching:
		return Restored, opstatus.StoredOriginalDamaged
	case rowBefore:
		return Restored, opstatus.StoredOriginalMissing
	default:
		// Another import of the same new bytes committed first.
		return AlreadyArchived, noEvent
	}
}

// commit records the import in one transaction. The message row and its
// latest_observation_id are set together, so a committed row is never NULL
// there, and the list's join can't hide it. The bool reports whether the row
// already existed at commit.
func (a *Archive) commit(ctx context.Context, contentHash string, size int64, meta ImportMeta) (Message, bool, error) {
	if err := fault.At("before-commit"); err != nil {
		return Message{}, false, fmt.Errorf("record the import: %w", err)
	}
	if err := fault.At("commit"); err != nil {
		return Message{}, false, fmt.Errorf("record the import: %w", err)
	}

	observedAt := a.now().UnixMilli()
	message := Message{
		ContentHash: contentHash,
		Size:        size,
		FileName:    meta.FileName,
		ImportedAt:  time.UnixMilli(observedAt).UTC(),
	}
	var rowAtCommit bool

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
		rowAtCommit = added == 0

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
		return Message{}, false, fmt.Errorf("record the import: %w", err)
	}
	return message, rowAtCommit, nil
}

// fail records a failure event, which opstatus also logs, and logs the cause
// beside it for the operator. Neither line holds anything from the file or
// the request.
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

// syncFile fsyncs a file that's already in place.
func syncFile(path string) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open a stored original to sync it: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil {
		return fmt.Errorf("sync a stored original: %w", syncErr)
	}
	if closeErr != nil {
		return fmt.Errorf("close a synced stored original: %w", closeErr)
	}
	return nil
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

// blobState is what inspecting a blob's path found.
type blobState struct{ name string }

var (
	blobAbsent      = blobState{"absent"}
	blobMatching    = blobState{"matching"}
	blobNotMatching = blobState{"not_matching"}
)

// noEvent means an import's outcome records no failure event.
var noEvent = opstatus.Category{}
