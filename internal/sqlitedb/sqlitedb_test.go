package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"testing/fstest"
)

// migrationCount reads the directory directly, so the expected version
// doesn't come from the code under test and stays right as migrations are
// added.
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

	// WAL and synchronous=FULL (2) ensure durable commits; foreign keys enforce
	// references.
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

// withSecond returns a migration set holding the real first migration and,
// unless second is empty, a second file with that SQL.
func withSecond(t *testing.T, second string) fstest.MapFS {
	t.Helper()
	first, err := os.ReadFile("migrations/0001_initial.sql")
	if err != nil {
		t.Fatal(err)
	}
	set := fstest.MapFS{"migrations/0001_initial.sql": {Data: first}}
	if second != "" {
		set["migrations/0002_under_test.sql"] = &fstest.MapFile{Data: []byte(second)}
	}
	return set
}

// readDirectly opens the catalog without the runner, so a failed Open can
// still be inspected.
func readDirectly(t *testing.T, path, query string) int {
	t.Helper()
	direct, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer direct.Close()
	var n int
	if err := direct.QueryRow(query).Scan(&n); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return n
}

// Foreign keys are off while a migration runs, so a broken reference can only
// be caught by the check before the commit.
func TestMigrationWithABrokenReferenceRollsBack(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	set := withSecond(t, `INSERT INTO source_observation
	(message_id, source_type, observed_at, account_email, file_name)
	VALUES (999, 'manual_import', 1, 'a@example.test', NULL);`)

	db, err := open(path, set)
	if err == nil {
		db.Close()
		t.Fatal("Open accepted a migration that left a reference to message 999")
	}
	if !strings.Contains(err.Error(), "source_observation") {
		t.Errorf("error = %v, want it to name the table with the broken reference", err)
	}

	if version := readDirectly(t, path, "PRAGMA user_version"); version != 1 {
		t.Errorf("user_version = %d, want 1", version)
	}
	if rows := readDirectly(t, path, "SELECT COUNT(*) FROM source_observation"); rows != 0 {
		t.Errorf("source_observation has %d rows, want the insert rolled back", rows)
	}
}

// SQLite can't change a CHECK or NOT NULL in place, so widening a table means
// create, copy, drop, and rename. Dropping a table that another one references
// fails while foreign keys are on.
func TestMigrationCanRebuildAReferencedTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "catalog.db")
	db, err := open(path, withSecond(t, ""))
	if err != nil {
		t.Fatal(err)
	}
	messageID := insertMessage(t, db, "hash")
	result, err := db.Writer.Exec(insertObservation, messageID, "manual_import")
	if err != nil {
		t.Fatal(err)
	}
	observationID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Writer.Exec("UPDATE message SET latest_observation_id = ? WHERE id = ?", observationID, messageID); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	db, err = open(path, withSecond(t, `
CREATE TABLE source_observation_new (
    id            INTEGER PRIMARY KEY,
    message_id    INTEGER NOT NULL REFERENCES message (id),
    source_type   TEXT NOT NULL CHECK (source_type IN ('manual_import')),
    observed_at   INTEGER NOT NULL,
    account_email TEXT NOT NULL,
    file_name     TEXT,
    note          TEXT
) STRICT;
INSERT INTO source_observation_new (id, message_id, source_type, observed_at, account_email, file_name)
    SELECT id, message_id, source_type, observed_at, account_email, file_name FROM source_observation;
DROP TABLE source_observation;
ALTER TABLE source_observation_new RENAME TO source_observation;
CREATE INDEX source_observation_by_message ON source_observation (message_id, id);`))
	if err != nil {
		t.Fatalf("Open with the rebuild: %v", err)
	}
	defer db.Close()

	var foreignKeys int
	if err := db.Writer.QueryRow("PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatal(err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys on the writer = %d after the migration, want 1", foreignKeys)
	}
	var version int
	if err := db.Writer.QueryRow("PRAGMA user_version").Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Errorf("user_version = %d, want 2", version)
	}

	var latest int64
	err = db.Reader.QueryRow(`SELECT o.id FROM message m JOIN source_observation o ON o.id = m.latest_observation_id
		WHERE m.id = ?`, messageID).Scan(&latest)
	if err != nil || latest != observationID {
		t.Errorf("latest observation = %d, %v, want %d", latest, err, observationID)
	}
	if _, err := db.Writer.Exec(insertObservation, 999, "manual_import"); err == nil {
		t.Error("after the rebuild, the table accepted an observation of a message that doesn't exist")
	}
}

func TestSchemaHasTheTenTables(t *testing.T) {
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

	want := "account collected_message_count imap_folder imap_message_problem imap_source " +
		"message session sign_in_block sign_in_failure source_observation"
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

// row is one row to insert, by column name.
type row map[string]any

// with returns a copy of r with the given columns changed. A nil value
// leaves the column out, so it takes its default or NULL.
func (r row) with(changes row) row {
	changed := row{}
	for column, value := range r {
		changed[column] = value
	}
	for column, value := range changes {
		if value == nil {
			delete(changed, column)
		} else {
			changed[column] = value
		}
	}
	return changed
}

func insert(db *DB, table string, r row) (int64, error) {
	columns := make([]string, 0, len(r))
	for column := range r {
		columns = append(columns, column)
	}
	slices.Sort(columns)
	values := make([]any, len(columns))
	for i, column := range columns {
		values[i] = r[column]
	}
	placeholders := strings.TrimSuffix(strings.Repeat("?, ", len(columns)), ", ")
	result, err := db.Writer.Exec(
		fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", table, strings.Join(columns, ", "), placeholders),
		values...)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func mustInsert(t *testing.T, db *DB, table string, r row) int64 {
	t.Helper()
	id, err := insert(db, table, r)
	if err != nil {
		t.Fatalf("insert into %s: %v", table, err)
	}
	return id
}

// refused asserts that inserting r fails on a constraint whose message holds
// reason, such as "CHECK constraint" or "UNIQUE constraint".
func refused(t *testing.T, db *DB, table string, r row, reason string) {
	t.Helper()
	_, err := insert(db, table, r)
	if err == nil {
		t.Fatalf("%s accepted %v", table, r)
	}
	if !strings.Contains(err.Error(), reason) {
		t.Fatalf("%s refused %v for another reason: %v", table, r, err)
	}
}

func activeSource(host, userName string) row {
	return row{
		"name":            "Mailbox",
		"host":            host,
		"port":            993,
		"security":        "tls",
		"user_name":       userName,
		"user_key":        strings.ToLower(userName),
		"sealed_password": []byte("sealed"),
		"created_at":      1,
	}
}

func chosenFolder(sourceID int64, name string) row {
	return row{"source_id": sourceID, "name": name, "chosen": 1}
}

func importEntry(messageID int64) row {
	return row{
		"message_id":    messageID,
		"source_type":   "manual_import",
		"observed_at":   1,
		"account_email": "a@example.test",
	}
}

func collectionEntry(messageID, sourceID, folderID int64) row {
	return row{
		"message_id":     messageID,
		"source_type":    "imap_collection",
		"observed_at":    1,
		"imap_source_id": sourceID,
		"imap_folder_id": folderID,
	}
}

func TestSourceTypeAcceptsImportAndCollection(t *testing.T) {
	db, _ := openCatalog(t)
	messageID := insertMessage(t, db, "hash")
	sourceID := mustInsert(t, db, "imap_source", activeSource("imap.example.test", "anna@example.test"))
	folderID := mustInsert(t, db, "imap_folder", chosenFolder(sourceID, "INBOX"))

	for _, sourceType := range []string{"imap", "mbox", "MANUAL_IMPORT", "IMAP_COLLECTION", ""} {
		t.Run(sourceType, func(t *testing.T) {
			change := row{"source_type": sourceType}
			refused(t, db, "source_observation", importEntry(messageID).with(change), "CHECK constraint")
			refused(t, db, "source_observation", collectionEntry(messageID, sourceID, folderID).with(change), "CHECK constraint")
		})
	}

	mustInsert(t, db, "source_observation", importEntry(messageID))
	mustInsert(t, db, "source_observation", collectionEntry(messageID, sourceID, folderID))
}

func TestObservationColumnsMatchTheirType(t *testing.T) {
	db, _ := openCatalog(t)
	messageID := insertMessage(t, db, "hash")
	sourceID := mustInsert(t, db, "imap_source", activeSource("imap.example.test", "anna@example.test"))
	folderID := mustInsert(t, db, "imap_folder", chosenFolder(sourceID, "INBOX"))
	imported := importEntry(messageID)
	collected := collectionEntry(messageID, sourceID, folderID)

	refusedRows := []struct {
		name string
		row  row
	}{
		{"an import without an address", imported.with(row{"account_email": nil})},
		{"an import naming a source", imported.with(row{"imap_source_id": sourceID})},
		{"an import naming a folder", imported.with(row{"imap_folder_id": folderID})},
		{"an import with a received time", imported.with(row{"received_at": 5})},
		{"a collection without a source", collected.with(row{"imap_source_id": nil})},
		{"a collection without a folder", collected.with(row{"imap_folder_id": nil})},
		{"a collection with a file name", collected.with(row{"file_name": "invoice.eml"})},
		{"a collection with an address", collected.with(row{"account_email": "a@example.test"})},
	}
	for _, tt := range refusedRows {
		t.Run(tt.name, func(t *testing.T) {
			refused(t, db, "source_observation", tt.row, "CHECK constraint")
		})
	}

	t.Run("one collection entry per folder and message", func(t *testing.T) {
		mustInsert(t, db, "source_observation", collected.with(row{"received_at": 5}))
		refused(t, db, "source_observation", collected, "UNIQUE constraint")

		other := mustInsert(t, db, "imap_folder", chosenFolder(sourceID, "Archiv"))
		mustInsert(t, db, "source_observation", collected.with(row{"imap_folder_id": other}))
	})

	t.Run("repeated imports of one message", func(t *testing.T) {
		mustInsert(t, db, "source_observation", imported.with(row{"file_name": "invoice.eml"}))
		mustInsert(t, db, "source_observation", imported)
	})
}

func TestSourceRowRules(t *testing.T) {
	db, _ := openCatalog(t)
	source := activeSource("imap.example.test", "anna@example.test")
	failing := row{
		"failed_attempts":       1,
		"failing_since":         10,
		"last_failure_category": "timed_out",
		"last_failure_at":       10,
	}

	refusedRows := []struct {
		name string
		row  row
	}{
		{"port 0", source.with(row{"port": 0})},
		{"port 65536", source.with(row{"port": 65536})},
		{"security ssl", source.with(row{"security": "ssl"})},
		{"no password on an active source", source.with(row{"sealed_password": nil})},
		{"a password on a removed source", source.with(row{"removed_at": 20})},
		{"failing since, with no failed attempt", source.with(row{"failing_since": 10})},
		{"a failure category, with no failed attempt", source.with(row{"last_failure_category": "timed_out"})},
		{"a failure time, with no failed attempt", source.with(row{"last_failure_at": 10})},
		{"a failed attempt without its start", source.with(failing).with(row{"failing_since": nil})},
		{"a failed attempt without its category", source.with(failing).with(row{"last_failure_category": nil})},
		{"a failed attempt without its time", source.with(failing).with(row{"last_failure_at": nil})},
		{"a folder category on the source", source.with(failing).with(row{"last_failure_category": "folder_unavailable"})},
		{"a state category on the source", source.with(failing).with(row{"last_failure_category": "password_unreadable"})},
	}
	for _, tt := range refusedRows {
		t.Run(tt.name, func(t *testing.T) {
			refused(t, db, "imap_source", tt.row, "CHECK constraint")
		})
	}

	t.Run("each sync category", func(t *testing.T) {
		for i, category := range []string{
			"server_not_reachable", "secure_connection_failed", "sign_in_rejected", "timed_out",
			"server_error", "storage_full", "storage_write_failed", "catalog_write_failed",
		} {
			user := fmt.Sprintf("user%d@example.test", i)
			mustInsert(t, db, "imap_source", activeSource("imap.example.test", user).
				with(failing).with(row{"last_failure_category": category}))
		}
	})

	t.Run("one active source per host and user name", func(t *testing.T) {
		first := mustInsert(t, db, "imap_source", activeSource("mail.example.test", "Anna@Example.test"))
		refused(t, db, "imap_source", activeSource("mail.example.test", "anna@example.test"), "UNIQUE constraint")

		if _, err := db.Writer.Exec("UPDATE imap_source SET removed_at = 20, sealed_password = NULL WHERE id = ?", first); err != nil {
			t.Fatal(err)
		}
		mustInsert(t, db, "imap_source", activeSource("mail.example.test", "anna@example.test"))
	})
}

func TestFolderAndProblemRowRules(t *testing.T) {
	db, _ := openCatalog(t)
	sourceID := mustInsert(t, db, "imap_source", activeSource("imap.example.test", "anna@example.test"))
	folder := chosenFolder(sourceID, "INBOX")

	refused(t, db, "imap_folder", folder.with(row{"last_uid": 5}), "CHECK constraint")
	refused(t, db, "imap_folder", folder.with(row{"chosen": 2}), "CHECK constraint")

	folderID := mustInsert(t, db, "imap_folder", folder.with(row{"uid_validity": 7, "last_uid": 5}))
	refused(t, db, "imap_folder", folder, "UNIQUE constraint")
	mustInsert(t, db, "imap_folder", chosenFolder(sourceID, "Archiv").with(row{"chosen": 0}))

	problem := row{"folder_id": folderID, "uid_validity": 7, "uid": 3, "recorded_at": 10}
	refused(t, db, "imap_message_problem", problem.with(row{"kind": "other"}), "CHECK constraint")
	for uid, kind := range []string{"waiting_for_retry", "too_large", "removed"} {
		mustInsert(t, db, "imap_message_problem", problem.with(row{"kind": kind, "uid": uid + 1}))
	}
	refused(t, db, "imap_message_problem", problem.with(row{"kind": "removed", "uid": 1}), "UNIQUE constraint")
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

func TestInTxCommitsOnlyWhenTheFunctionSucceeds(t *testing.T) {
	db, _ := openCatalog(t)
	ctx := context.Background()
	if _, err := db.Writer.Exec("CREATE TABLE note (text TEXT NOT NULL) STRICT"); err != nil {
		t.Fatal(err)
	}
	count := func() int {
		t.Helper()
		var n int
		if err := db.Reader.QueryRow("SELECT COUNT(*) FROM note").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}

	err := InTx(ctx, db.Writer, func(tx *sql.Tx) error {
		_, err := tx.Exec("INSERT INTO note (text) VALUES ('kept')")
		return err
	})
	if err != nil || count() != 1 {
		t.Fatalf("InTx = %v with %d rows, want success and 1 row", err, count())
	}

	failure := errors.New("the function failed")
	err = InTx(ctx, db.Writer, func(tx *sql.Tx) error {
		if _, err := tx.Exec("INSERT INTO note (text) VALUES ('dropped')"); err != nil {
			return err
		}
		return failure
	})
	if !errors.Is(err, failure) {
		t.Fatalf("InTx = %v, want the function's error", err)
	}
	if strings.Contains(err.Error(), "already been committed or rolled back") {
		t.Errorf("InTx reported a second rollback: %v", err)
	}
	if count() != 1 {
		t.Fatalf("rows = %d, want the failed insert rolled back", count())
	}
}

func TestInTxReportsAFailedBegin(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	ran := false
	err = InTx(context.Background(), db.Writer, func(*sql.Tx) error { ran = true; return nil })
	if err == nil || ran {
		t.Fatalf("InTx on a closed catalog = %v, ran = %v, want an error and no run", err, ran)
	}
}
