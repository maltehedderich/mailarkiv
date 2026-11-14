package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// RFC 3339 timestamps, reasoned from testNow: 15:50:52.123 UTC.
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
			"sources": [],
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

	// A message event names its source, removed or not, and never the folder.
	t.Run("message events name their source", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)
		for _, statement := range []string{
			`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
			 VALUES (3, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0)`,
			`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at, removed_at)
			 VALUES (5, 'Büro <b>', 'imap.example.test', 993, 'tls', 'office@example.test', 'office@example.test', NULL, 0, 1)`,
			`INSERT INTO imap_folder (id, source_id, name, chosen) VALUES (1, 3, 'Geheimordner', 1), (2, 5, 'Geheimordner', 1)`,
		} {
			if _, err := h.db.Writer.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}
		anna, office := int64(3), int64(5)
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.MessageTooLarge, SourceID: &anna})
		h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.MessageRemoved, SourceID: &office})

		response, body := h.do(t, http.MethodGet, "/api/status", session)

		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %[1]q,
			"recovery": null,
			"sources": [],
			"unreferenced": [],
			"failures": [
				{"at": %[1]q, "category": "message_too_large", "source_id": 3, "source_name": "Anna", "restored": false},
				{"at": %[1]q, "category": "message_removed", "source_id": 5, "source_name": "Büro <b>", "restored": false}
			]
		}`, testTime))
		if strings.Contains(body, "Geheimordner") {
			t.Error("the status names a folder")
		}
	})

	// Contract: each problem source appears once, by name, with its category,
	// when the problem began, and its failed attempts for a sync failure. No
	// entry names a user, a folder, or the server's reply.
	t.Run("problem sources", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, account)
		session := h.signIn(t, account)
		const (
			user   = "MARKER-USER-6b1a@example.test"
			folder = "MARKER-FOLDER-2c7e"
			reply  = "MARKER-REPLY-9d2e"
		)
		failingSince, unavailableSince := testNow.Add(-30*time.Minute), testNow.Add(-50*time.Minute)
		for _, statement := range []string{
			fmt.Sprintf(`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at,
			             failed_attempts, failing_since, last_failure_category, last_failure_at, last_server_reply)
			 VALUES (3, 'Anna', 'imap.example.test', 993, 'tls', '%[1]s', lower('%[1]s'), x'00', 0, 3, %[2]d, 'timed_out', %[2]d, '%[3]s')`,
				user, failingSince.UnixMilli(), reply),
			fmt.Sprintf(`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
			 VALUES (5, 'Office', 'imap.example.test', 993, 'tls', 'office-%[1]s', lower('office-%[1]s'), x'00', 0),
			        (7, 'Healthy', 'imap.example.test', 993, 'tls', 'ok-%[1]s', lower('ok-%[1]s'), x'00', 0)`, user),
			fmt.Sprintf(`INSERT INTO imap_folder (id, source_id, name, chosen, unavailable_since)
			 VALUES (1, 3, '%[1]s', 1, NULL), (2, 5, '%[1]s', 1, %[2]d), (3, 7, '%[1]s', 1, NULL)`, folder, unavailableSince.UnixMilli()),
		} {
			if _, err := h.db.Writer.Exec(statement); err != nil {
				t.Fatal(err)
			}
		}

		response, body := h.do(t, http.MethodGet, "/api/status", session)

		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %[1]q,
			"recovery": null,
			"sources": [
				{"id": 3, "name": "Anna", "category": "timed_out", "since": %[2]q, "failed_attempts": 3},
				{"id": 5, "name": "Office", "category": "folder_unavailable", "since": %[3]q, "failed_attempts": null}
			],
			"unreferenced": [],
			"failures": []
		}`, testTime, formatTime(failingSince), formatTime(unavailableSince)))
		for _, marker := range []string{"MARKER-USER", folder, reply} {
			if strings.Contains(strings.ToUpper(body), marker) {
				t.Errorf("the status holds %s: %s", marker, body)
			}
		}
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
			"sources": [],
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
			"sources": [],
			"unreferenced": [{"content_hash": %q, "size": %d}],
			"failures": []
		}`, testTime, recovery, hash, len(content)))
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":1,"expires_in":43200}`)

		response, body = h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
		}

		response, body = h.do(t, http.MethodGet, "/api/status", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %q,
			"recovery": %s,
			"sources": [],
			"unreferenced": [],
			"failures": []
		}`, testTime, recovery))
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":0,"expires_in":43200}`)
	})

	// No endpoint here changes the archive.
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

// The count is failure events plus unreferenced originals.
func TestProblemCountAddsFailuresAndUnreferenced(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	assertCount := func(t *testing.T, want string) {
		t.Helper()
		response, body := h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, want)
	}

	assertCount(t, `{"problem_count":0,"expires_in":43200}`)

	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.TempFileNotRemoved})
	assertCount(t, `{"problem_count":2,"expires_in":43200}`)

	first := hashOf([]byte("first"))
	for _, hash := range []string{first, hashOf([]byte("second")), hashOf([]byte("third"))} {
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hash, Size: 1})
	}
	assertCount(t, `{"problem_count":5,"expires_in":43200}`)

	h.status.RemoveUnreferenced(first)
	assertCount(t, `{"problem_count":4,"expires_in":43200}`)
}

// Contract: the problem count adds one for each problem source on the status
// screen, however many problems the source has.
func TestProblemCountAddsProblemSources(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})

	for _, statement := range []string{
		// A failing source with an unavailable folder and a waiting message
		// counts once.
		`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at,
		                          failed_attempts, failing_since, last_failure_category, last_failure_at)
		 VALUES (3, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0,
		         2, 0, 'server_error', 0)`,
		`INSERT INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		 VALUES (5, 'Office', 'imap.example.test', 993, 'tls', 'office@example.test', 'office@example.test', x'00', 0),
		        (7, 'Healthy', 'imap.example.test', 993, 'tls', 'ok@example.test', 'ok@example.test', x'00', 0)`,
		`INSERT INTO imap_folder (id, source_id, name, chosen, uid_validity, unavailable_since)
		 VALUES (1, 3, 'INBOX', 1, 7, 0), (2, 5, 'INBOX', 1, 7, NULL), (3, 7, 'INBOX', 1, 7, NULL)`,
		`INSERT INTO imap_message_problem (folder_id, uid_validity, uid, kind, recorded_at)
		 VALUES (1, 7, 1, 'waiting_for_retry', 0), (2, 7, 1, 'waiting_for_retry', 0), (3, 7, 1, 'too_large', 0)`,
	} {
		if _, err := h.db.Writer.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}

	response, body := h.do(t, http.MethodGet, "/api/session", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":3,"expires_in":43200}`)
}
