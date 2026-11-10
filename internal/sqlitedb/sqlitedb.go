// Package sqlitedb opens the catalog with its durability settings and applies
// the schema migrations. The modules that own the tables do all the reading and
// writing.
package sqlitedb

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"

	// Registers the "sqlite" driver. ADR 0002 records the choice.
	_ "modernc.org/sqlite"
)

// ErrNewerDatabase means the catalog was migrated by a newer binary, which
// this one can't read safely.
var ErrNewerDatabase = errors.New("the catalog is newer than this binary")

// settings apply to every connection. The driver runs each _pragma when it opens
// a connection, so connections the pool opens later get them too.
const settings = "_pragma=busy_timeout(5000)" +
	"&_pragma=journal_mode(WAL)" +
	"&_pragma=synchronous(FULL)" +
	"&_pragma=foreign_keys(ON)"

// The writer takes its lock at BEGIN, so a transaction that reads before it
// writes waits for another process instead of failing. The reader can't use
// this: on a query_only connection, BEGIN IMMEDIATE fails.
const (
	writerSettings = settings + "&_txlock=immediate"
	readerSettings = settings + "&_pragma=query_only(ON)"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB is the catalog. Writer holds one connection, so writes queue in this
// process. Reader is a pool that refuses writes.
type DB struct {
	Writer *sql.DB
	Reader *sql.DB
}

// Open opens the catalog at path, creating it if it's missing, and brings its
// schema up to date.
func Open(path string) (*DB, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("find the catalog: %w", err)
	}
	// A URI keeps characters such as ? and # in the path from being read as
	// the start of the settings.
	location := (&url.URL{Scheme: "file", Path: absolute}).String()

	writer, err := sql.Open("sqlite", location+"?"+writerSettings)
	if err != nil {
		return nil, fmt.Errorf("open the catalog: %w", err)
	}
	writer.SetMaxOpenConns(1)
	if err := migrate(context.Background(), writer, migrations); err != nil {
		return nil, errors.Join(err, writer.Close())
	}

	reader, err := sql.Open("sqlite", location+"?"+readerSettings)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("open the catalog for reading: %w", err), writer.Close())
	}
	if err := reader.Ping(); err != nil {
		return nil, errors.Join(fmt.Errorf("open the catalog for reading: %w", err), reader.Close(), writer.Close())
	}

	return &DB{Writer: writer, Reader: reader}, nil
}

// Close closes both pools.
func (db *DB) Close() error {
	return errors.Join(db.Reader.Close(), db.Writer.Close())
}

// migrate applies each migration in set that the catalog lacks, one
// transaction each, and counts them in PRAGMA user_version. It reads the
// version inside the write transaction, so `serve` and an account command
// starting together can't both apply the same file.
func migrate(ctx context.Context, writer *sql.DB, set fs.FS) error {
	// Glob returns the names sorted, which is the order the numbers give.
	files, err := fs.Glob(set, "migrations/*.sql")
	if err != nil {
		return fmt.Errorf("list the migrations: %w", err)
	}

	for {
		applied, err := migrateOnce(ctx, writer, set, files)
		if err != nil {
			return err
		}
		if !applied {
			return nil
		}
	}
}

// migrateOnce applies the next migration and reports whether there was one.
func migrateOnce(ctx context.Context, writer *sql.DB, set fs.FS, files []string) (applied bool, err error) {
	tx, err := writer.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("open the catalog: %w", err)
	}
	defer func() {
		if !applied {
			err = errors.Join(err, tx.Rollback())
		}
	}()

	var version int
	if err := tx.QueryRowContext(ctx, "PRAGMA user_version").Scan(&version); err != nil {
		return false, fmt.Errorf("read the catalog's version: %w", err)
	}
	if version > len(files) {
		return false, fmt.Errorf("%w: the catalog has version %d, and this binary knows versions up to %d",
			ErrNewerDatabase, version, len(files))
	}
	if version == len(files) {
		return false, nil
	}

	name := files[version]
	statements, err := fs.ReadFile(set, name)
	if err != nil {
		return false, fmt.Errorf("read %s: %w", name, err)
	}
	if _, err := tx.ExecContext(ctx, string(statements)); err != nil {
		return false, fmt.Errorf("apply %s: %w", name, err)
	}
	// PRAGMA takes no parameters. The value is an integer this code computed.
	if _, err := tx.ExecContext(ctx, fmt.Sprintf("PRAGMA user_version = %d", version+1)); err != nil {
		return false, fmt.Errorf("record %s: %w", name, err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit %s: %w", name, err)
	}
	return true, nil
}

// InTx runs fn in a transaction on db, and commits only if fn returns nil.
// Otherwise it rolls back and returns fn's error.
func InTx(ctx context.Context, db *sql.DB, fn func(*sql.Tx) error) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin a transaction: %w", err)
	}
	if err := fn(tx); err != nil {
		return errors.Join(err, tx.Rollback())
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
