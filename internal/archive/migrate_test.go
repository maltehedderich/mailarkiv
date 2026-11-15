package archive

import (
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

	t.Run("the list keeps its order and counts", func(t *testing.T) {
		page, err := archive.List(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		// Newest latest entry first: message 3 (entry 5), 1 (entry 4), 2 (entry 2).
		want := []Message{
			{ID: 3, ContentHash: hashOf(bodies[3]), Size: int64(len(bodies[3])), ObservationCount: 2,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(5_000).UTC(), AccountEmail: "admin@example.test", FileName: name("report.eml")}},
			{ID: 1, ContentHash: hashOf(bodies[1]), Size: int64(len(bodies[1])), ObservationCount: 2,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(4_000).UTC(), AccountEmail: "other@example.test", FileName: name("invoice-again.eml")}},
			{ID: 2, ContentHash: hashOf(bodies[2]), Size: int64(len(bodies[2])), ObservationCount: 1,
				Latest: ImportEntry{ObservedAt: time.UnixMilli(2_000).UTC(), AccountEmail: "admin@example.test", FileName: nil}},
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
