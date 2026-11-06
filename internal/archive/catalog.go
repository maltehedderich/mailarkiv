package archive

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"os"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// PageSize is the number of messages on one list page.
const PageSize = 50

// ErrNotFound means no message has the requested ID.
var ErrNotFound = errors.New("no message has that ID")

// Page is one page of the message list.
type Page struct {
	Items []Message
	// Total counts every message, so a page past the end still reports it.
	Total int
}

// listQuery is the data model's list query: most recent import first.
const listQuery = `
SELECT m.id, m.content_hash, m.size, o.file_name, o.observed_at,
       (SELECT COUNT(*) FROM source_observation c WHERE c.message_id = m.id)
FROM message m
JOIN source_observation o ON o.id = m.latest_observation_id
ORDER BY m.latest_observation_id DESC
LIMIT ? OFFSET ?`

// List returns page number page, counting from 1. A page past the end has no
// items and the real total (FR-019).
func (a *Archive) List(ctx context.Context, page int) (Page, error) {
	if page < 1 {
		return Page{}, fmt.Errorf("list page %d: pages count from 1", page)
	}
	result := Page{Items: []Message{}}
	// A page so far past the end that its offset overflows still lists nothing.
	offset := math.MaxInt
	if page-1 <= math.MaxInt/PageSize {
		offset = (page - 1) * PageSize
	}

	// One read transaction, so the items and the total come from one snapshot.
	err := sqlitedb.InTx(ctx, a.db.Reader, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, listQuery, PageSize, offset)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var message Message
			var fileName sql.NullString
			var observedAt int64
			if err := rows.Scan(&message.ID, &message.ContentHash, &message.Size, &fileName, &observedAt, &message.ImportCount); err != nil {
				return err
			}
			if fileName.Valid {
				message.FileName = &fileName.String
			}
			message.ImportedAt = time.UnixMilli(observedAt).UTC()
			result.Items = append(result.Items, message)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		return tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM message").Scan(&result.Total)
	})
	if err != nil {
		return Page{}, fmt.Errorf("list messages: %w", err)
	}
	return result, nil
}

// Original is an open stored original. The caller closes File.
type Original struct {
	File        *os.File
	ContentHash string
	Size        int64
}

// OpenOriginal opens a message's stored original. The path comes from the
// content hash alone, never from a supplied name (FR-021). It doesn't check the
// bytes against the hash yet.
func (a *Archive) OpenOriginal(ctx context.Context, id int64) (Original, error) {
	var contentHash string
	err := a.db.Reader.QueryRowContext(ctx, "SELECT content_hash FROM message WHERE id = ?", id).Scan(&contentHash)
	if errors.Is(err, sql.ErrNoRows) {
		return Original{}, ErrNotFound
	}
	if err != nil {
		return Original{}, fmt.Errorf("read the message: %w", err)
	}

	file, err := os.Open(a.blobPath(contentHash))
	if err != nil {
		return Original{}, fmt.Errorf("open the stored original: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		return Original{}, errors.Join(fmt.Errorf("read the stored original's size: %w", err), file.Close())
	}
	return Original{File: file, ContentHash: contentHash, Size: info.Size()}, nil
}
