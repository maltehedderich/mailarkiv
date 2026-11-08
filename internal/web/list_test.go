package web

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"strconv"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// With 10,000 messages, every page answers in under 2 seconds, and walking
// the pages reaches every message once, most recent import first.
func TestListAtTenThousand(t *testing.T) {
	const messages = 10_000
	const budget = 2 * time.Second

	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	seedMessages(t, h, messages)

	type listed struct {
		Items []struct {
			ID         int64  `json:"id"`
			ImportedAt string `json:"imported_at"`
		} `json:"items"`
		PageSize int `json:"page_size"`
		Total    int `json:"total"`
	}

	seen := make(map[int64]bool, messages)
	var previous time.Time
	pages := 0
	for page := 1; ; page++ {
		started := time.Now()
		response, body := h.do(t, http.MethodGet, "/api/messages?page="+strconv.Itoa(page), session)
		if took := time.Since(started); took >= budget {
			t.Errorf("page %d took %v, want under %v", page, took, budget)
		}
		if response.StatusCode != http.StatusOK {
			t.Fatalf("page %d: status %d %s", page, response.StatusCode, body)
		}
		var got listed
		if err := json.Unmarshal([]byte(body), &got); err != nil {
			t.Fatalf("page %d: %v", page, err)
		}
		if got.Total != messages {
			t.Fatalf("page %d: total = %d, want %d", page, got.Total, messages)
		}
		if len(got.Items) == 0 {
			break
		}
		if len(got.Items) > got.PageSize {
			t.Fatalf("page %d has %d items, more than its page size %d", page, len(got.Items), got.PageSize)
		}
		pages++
		for _, item := range got.Items {
			if seen[item.ID] {
				t.Fatalf("page %d lists message %d again", page, item.ID)
			}
			seen[item.ID] = true
			at, err := time.Parse(time.RFC3339Nano, item.ImportedAt)
			if err != nil {
				t.Fatalf("page %d: imported_at %q: %v", page, item.ImportedAt, err)
			}
			// Each seeded import has its own time, so most recent first is
			// strictly decreasing.
			if !previous.IsZero() && !at.Before(previous) {
				t.Fatalf("page %d: message %d imported at %v follows %v", page, item.ID, at, previous)
			}
			previous = at
		}
	}

	if len(seen) != messages {
		t.Fatalf("the pages listed %d messages, want %d", len(seen), messages)
	}
	if want := messages / 50; pages != want {
		t.Errorf("walked %d pages, want %d", pages, want)
	}
}

// seedMessages writes count messages straight to the catalog in one
// transaction, each with one import a second after the last. Real imports
// would take minutes, and archtest ignores test files. Three statements
// instead of three per message, because the race detector slows each one.
func seedMessages(t *testing.T, h *harness, count int) {
	t.Helper()
	first := testNow.Add(-time.Duration(count) * time.Second).UnixMilli()
	err := sqlitedb.InTx(context.Background(), h.db.Writer, func(tx *sql.Tx) error {
		for _, step := range []struct {
			query string
			args  []any
		}{
			{`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			  INSERT INTO message (id, content_hash, size)
			  SELECT i, printf('%064x', i), 100 + i FROM n`, []any{count}},
			{`WITH RECURSIVE n(i) AS (SELECT 1 UNION ALL SELECT i + 1 FROM n WHERE i < ?)
			  INSERT INTO source_observation (id, message_id, source_type, observed_at, account_email, file_name)
			  SELECT i, i, 'manual_import', ? + i * 1000, 'admin@example.test', printf('seed-%05d.eml', i) FROM n`, []any{count, first}},
			{`UPDATE message SET latest_observation_id = id`, nil},
		} {
			if _, err := tx.Exec(step.query, step.args...); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
