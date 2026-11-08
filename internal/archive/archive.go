// Package archive owns capture, the catalog, and retrieval: the message and
// source_observation tables, and the tmp/ and blobs/ directories. Stored
// originals are files named by their SHA-256, so no supplied name ever
// chooses a path.
package archive

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// MaxSize is the largest file an import accepts, in bytes. A file of exactly
// this size is accepted.
const MaxSize = 150_000_000

// Archive is safe for concurrent use.
type Archive struct {
	dataDir  string
	tmpDir   string
	blobsDir string
	db       *sqlitedb.DB
	status   *opstatus.Status
	logger   *slog.Logger
	now      func() time.Time
	// Tests wrap the real inspector to hold concurrent captures at this boundary.
	inspectBlob func(path, contentHash string) blobState
}

// Open prepares tmp/ and blobs/ in the data directory. status records failure
// events, and logger takes the details an operator needs to find their cause.
func Open(dataDir string, db *sqlitedb.DB, status *opstatus.Status, logger *slog.Logger, now func() time.Time) (*Archive, error) {
	a := &Archive{
		dataDir:     dataDir,
		tmpDir:      filepath.Join(dataDir, "tmp"),
		blobsDir:    filepath.Join(dataDir, "blobs"),
		db:          db,
		status:      status,
		logger:      logger,
		now:         now,
		inspectBlob: inspectBlob,
	}
	for _, dir := range []string{a.tmpDir, a.blobsDir} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, fmt.Errorf("create %s: %w", dir, err)
		}
	}
	return a, nil
}

// blobPath is where the original with this hash lives: blobs/ab/cd/<hash>.
func (a *Archive) blobPath(contentHash string) string {
	return filepath.Join(a.blobsDir, contentHash[0:2], contentHash[2:4], contentHash)
}

// Message is one distinct set of bytes, with what its latest import recorded.
type Message struct {
	ID          int64
	ContentHash string // Lowercase hex SHA-256.
	Size        int64
	FileName    *string // Of the latest import. Nil when none was supplied.
	ImportedAt  time.Time
	ImportCount int
}

// ImportMeta is what an import records besides the bytes.
type ImportMeta struct {
	// AccountEmail is the importing account's stored, lowercase address.
	AccountEmail string
	// FileName is nil when none was supplied. It must be valid UTF-8.
	FileName *string
}

// Outcome is how an import that stored its bytes ended.
type Outcome struct{ name string }

var (
	Archived        = Outcome{"archived"}
	AlreadyArchived = Outcome{"already_archived"}
	Restored        = Outcome{"restored"}
)

// String returns the outcome's name, which the API shares.
func (o Outcome) String() string { return o.name }

// Reason is why the file check or the size limit refused a file.
type Reason struct{ name string }

var (
	EmptyFile    = Reason{"empty"}
	NoMailHeader = Reason{"no_mail_header"}
	TooLarge     = Reason{"too_large"}
)

// String returns the reason's name, which the API shares.
func (r Reason) String() string { return r.name }

// Result is how an import ended. Its method is private, so the set is closed:
// Stored, Refused, Failed, and Dropped.
type Result interface{ isResult() }

// Stored means the bytes and the catalog entry are durable.
type Stored struct {
	Outcome Outcome
	Message Message
}

// Refused means the file was refused and nothing was stored.
type Refused struct{ Reason Reason }

// Failed means the import didn't finish. Its failure event is recorded.
type Failed struct{ Category opstatus.Category }

// Dropped means the body ended early, as when the connection drops. Nothing
// was stored and no failure event is recorded, because no one is waiting for
// an answer.
type Dropped struct{}

func (Stored) isResult()  {}
func (Refused) isResult() {}
func (Failed) isResult()  {}
func (Dropped) isResult() {}
