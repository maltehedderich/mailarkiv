package archive

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// slice1Entry is one import recorded by a Slice 1 binary.
type slice1Entry struct {
	id           int64
	messageID    int64
	observedAt   int64
	accountEmail string
	fileName     *string
}

// buildSlice1Catalog writes the catalog a Slice 1 binary leaves behind: the
// first migration only, user_version 1, and rows written by hand.
func buildSlice1Catalog(t *testing.T, dataDir string, bodies map[int64][]byte, entries []slice1Entry) {
	t.Helper()
	schema, err := os.ReadFile(filepath.Join("..", "sqlitedb", "migrations", "0001_initial.sql"))
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec("PRAGMA user_version = 1"); err != nil {
		t.Fatal(err)
	}

	latest := map[int64]int64{}
	for _, entry := range entries {
		latest[entry.messageID] = max(latest[entry.messageID], entry.id)
	}
	for id, body := range bodies {
		if _, err := raw.Exec("INSERT INTO message (id, content_hash, size) VALUES (?, ?, ?)", id, hashOf(body), len(body)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(dataDir, filepath.FromSlash(blobPath(hashOf(body))))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range entries {
		_, err := raw.Exec(`INSERT INTO source_observation (id, message_id, source_type, observed_at, account_email, file_name)
			VALUES (?, ?, 'manual_import', ?, ?, ?)`,
			entry.id, entry.messageID, entry.observedAt, entry.accountEmail, entry.fileName)
		if err != nil {
			t.Fatal(err)
		}
	}
	for messageID, observationID := range latest {
		if _, err := raw.Exec("UPDATE message SET latest_observation_id = ? WHERE id = ?", observationID, messageID); err != nil {
			t.Fatal(err)
		}
	}
}

// Migration 0002 rebuilds source_observation, which message references. A
// catalog with imports must come through with its list, history, and stored
// originals unchanged.
func TestMigrateSlice1Catalog(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	bodies := map[int64][]byte{
		1: []byte("From: anna@example.test\r\nSubject: Invoice\r\n\r\nInvoice\r\n"),
		2: []byte("From: ben@example.test\r\nSubject: Notes\r\n\r\nNotes\r\n"),
		3: []byte("From: cara@example.test\r\nSubject: Report\r\n\r\nReport\r\n"),
	}
	entries := []slice1Entry{
		{id: 1, messageID: 1, observedAt: 1_000, accountEmail: "admin@example.test", fileName: name("invoice.eml")},
		{id: 2, messageID: 2, observedAt: 2_000, accountEmail: "admin@example.test", fileName: nil},
		{id: 3, messageID: 3, observedAt: 3_000, accountEmail: "other@example.test", fileName: name("report.eml")},
		{id: 4, messageID: 1, observedAt: 4_000, accountEmail: "other@example.test", fileName: name("invoice-again.eml")},
		{id: 5, messageID: 3, observedAt: 5_000, accountEmail: "admin@example.test", fileName: name("report.eml")},
	}
	buildSlice1Catalog(t, dataDir, bodies, entries)

	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatalf("sqlitedb.Open on a Slice 1 catalog: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	clock := func() time.Time { return testNow }
	archive, err := Open(dataDir, db, opstatus.New(logger, clock), logger, clock)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("the list keeps its messages and counts", func(t *testing.T) {
		page, err := archive.List(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		// Newest list date first. Until processing reads a message, its list
		// date is its first entry's time: message 3 (3,000), 2 (2,000), 1 (1,000).
		want := []Message{
			{ID: 3, ContentHash: hashOf(bodies[3]), Size: int64(len(bodies[3])), ObservationCount: 2,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(5_000).UTC(), AccountEmail: "admin@example.test", FileName: name("report.eml")}},
			{ID: 2, ContentHash: hashOf(bodies[2]), Size: int64(len(bodies[2])), ObservationCount: 1,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(2_000).UTC(), AccountEmail: "admin@example.test", FileName: nil}},
			{ID: 1, ContentHash: hashOf(bodies[1]), Size: int64(len(bodies[1])), ObservationCount: 2,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(4_000).UTC(), AccountEmail: "other@example.test", FileName: name("invoice-again.eml")}},
		}
		if page.Total != 3 || len(page.Items) != len(want) {
			t.Fatalf("page = %+v, want 3 items and a total of 3", page)
		}
		for i := range want {
			if !equalMessages(page.Items[i], want[i]) {
				t.Errorf("item %d = %+v, want %+v", i, page.Items[i], want[i])
			}
		}
	})

	t.Run("history keeps every entry", func(t *testing.T) {
		for messageID := range bodies {
			history, err := archive.History(ctx, messageID)
			if err != nil {
				t.Fatal(err)
			}
			var want []slice1Entry
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i].messageID == messageID {
					want = append(want, entries[i])
				}
			}
			if len(history.Entries) != len(want) {
				t.Fatalf("message %d has %d entries, want %d", messageID, len(history.Entries), len(want))
			}
			for i, entry := range want {
				got := history.Entries[i]
				imported := ImportEntry{ObservedAt: time.UnixMilli(entry.observedAt), AccountEmail: entry.accountEmail, FileName: entry.fileName}
				if !sameEntry(got, imported) {
					t.Errorf("message %d entry %d = %+v, want %+v", messageID, i, got, entry)
				}
			}
		}
	})

	t.Run("stored originals are unchanged", func(t *testing.T) {
		for messageID, body := range bodies {
			original, err := archive.OpenVerified(ctx, messageID)
			if err != nil {
				t.Fatalf("message %d: %v", messageID, err)
			}
			got, err := io.ReadAll(original.File)
			original.File.Close()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(body) {
				t.Errorf("message %d reads %q, want %q", messageID, got, body)
			}
		}
	})

	t.Run("latest entries point at the same rows", func(t *testing.T) {
		for messageID, want := range map[int64]int64{1: 4, 2: 2, 3: 5} {
			var latest int64
			if err := db.Reader.QueryRow("SELECT latest_observation_id FROM message WHERE id = ?", messageID).Scan(&latest); err != nil {
				t.Fatal(err)
			}
			if latest != want {
				t.Errorf("message %d latest_observation_id = %d, want %d", messageID, latest, want)
			}
		}
	})

	t.Run("references hold", func(t *testing.T) {
		rows, err := db.Reader.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if rows.Next() {
			t.Error("foreign_key_check found a broken reference")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		_, err = db.Writer.Exec(`INSERT INTO source_observation (message_id, source_type, observed_at, account_email)
			VALUES (999, 'manual_import', 1, 'admin@example.test')`)
		if err == nil {
			t.Error("the catalog accepted an entry for message 999")
		}
	})
}

func equalNames(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// slice2Entry is one entry recorded by a Slice 2 binary: an import, or a
// collection when sourceID is set.
type slice2Entry struct {
	id           int64
	messageID    int64
	observedAt   int64
	accountEmail string  // Imports only.
	fileName     *string // Imports only.
	sourceID     int64   // Collections only.
	folderID     int64   // Collections only.
	receivedAt   *int64  // Collections only.
}

func (e slice2Entry) want() Entry {
	if e.sourceID == 0 {
		return ImportEntry{ObservedAt: time.UnixMilli(e.observedAt), AccountEmail: e.accountEmail, FileName: e.fileName}
	}
	entry := CollectionEntry{ObservedAt: time.UnixMilli(e.observedAt), SourceID: e.sourceID, FolderID: e.folderID}
	if e.receivedAt != nil {
		received := time.UnixMilli(*e.receivedAt)
		entry.ReceivedAt = &received
	}
	return entry
}

// buildSlice2Catalog writes the catalog a Slice 2 binary leaves behind: the
// first two migrations, user_version 2, and rows written by hand. The
// message sequence is set to sequence.
func buildSlice2Catalog(t *testing.T, dataDir string, bodies map[int64][]byte, entries []slice2Entry, sequence int64) {
	t.Helper()
	raw, err := sql.Open("sqlite", filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		if _, err := raw.Exec(query, args...); err != nil {
			t.Fatalf("%s: %v", query, err)
		}
	}
	for _, file := range []string{"0001_initial.sql", "0002_imap_collection.sql"} {
		schema, err := os.ReadFile(filepath.Join("..", "sqlitedb", "migrations", file))
		if err != nil {
			t.Fatal(err)
		}
		exec(string(schema))
	}
	exec("PRAGMA user_version = 2")

	exec(`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		VALUES (1, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0)`)
	exec(`INSERT INTO imap_folder (id, source_id, name, chosen, uid_validity, last_uid) VALUES (1, 1, 'INBOX', 1, 7, 2)`)

	latest := map[int64]int64{}
	collected := map[int64]bool{}
	for _, entry := range entries {
		latest[entry.messageID] = max(latest[entry.messageID], entry.id)
		if entry.sourceID != 0 {
			collected[entry.messageID] = true
		}
	}
	for id, body := range bodies {
		exec("INSERT INTO message (id, content_hash, size) VALUES (?, ?, ?)", id, hashOf(body), len(body))
		path := filepath.Join(dataDir, filepath.FromSlash(blobPath(hashOf(body))))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range entries {
		if entry.sourceID == 0 {
			exec(`INSERT INTO source_observation (id, message_id, source_type, observed_at, account_email, file_name)
				VALUES (?, ?, 'manual_import', ?, ?, ?)`,
				entry.id, entry.messageID, entry.observedAt, entry.accountEmail, entry.fileName)
			continue
		}
		exec(`INSERT INTO source_observation (id, message_id, source_type, observed_at, imap_source_id, imap_folder_id, received_at)
			VALUES (?, ?, 'imap_collection', ?, ?, ?, ?)`,
			entry.id, entry.messageID, entry.observedAt, entry.sourceID, entry.folderID, entry.receivedAt)
	}
	for messageID, observationID := range latest {
		exec("UPDATE message SET latest_observation_id = ? WHERE id = ?", observationID, messageID)
	}
	exec("INSERT INTO collected_message_count (imap_source_id, messages) VALUES (1, ?)", len(collected))
	exec("UPDATE sqlite_sequence SET seq = ? WHERE name = 'message'", sequence)
}

// Migration 0003 rebuilds message, which source_observation references. A
// catalog with imports and collections must come through with its history
// and stored originals unchanged, and every message waiting to be processed.
func TestMigrateSlice2Catalog(t *testing.T) {
	ctx := context.Background()
	dataDir := t.TempDir()
	bodies := map[int64][]byte{
		1: []byte("From: anna@example.test\r\nSubject: Invoice\r\n\r\nInvoice\r\n"),
		2: []byte("From: ben@example.test\r\nSubject: Notes\r\n\r\nNotes\r\n"),
		3: []byte("From: cara@example.test\r\nSubject: Report\r\n\r\nReport\r\n"),
	}
	received := int64(500)
	// Message 1 has two imports and a collection, message 2 only a
	// collection, and message 3 only an import.
	entries := []slice2Entry{
		{id: 1, messageID: 1, observedAt: 1_000, accountEmail: "admin@example.test", fileName: name("invoice.eml")},
		{id: 2, messageID: 3, observedAt: 2_000, accountEmail: "admin@example.test"},
		{id: 3, messageID: 1, observedAt: 3_000, sourceID: 1, folderID: 1, receivedAt: &received},
		{id: 4, messageID: 2, observedAt: 4_000, sourceID: 1, folderID: 1},
		{id: 5, messageID: 1, observedAt: 6_000, accountEmail: "other@example.test", fileName: name("invoice-again.eml")},
	}
	// The sequence runs ahead of the highest ID, as after a row deleted by
	// hand, so a sequence recomputed from the rows would hand out ID 4 again.
	const sequence = 10
	buildSlice2Catalog(t, dataDir, bodies, entries, sequence)

	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatalf("sqlitedb.Open on a Slice 2 catalog: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	clock := func() time.Time { return testNow }
	archive, err := Open(dataDir, db, opstatus.New(logger, clock), logger, clock)
	if err != nil {
		t.Fatal(err)
	}

	t.Run("every message waits to be processed, dated by its first entry", func(t *testing.T) {
		earliest := map[int64]int64{1: 1_000, 2: 4_000, 3: 2_000}
		for messageID, want := range earliest {
			var firstArchived, listDate int64
			var state string
			var seq sql.NullInt64
			err := db.Reader.QueryRow(
				"SELECT first_archived_at, list_date, processing_state, processed_seq FROM message WHERE id = ?",
				messageID).Scan(&firstArchived, &listDate, &state, &seq)
			if err != nil {
				t.Fatal(err)
			}
			if firstArchived != want || listDate != want || state != "not_processed" || seq.Valid {
				t.Errorf("message %d: first_archived_at %d, list_date %d, %s, processed_seq %v; want %d, %d, not_processed, NULL",
					messageID, firstArchived, listDate, state, seq, want, want)
			}
		}
	})

	t.Run("the list holds every message in list date order", func(t *testing.T) {
		page, err := archive.List(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		want := []Message{
			{ID: 2, ContentHash: hashOf(bodies[2]), Size: int64(len(bodies[2])), ObservationCount: 1, Latest: entries[3].want()},
			{ID: 3, ContentHash: hashOf(bodies[3]), Size: int64(len(bodies[3])), ObservationCount: 1, Latest: entries[1].want()},
			{ID: 1, ContentHash: hashOf(bodies[1]), Size: int64(len(bodies[1])), ObservationCount: 3, Latest: entries[4].want()},
		}
		if page.Total != 3 || len(page.Items) != len(want) {
			t.Fatalf("page = %+v, want 3 items and a total of 3", page)
		}
		for i := range want {
			if !equalMessages(page.Items[i], want[i]) {
				t.Errorf("item %d = %+v, want %+v", i, page.Items[i], want[i])
			}
		}
	})

	t.Run("history keeps every entry", func(t *testing.T) {
		for messageID := range bodies {
			history, err := archive.History(ctx, messageID)
			if err != nil {
				t.Fatal(err)
			}
			var want []Entry
			for i := len(entries) - 1; i >= 0; i-- {
				if entries[i].messageID == messageID {
					want = append(want, entries[i].want())
				}
			}
			if len(history.Entries) != len(want) {
				t.Fatalf("message %d has %d entries, want %d", messageID, len(history.Entries), len(want))
			}
			for i := range want {
				if !sameEntry(history.Entries[i], want[i]) {
					t.Errorf("message %d entry %d = %+v, want %+v", messageID, i, history.Entries[i], want[i])
				}
			}
		}
	})

	t.Run("latest entries point at the same rows", func(t *testing.T) {
		for messageID, want := range map[int64]int64{1: 5, 2: 4, 3: 2} {
			var latest int64
			if err := db.Reader.QueryRow("SELECT latest_observation_id FROM message WHERE id = ?", messageID).Scan(&latest); err != nil {
				t.Fatal(err)
			}
			if latest != want {
				t.Errorf("message %d latest_observation_id = %d, want %d", messageID, latest, want)
			}
		}
	})

	t.Run("the collected count is unchanged", func(t *testing.T) {
		counts, err := archive.CollectedCounts(ctx, []int64{1})
		if err != nil {
			t.Fatal(err)
		}
		if counts[1] != 2 {
			t.Errorf("source 1 collected %d, want 2", counts[1])
		}
	})

	t.Run("stored originals are unchanged", func(t *testing.T) {
		for messageID, body := range bodies {
			original, err := archive.OpenVerified(ctx, messageID)
			if err != nil {
				t.Fatalf("message %d: %v", messageID, err)
			}
			got, err := io.ReadAll(original.File)
			original.File.Close()
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != string(body) {
				t.Errorf("message %d reads %q, want %q", messageID, got, body)
			}
		}
	})

	t.Run("references hold", func(t *testing.T) {
		rows, err := db.Reader.Query("PRAGMA foreign_key_check")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		if rows.Next() {
			t.Error("foreign_key_check found a broken reference")
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}

		_, err = db.Writer.Exec(`INSERT INTO source_observation (message_id, source_type, observed_at, account_email)
			VALUES (999, 'manual_import', 1, 'admin@example.test')`)
		if err == nil {
			t.Error("the catalog accepted an entry for message 999")
		}
	})

	t.Run("the list's indexes", func(t *testing.T) {
		indexes := map[string]bool{}
		rows, err := db.Reader.Query("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'message'")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		for rows.Next() {
			var index string
			if err := rows.Scan(&index); err != nil {
				t.Fatal(err)
			}
			indexes[index] = true
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		if indexes["message_by_latest_observation"] {
			t.Error("message_by_latest_observation is still there")
		}
		for _, index := range []string{"message_list_order", "message_with_problems", "message_by_processed_seq", "message_not_processed"} {
			if !indexes[index] {
				t.Errorf("%s is missing, the indexes are %v", index, indexes)
			}
		}
	})

	t.Run("a new capture gets a new ID", func(t *testing.T) {
		result, err := archive.Import(ctx, bytes.NewReader([]byte("Subject: New\r\n\r\nNew\r\n")), ImportMeta{AccountEmail: account})
		if err != nil {
			t.Fatal(err)
		}
		stored, ok := result.(Stored)
		if !ok {
			t.Fatalf("result = %#v, want Stored", result)
		}
		if stored.Message.ID <= sequence {
			t.Errorf("new message ID = %d, want above %d", stored.Message.ID, sequence)
		}
	})
}
