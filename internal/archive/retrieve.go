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
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

var (
	// ErrNotFound means no message has the requested ID.
	ErrNotFound = errors.New("no message has that ID")
	// ErrOriginalMissing means the message exists and its stored file doesn't.
	ErrOriginalMissing = errors.New("the stored original is missing")
	// ErrOriginalDamaged means the stored file can't be read or doesn't match
	// its content hash.
	ErrOriginalDamaged = errors.New("the stored original is damaged")
)

var errHashMismatch = errors.New("the file doesn't match its content hash")

// Original is an open stored original whose bytes matched its hash when they
// were read. The caller closes File.
type Original struct {
	File        *os.File
	MessageID   int64
	ContentHash string
	Size        int64
}

// OpenVerified opens a message's stored original, hashes all of it, and returns
// the same open file at its start, so the bytes sent are the bytes checked
// (FR-023). The path comes from the content hash alone, never from a supplied
// name (FR-021). ErrOriginalMissing and ErrOriginalDamaged each record a failure
// event, and ErrNotFound doesn't, because nothing is wrong with the archive.
func (a *Archive) OpenVerified(ctx context.Context, id int64) (Original, error) {
	var contentHash string
	err := a.db.Reader.QueryRowContext(ctx, "SELECT content_hash FROM message WHERE id = ?", id).Scan(&contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Original{}, ErrNotFound
	}
	if err != nil {
		return Original{}, fmt.Errorf("read the message: %w", err)
	}

	file, size, err := openMatching(a.blobPath(contentHash), contentHash)
	switch {
	case err == nil:
		return Original{File: file, MessageID: id, ContentHash: contentHash, Size: size}, nil
	case errors.Is(err, fs.ErrNotExist):
		a.recordFailedRead(opstatus.StoredOriginalMissing, id, contentHash)
		return Original{}, ErrOriginalMissing
	default:
		a.logger.Error("check a stored original", "message_id", id, "error", err.Error())
		a.recordFailedRead(opstatus.StoredOriginalDamaged, id, contentHash)
		return Original{}, ErrOriginalDamaged
	}
}

// RecordDamaged records that a checked original failed while it was being sent.
func (a *Archive) RecordDamaged(original Original) {
	a.recordFailedRead(opstatus.StoredOriginalDamaged, original.MessageID, original.ContentHash)
}

func (a *Archive) recordFailedRead(category opstatus.Category, id int64, contentHash string) {
	a.status.RecordFailure(opstatus.FailureEvent{
		Category:    category,
		MessageID:   &id,
		ContentHash: &contentHash,
	})
}

// openMatching opens the file at path and hashes all of it. It returns the file
// at its start, with its size, only if the hash matches. An error for a missing
// file wraps fs.ErrNotExist, and any other means the file can't be read or
// doesn't match, which FR-038's Stored original damaged covers.
func openMatching(path, contentHash string) (*os.File, int64, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, 0, err
	}

	hash := sha256.New()
	size, err := io.Copy(hash, file)
	if err == nil && hex.EncodeToString(hash.Sum(nil)) != contentHash {
		err = errHashMismatch
	}
	if err == nil {
		_, err = file.Seek(0, io.SeekStart)
	}
	if err != nil {
		// Opened for reading only, so closing it can't lose anything.
		file.Close()
		return nil, 0, err
	}
	return file, size, nil
}

// Observation is one recorded import of a message's bytes (FR-014).
type Observation struct {
	SourceType   string // One of the values source_observation's CHECK allows.
	ObservedAt   time.Time
	AccountEmail string
	FileName     *string // Nil when none was supplied.
}

// History is a message with every import of its bytes, newest first (FR-020).
type History struct {
	ID           int64
	ContentHash  string
	Size         int64
	Observations []Observation
}

// History returns a message's history with no cap, or ErrNotFound.
func (a *Archive) History(ctx context.Context, id int64) (History, error) {
	history := History{ID: id, Observations: []Observation{}}

	// One read transaction, so the message and its observations agree.
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		err := tx.QueryRowContext(ctx, "SELECT content_hash, size FROM message WHERE id = ?", id).
			Scan(&history.ContentHash, &history.Size)
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		if err != nil {
			return err
		}

		rows, err := tx.QueryContext(ctx, `
SELECT source_type, observed_at, account_email, file_name
FROM source_observation
WHERE message_id = ?
ORDER BY id DESC`, id)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var observation Observation
			var observedAt int64
			var fileName sql.NullString
			if err := rows.Scan(&observation.SourceType, &observedAt, &observation.AccountEmail, &fileName); err != nil {
				return err
			}
			observation.ObservedAt = time.UnixMilli(observedAt).UTC()
			if fileName.Valid {
				observation.FileName = &fileName.String
			}
			history.Observations = append(history.Observations, observation)
		}
		return rows.Err()
	})
	if errors.Is(err, ErrNotFound) {
		return History{}, ErrNotFound
	}
	if err != nil {
		return History{}, fmt.Errorf("read a message's history: %w", err)
	}
	return history, nil
}
