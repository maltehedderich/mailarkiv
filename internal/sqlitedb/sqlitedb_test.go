package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// migrationCount reads the directory directly, so the expected version doesn't
// come from the code under test and stays right as later slices add files.
func migrationCount(t *testing.T) int {
	t.Helper()
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	return len(entries)
}

func openCatalog(t *testing.T) (*DB, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return db, path
}

func pragma(t *testing.T, conn *sql.Conn, name string) string {
	t.Helper()
	var value string
	if err := conn.QueryRowContext(context.Background(), "PRAGMA "+name).Scan(&value); err != nil {
		t.Fatalf("PRAGMA %s: %v", name, err)
	}
	return value
}

// connections holds n connections at once, so the pool has to open new ones.
// Settings applied to one connection only would fail on the others.
func connections(t *testing.T, pool *sql.DB, n int) []*sql.Conn {
	t.Helper()
	held := make([]*sql.Conn, n)
	for i := range held {
		conn, err := pool.Conn(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { conn.Close() })
		held[i] = conn
	}
	return held
}

func TestEveryConnectionHasTheDurabilitySettings(t *testing.T) {
	db, _ := openCatalog(t)

	// The values are the plan's: WAL, synchronous=FULL (2), and foreign keys on.
	pools := []struct {
		name      string
		conns     []*sql.Conn
		queryOnly string
	}{
		{"writer", connections(t, db.Writer, 1), "0"},
		{"reader", connections(t, db.Reader, 3), "1"},
	}
	for _, pool := range pools {
		for i, conn := range pool.conns {
			t.Run(fmt.Sprintf("%s connection %d", pool.name, i), func(t *testing.T) {
				want := map[string]string{
					"journal_mode": "wal",
					"synchronous":  "2",
					"foreign_keys": "1",
					"busy_timeout": "5000",
					"query_only":   pool.queryOnly,
				}
				for name, value := range want {
					if got := pragma(t, conn, name); got != value {
						t.Errorf("%s = %s, want %s", name, got, value)
					}
				}
			})
		}
	}
}

func TestReadPoolRefusesWrites(t *testing.T) {
	db, _ := openCatalog(t)

	_, err := db.Reader.Exec(`INSERT INTO account (email, password_hash, created_at) VALUES ('a@example.test', 'x', 1)`)
	if err == nil {
		t.Fatal("the read pool accepted a write")
	}

	var count int
	if err := db.Writer.QueryRow(`SELECT COUNT(*) FROM account`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("account has %d rows after a refused write", count)
	}
}

// A read transaction gives one view across several queries, and it must not
// take the write lock. With _txlock=immediate on this pool, Begin fails.
func TestReadTransactionDoesNotBlockTheWriter(t *testing.T) {
	db, _ := openCatalog(t)

	read, err := db.Reader.Begin()
	if err != nil {
		t.Fatalf("Begin on the read pool: %v", err)
	}
	defer read.Rollback()
	var before int
	if err := read.QueryRow(`SELECT COUNT(*) FROM account`).Scan(&before); err != nil {
		t.Fatal(err)
	}

	if _, err := db.Writer.Exec(`INSERT INTO account (email, password_hash, created_at) VALUES ('a@example.test', 'x', 1)`); err != nil {
		t.Fatalf("write during a read transaction: %v", err)
	}

	var after int
	if err := read.QueryRow(`SELECT COUNT(*) FROM account`).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != before {
		t.Errorf("the read transaction's view changed from %d to %d rows", before, after)
	}
}

func TestWriterPoolHoldsOneConnection(t *testing.T) {
	db, _ := openCatalog(t)

	if got := db.Writer.Stats().MaxOpenConnections; got != 1 {
		t.Fatalf("MaxOpenConnections = %d, want 1", got)
	}
}

// A deferred transaction that reads and then writes can fail at once if another
// process wrote in between, and busy_timeout doesn't cover that. Taking the lock
// at Begin makes the CLI and the server wait for each other instead.
func TestWriterTransactionTakesTheLockAtBegin(t *testing.T) {
	db, path := openCatalog(t)

	tx, err := db.Writer.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()

	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()

	_, err = other.Exec(`INSERT INTO account (email, password_hash, created_at) VALUES ('a@example.test', 'x', 1)`)
	if err == nil {
		t.Fatal("a second connection wrote during a transaction that had run no statement")
	}
	if !strings.Contains(err.Error(), "locked") && !strings.Contains(err.Error(), "busy") {
		t.Fatalf("second writer failed for another reason: %v", err)
	}
}

func TestMigrationsSetTheVersionAndRunOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := Open(path)
	if err != nil {
		t.Fatalf("first Open: %v", err)
	}

	var version int
	if err := db.Writer.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if want := migrationCount(t); version != want {
		t.Fatalf("user_version = %d, want %d", version, want)
	}

	if _, err := db.Writer.Exec(`INSERT INTO account (email, password_hash, created_at) VALUES ('a@example.test', 'x', 1)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	// A migration that ran again would fail on CREATE TABLE.
	db, err = Open(path)
	if err != nil {
		t.Fatalf("second Open: %v", err)
	}
	defer db.Close()

	var email string
	if err := db.Reader.QueryRow(`SELECT email FROM account`).Scan(&email); err != nil {
		t.Fatalf("the row from before reopening: %v", err)
	}
	if err := db.Reader.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if want := migrationCount(t); version != want {
		t.Fatalf("user_version after reopening = %d, want %d", version, want)
	}
}

func TestMigrationFilesAreNumberedInOrder(t *testing.T) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no migrations")
	}
	for i, entry := range entries {
		if want := fmt.Sprintf("%04d_", i+1); !strings.HasPrefix(entry.Name(), want) {
			t.Errorf("file %d is %s, want the prefix %s", i, entry.Name(), want)
		}
	}
}

func TestNewerDatabaseRefusesToOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	newer := migrationCount(t) + 1
	if _, err := db.Writer.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, newer)); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	refused, err := Open(path)
	if err == nil {
		refused.Close()
		t.Fatal("Open accepted a database newer than the binary")
	}
	if !errors.Is(err, ErrNewerDatabase) {
		t.Fatalf("error = %v, want ErrNewerDatabase", err)
	}

	direct, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	var version int
	if err := direct.QueryRow(`PRAGMA user_version`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != newer {
		t.Fatalf("user_version = %d after the refusal, want %d", version, newer)
	}
}

func TestGarbageFileRefusesToOpen(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	garbage := []byte(strings.Repeat("not a database ", 1024))
	if err := os.WriteFile(path, garbage, 0o600); err != nil {
		t.Fatal(err)
	}

	db, err := Open(path)
	if err == nil {
		db.Close()
		t.Fatal("Open accepted a file that isn't a database")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(garbage) {
		t.Fatal("the refused file changed")
	}
}

func TestPathWithURICharacters(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "odd ?#% name")
	if err := os.Mkdir(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "catalog.db")

	db, err := Open(path)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer db.Close()

	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the catalog isn't at the path given: %v", err)
	}
}

func TestSchemaHasTheSixTables(t *testing.T) {
	db, _ := openCatalog(t)

	rows, err := db.Reader.Query(`SELECT name FROM sqlite_master WHERE type = 'table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		got = append(got, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}

	want := "account message session sign_in_block sign_in_failure source_observation"
	if strings.Join(got, " ") != want {
		t.Fatalf("tables = %v, want %s", got, want)
	}
}

func insertMessage(t *testing.T, db *DB, hash string) int64 {
	t.Helper()
	result, err := db.Writer.Exec(`INSERT INTO message (content_hash, size) VALUES (?, 1)`, hash)
	if err != nil {
		t.Fatal(err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	return id
}

const insertObservation = `INSERT INTO source_observation
	(message_id, source_type, observed_at, account_email, file_name)
	VALUES (?, ?, 1, 'a@example.test', NULL)`

func TestSourceTypeAcceptsOnlyManualImport(t *testing.T) {
	db, _ := openCatalog(t)
	id := insertMessage(t, db, "hash")

	for _, sourceType := range []string{"imap", "mbox", "MANUAL_IMPORT", ""} {
		t.Run(sourceType, func(t *testing.T) {
			_, err := db.Writer.Exec(insertObservation, id, sourceType)
			if err == nil {
				t.Fatal("the table accepted the value")
			}
			if !strings.Contains(err.Error(), "CHECK constraint") {
				t.Fatalf("refused for another reason: %v", err)
			}
		})
	}

	if _, err := db.Writer.Exec(insertObservation, id, "manual_import"); err != nil {
		t.Fatalf("manual_import: %v", err)
	}
}

func TestObservationNeedsAnExistingMessage(t *testing.T) {
	db, _ := openCatalog(t)

	if _, err := db.Writer.Exec(insertObservation, 999, "manual_import"); err == nil {
		t.Fatal("the table accepted an observation of a message that doesn't exist")
	}
}

// A stale message ID in a log or a failure report must never name another message.
func TestMessageIDsAreNeverReused(t *testing.T) {
	db, _ := openCatalog(t)

	first := insertMessage(t, db, "first")
	if _, err := db.Writer.Exec(`DELETE FROM message WHERE id = ?`, first); err != nil {
		t.Fatal(err)
	}
	if second := insertMessage(t, db, "second"); second <= first {
		t.Fatalf("second ID = %d, first was %d", second, first)
	}
}
