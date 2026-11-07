package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// Times in the status contract's form, reasoned from testNow: 15:50:52.123 UTC.
const (
	testNowPlus410ms = "2026-11-06T15:50:52.533Z"
	testNowPlus20min = "2026-11-06T16:10:52.123Z"
)

func TestStatus(t *testing.T) {
	const account = "admin@example.test"

	t.Run("every field in the contract's form", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)

		hashOne, hashTwo := hashOf([]byte("unreferenced one")), hashOf([]byte("unreferenced two"))
		h.status.RecordRecovery(opstatus.RecoveryResult{
			FinishedAt:          testNow.Add(410 * time.Millisecond),
			TempFilesRemoved:    1,
			TempFilesNotRemoved: 3,
			UnreferencedFound:   2,
			Duration:            410 * time.Millisecond,
		})
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hashOne, Size: 2048})
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hashTwo, Size: 150_000_000})

		h.clock.Advance(20 * time.Minute)
		damaged, missing := int64(17), int64(18)
		hashDamaged, hashMissing, hashCatalog := hashOf([]byte("damaged")), hashOf([]byte("missing")), hashOf([]byte("catalog"))
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StoredOriginalDamaged, MessageID: &damaged, ContentHash: &hashDamaged})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StoredOriginalMissing, MessageID: &missing, ContentHash: &hashMissing, Restored: true})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.CatalogWriteFailed, ContentHash: &hashCatalog})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageWriteFailed})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.TempFileNotRemoved})

		response, body := h.do(t, http.MethodGet, "/api/status", session)

		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %[1]q,
			"recovery": {
				"finished_at": %[2]q,
				"temp_files_removed": 1,
				"temp_files_not_removed": 3,
				"unreferenced_found": 2
			},
			"unreferenced": [
				{"content_hash": %[4]q, "size": 2048},
				{"content_hash": %[5]q, "size": 150000000}
			],
			"failures": [
				{"at": %[3]q, "category": "stored_original_damaged", "message_id": 17, "content_hash": %[6]q, "restored": false},
				{"at": %[3]q, "category": "stored_original_missing", "message_id": 18, "content_hash": %[7]q, "restored": true},
				{"at": %[3]q, "category": "catalog_write_failed", "content_hash": %[8]q, "restored": false},
				{"at": %[3]q, "category": "storage_full", "restored": false},
				{"at": %[3]q, "category": "storage_write_failed", "restored": false},
				{"at": %[3]q, "category": "temp_file_not_removed", "restored": false}
			]
		}`, testTime, testNowPlus410ms, testNowPlus20min, hashOne, hashTwo, hashDamaged, hashMissing, hashCatalog))
	})

	t.Run("empty lists are empty arrays", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)
		h.status.RecordRecovery(opstatus.RecoveryResult{FinishedAt: testNow})

		response, body := h.do(t, http.MethodGet, "/api/status", session)

		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %[1]q,
			"recovery": {"finished_at": %[1]q, "temp_files_removed": 0, "temp_files_not_removed": 0, "unreferenced_found": 0},
			"unreferenced": [],
			"failures": []
		}`, testTime))
	})

	// Contract: recovery holds what startup found, and unreferenced is the
	// current list, which shrinks as files are imported again.
	t.Run("what recovery found stays, and the current list shrinks", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)

		content := mail("unreferenced")
		hash := hashOf(content)
		h.writeBlob(t, hash, content)
		if err := os.WriteFile(filepath.Join(h.dataDir, "tmp", "up-left-behind"), []byte("Subject: partial"), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := h.archive.Recover(context.Background()); err != nil {
			t.Fatalf("Recover: %v", err)
		}
		recovery := fmt.Sprintf(`{"finished_at": %q, "temp_files_removed": 1, "temp_files_not_removed": 0, "unreferenced_found": 1}`, testTime)

		response, body := h.do(t, http.MethodGet, "/api/status", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %q,
			"recovery": %s,
			"unreferenced": [{"content_hash": %q, "size": %d}],
			"failures": []
		}`, testTime, recovery, hash, len(content)))
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":1}`)

		response, body = h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
		}

		response, body = h.do(t, http.MethodGet, "/api/status", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %q,
			"recovery": %s,
			"unreferenced": [],
			"failures": []
		}`, testTime, recovery))
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":0}`)
	})

	// FR-027: no endpoint here changes the archive.
	t.Run("reading the status changes nothing", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)
		response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(mail("kept")), session)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
		}
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hashOf([]byte("kept off the list")), Size: 1})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})
		before := h.counts(t)

		response, body = h.do(t, http.MethodGet, "/api/status", session)

		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/status = %d %s, want 200", response.StatusCode, body)
		}
		if after := h.counts(t); after != before {
			t.Errorf("the archive went from %+v to %+v", before, after)
		}
		if got := h.status.ProblemCount(); got != 2 {
			t.Errorf("problem count = %d, want the 2 seeded problems", got)
		}
	})
}

// FR-039: the count is failure events plus unreferenced originals.
func TestProblemCountAddsFailuresAndUnreferenced(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	assertCount := func(t *testing.T, want string) {
		t.Helper()
		response, body := h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, want)
	}

	assertCount(t, `{"problem_count":0}`)

	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.TempFileNotRemoved})
	assertCount(t, `{"problem_count":2}`)

	first := hashOf([]byte("first"))
	for _, hash := range []string{first, hashOf([]byte("second")), hashOf([]byte("third"))} {
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hash, Size: 1})
	}
	assertCount(t, `{"problem_count":5}`)

	h.status.RemoveUnreferenced(first)
	assertCount(t, `{"problem_count":4}`)
}
