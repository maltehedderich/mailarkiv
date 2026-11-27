package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/search"
)

// RFC 3339 timestamps, reasoned from testNow: 15:50:52.123 UTC.
const (
	testNowPlus410ms = "2026-11-06T15:50:52.533Z"
	testNowPlus20min = "2026-11-06T16:10:52.123Z"
)

// idleSearch is the search object of a search module that never started, as
// in a harness without a search program: Unavailable with no wait, nothing
// read yet, and the thresholds of 2 GiB and 3 GiB.
const idleSearch = `{
	"state": "unavailable", "next_start_at": null, "needs_rebuild": null, "restarts": 0,
	"indexing": {"state": "stopped", "waiting": 0, "done": null, "to_do": null, "free_bytes": null,
		"pause_below_bytes": 2147483648, "resume_at_bytes": 3221225472},
	"rebuild": null
}`

// noProcessing is the processing object with nothing to count.
const noProcessing = `{"not_processed": 0, "processed_with_problems": 0, "unreadable": 0}`

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
			"processing": %[9]s,
			"search": %[10]s,
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
				{"at": %[3]q, "category": "stored_original_damaged", "message_id": 17, "content_hash": %[6]q, "restored": false, "repeats": 1, "last_at": %[3]q},
				{"at": %[3]q, "category": "stored_original_missing", "message_id": 18, "content_hash": %[7]q, "restored": true, "repeats": 1, "last_at": %[3]q},
				{"at": %[3]q, "category": "catalog_write_failed", "content_hash": %[8]q, "restored": false, "repeats": 1, "last_at": %[3]q},
				{"at": %[3]q, "category": "storage_full", "restored": false, "repeats": 1, "last_at": %[3]q},
				{"at": %[3]q, "category": "storage_write_failed", "restored": false, "repeats": 1, "last_at": %[3]q},
				{"at": %[3]q, "category": "temp_file_not_removed", "restored": false, "repeats": 1, "last_at": %[3]q}
			]
		}`, testTime, testNowPlus410ms, testNowPlus20min, hashOne, hashTwo, hashDamaged, hashMissing, hashCatalog, noProcessing, idleSearch))
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
			"processing": %[2]s,
			"search": %[3]s,
			"recovery": null,
			"sources": [],
			"unreferenced": [],
			"failures": [
				{"at": %[1]q, "category": "message_too_large", "source_id": 3, "source_name": "Anna", "restored": false, "repeats": 1, "last_at": %[1]q},
				{"at": %[1]q, "category": "message_removed", "source_id": 5, "source_name": "Büro <b>", "restored": false, "repeats": 1, "last_at": %[1]q}
			]
		}`, testTime, noProcessing, idleSearch))
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
			"processing": %[4]s,
			"search": %[5]s,
			"recovery": null,
			"sources": [
				{"id": 3, "name": "Anna", "category": "timed_out", "since": %[2]q, "failed_attempts": 3},
				{"id": 5, "name": "Office", "category": "folder_unavailable", "since": %[3]q, "failed_attempts": null}
			],
			"unreferenced": [],
			"failures": []
		}`, testTime, formatTime(failingSince), formatTime(unavailableSince), noProcessing, idleSearch))
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
			"processing": %[2]s,
			"search": %[3]s,
			"recovery": {"finished_at": %[1]q, "temp_files_removed": 0, "temp_files_not_removed": 0, "unreferenced_found": 0},
			"sources": [],
			"unreferenced": [],
			"failures": []
		}`, testTime, noProcessing, idleSearch))
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
			"processing": %s,
			"search": %s,
			"recovery": %s,
			"sources": [],
			"unreferenced": [{"content_hash": %q, "size": %d}],
			"failures": []
		}`, testTime, noProcessing, idleSearch, recovery, hash, len(content)))
		// The unreferenced original, and search, which never started.
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":2,"expires_in":43200}`)

		response, body = h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
		}

		response, body = h.do(t, http.MethodGet, "/api/status", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %q,
			"processing": {"not_processed": 1, "processed_with_problems": 0, "unreadable": 0},
			"search": %s,
			"recovery": %s,
			"sources": [],
			"unreferenced": [],
			"failures": []
		}`, testTime, idleSearch, recovery))
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":1,"expires_in":43200}`)
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

// The count is failures plus unreferenced originals, plus one here for
// search, which never starts in this harness.
func TestProblemCountAddsFailuresAndUnreferenced(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	assertCount := func(t *testing.T, want string) {
		t.Helper()
		response, body := h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, want)
	}

	assertCount(t, `{"problem_count":1,"expires_in":43200}`)

	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.StorageFull})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.TempFileNotRemoved})
	assertCount(t, `{"problem_count":3,"expires_in":43200}`)

	first := hashOf([]byte("first"))
	for _, hash := range []string{first, hashOf([]byte("second")), hashOf([]byte("third"))} {
		h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hash, Size: 1})
	}
	assertCount(t, `{"problem_count":6,"expires_in":43200}`)

	h.status.RemoveUnreferenced(first)
	assertCount(t, `{"problem_count":5,"expires_in":43200}`)
}

// Contract: the problem count adds one for each problem source on the status
// screen, however many problems the source has. Search, which never starts
// in this harness, adds one more.
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
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":4,"expires_in":43200}`)
}

// Search failures carry their detail. One that follows another of its
// category repeats it, and each repeat counts as a problem.
func TestStatusSearchFailures(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	signal, exitStatus := 9, 0
	generation, rebuild, first, last, free := int64(3), int64(4), int64(17), int64(40), int64(1536)<<20

	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.SearchProcessStopped, Detail: &opstatus.Detail{Signal: &signal}})
	h.clock.Advance(20 * time.Minute)
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.SearchProcessStopped, Detail: &opstatus.Detail{Signal: &signal}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.SearchProcessDidNotStart, Detail: &opstatus.Detail{Cause: opstatus.CauseMissing}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.SearchProcessDidNotAnswer, Detail: &opstatus.Detail{Operation: opstatus.OperationSearch}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.IndexWriteFailed, Detail: &opstatus.Detail{
		GenerationID: &generation, FirstMessageID: &first, LastMessageID: &last, Cause: opstatus.CauseNoSpace,
	}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.IndexMaintenanceFailed, Detail: &opstatus.Detail{GenerationID: &generation}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.IndexNeedsRebuild, Detail: &opstatus.Detail{Cause: opstatus.CauseIndexMaintenanceFailed}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.LowDiskSpace, Detail: &opstatus.Detail{FreeBytes: &free}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.RebuildFailed, Detail: &opstatus.Detail{RebuildID: &rebuild, GenerationID: &generation}})
	h.status.RecordFailure(opstatus.FailureEvent{Category: opstatus.SearchProcessStopped, Detail: &opstatus.Detail{ExitStatus: &exitStatus}})

	response, body := h.do(t, http.MethodGet, "/api/status", session)
	assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
		"started_at": %[1]q,
		"processing": %[3]s,
		"search": %[4]s,
		"recovery": null,
		"sources": [],
		"unreferenced": [],
		"failures": [
			{"at": %[1]q, "category": "search_process_stopped", "restored": false, "repeats": 2, "last_at": %[2]q, "detail": {"signal": 9}},
			{"at": %[2]q, "category": "search_process_did_not_start", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"cause": "missing"}},
			{"at": %[2]q, "category": "search_process_did_not_answer", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"operation": "search"}},
			{"at": %[2]q, "category": "index_write_failed", "restored": false, "repeats": 1, "last_at": %[2]q,
				"detail": {"generation_id": 3, "first_message_id": 17, "last_message_id": 40, "cause": "no_space"}},
			{"at": %[2]q, "category": "index_maintenance_failed", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"generation_id": 3}},
			{"at": %[2]q, "category": "index_needs_rebuild", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"cause": "index_maintenance_failed"}},
			{"at": %[2]q, "category": "low_disk_space", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"free_bytes": 1610612736}},
			{"at": %[2]q, "category": "rebuild_failed", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"rebuild_id": 4, "generation_id": 3}},
			{"at": %[2]q, "category": "search_process_stopped", "restored": false, "repeats": 1, "last_at": %[2]q, "detail": {"exit_status": 0}}
		]
	}`, testTime, testNowPlus20min, noProcessing, idleSearch))

	// Ten failures, and search, which never started. The clock moved 20 minutes.
	response, body = h.do(t, http.MethodGet, "/api/session", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":11,"expires_in":42000}`)
}

// The processing counts and the search object follow the archive and a real
// search module as it starts, pauses for low disk space, and rebuilds.
// Messages with processing problems never count as problems.
func TestStatusFollowsSearchAndProcessing(t *testing.T) {
	const roomy = int64(41_234_567_890)
	disk := &fakeDisk{free: roomy}
	h := newHarnessWith(t, Routes(), harnessOptions{search: searchProgram(t), freeSpace: disk.read, holdSearch: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	assertCount := func(t *testing.T, want int) {
		t.Helper()
		response, body := h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{"problem_count":%d,"expires_in":43200}`, want))
	}
	assertStatus := func(t *testing.T, processing, search, failures string) {
		t.Helper()
		response, body := h.do(t, http.MethodGet, "/api/status", session)
		assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(`{
			"started_at": %q, "processing": %s, "search": %s,
			"recovery": null, "sources": [], "unreferenced": [], "failures": %s
		}`, testTime, processing, search, failures))
	}
	processed := func(t *testing.T, n int64) {
		t.Helper()
		waitFor(t, fmt.Sprintf("%d messages processed", n), func() bool {
			highest, err := h.archive.HighestProcessedSeq(context.Background())
			return err == nil && highest == n
		})
	}
	indexing := func(t *testing.T, want search.IndexingState) {
		t.Helper()
		waitFor(t, "indexing "+string(want), func() bool { return h.search.State().Indexing == want })
	}

	// Before the first start, every processed message waits.
	h.importMail(t, session, plainFixture(t, "plain-text.eml"), "plan.eml")
	processed(t, 1)
	assertStatus(t, noProcessing, `{
		"state": "unavailable", "next_start_at": null, "needs_rebuild": null, "restarts": 0,
		"indexing": {"state": "stopped", "waiting": 1, "done": null, "to_do": null, "free_bytes": null,
			"pause_below_bytes": 2147483648, "resume_at_bytes": 3221225472},
		"rebuild": null
	}`, `[]`)
	assertCount(t, 1)

	h.startSearch()
	indexing(t, search.IndexingUpToDate)
	h.importMail(t, session, brokenMail(), "broken.eml")
	processed(t, 2)
	waitFor(t, "the broken message indexed", func() bool {
		report, err := h.search.Report(context.Background())
		return err == nil && report.Waiting == 0 && report.State.Indexing == search.IndexingUpToDate
	})
	assertStatus(t, `{"not_processed": 0, "processed_with_problems": 1, "unreadable": 0}`, `{
		"state": "available", "next_start_at": null, "needs_rebuild": null, "restarts": 0,
		"indexing": {"state": "up_to_date", "waiting": 0, "done": null, "to_do": null, "free_bytes": 41234567890,
			"pause_below_bytes": 2147483648, "resume_at_bytes": 3221225472},
		"rebuild": null
	}`, `[]`)
	assertCount(t, 0)

	// Low disk space pauses indexing, which counts besides its event.
	disk.set(1*gib, 1*gib)
	h.importMail(t, session, mail("Pause"), "pause.eml")
	processed(t, 3)
	indexing(t, search.IndexingPaused)
	lowDisk := fmt.Sprintf(`{"at": %q, "category": "low_disk_space", "restored": false, "repeats": 1, "last_at": %[1]q,
		"detail": {"free_bytes": 1073741824}}`, testTime)
	assertStatus(t, `{"not_processed": 0, "processed_with_problems": 1, "unreadable": 0}`, `{
		"state": "available", "next_start_at": null, "needs_rebuild": null, "restarts": 0,
		"indexing": {"state": "paused", "waiting": 1, "done": null, "to_do": null, "free_bytes": 1073741824,
			"pause_below_bytes": 2147483648, "resume_at_bytes": 3221225472},
		"rebuild": null
	}`, "["+lowDisk+"]")
	assertCount(t, 2)

	disk.set(roomy, roomy)
	h.importMail(t, session, mail("Weiter"), "resume.eml")
	processed(t, 4)
	waitFor(t, "everything indexed", func() bool {
		report, err := h.search.Report(context.Background())
		return err == nil && report.Waiting == 0 && report.State.Indexing == search.IndexingUpToDate
	})
	assertCount(t, 1)

	// The rebuild's check reads plenty of space, and indexing then reads
	// too little, so the rebuild pauses before its first batch.
	disk.set(roomy, 1*gib)
	response, body := h.send(t, http.MethodPost, "/api/search/rebuild", nil, session)
	assertJSONResponse(t, response, body, http.StatusAccepted,
		`{"rebuild":{"id":1,"started_at":`+quote(testTime)+`,"started_by":"admin@example.test"}}`)
	indexing(t, search.IndexingPaused)
	assertStatus(t, `{"not_processed": 0, "processed_with_problems": 1, "unreadable": 0}`, fmt.Sprintf(`{
		"state": "available", "next_start_at": null, "needs_rebuild": null, "restarts": 0,
		"indexing": {"state": "paused", "waiting": 0, "done": 0, "to_do": 4, "free_bytes": 1073741824,
			"pause_below_bytes": 2147483648, "resume_at_bytes": 3221225472},
		"rebuild": {"id": 1, "started_at": %q, "started_by": "admin@example.test"}
	}`, testTime), "["+lowDisk+","+lowDisk+"]")
	assertCount(t, 3)
}

// Needs rebuild shows its cause and since when, and counts as a problem.
// Low disk space holds the first build, whose end would clear the mark.
func TestStatusNeedsRebuild(t *testing.T) {
	disk := &fakeDisk{free: 1 * gib}
	h := newHarnessWith(t, Routes(), harnessOptions{search: searchProgram(t), freeSpace: disk.read, holdSearch: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	h.importMail(t, session, plainFixture(t, "plain-text.eml"), "plan.eml")
	waitFor(t, "the message processed", func() bool {
		highest, err := h.archive.HighestProcessedSeq(context.Background())
		return err == nil && highest == 1
	})
	since := testNow.Add(-time.Hour)
	if _, err := h.db.Writer.Exec("INSERT INTO search_needs_rebuild (id, category, since) VALUES (1, 'index_damaged', ?)", since.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	h.startSearch()
	waitFor(t, "needs rebuild with low disk space", func() bool {
		return h.search.State().Search == search.SearchNeedsRebuild && len(h.status.Snapshot().Failures) == 1
	})

	response, body := h.do(t, http.MethodGet, "/api/status", session)
	var status struct {
		Search struct {
			State        string          `json:"state"`
			NeedsRebuild json.RawMessage `json:"needs_rebuild"`
			Indexing     struct {
				State string `json:"state"`
			} `json:"indexing"`
		} `json:"search"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status = %d %s", response.StatusCode, body)
	}
	if status.Search.State != "needs_rebuild" || status.Search.Indexing.State != "stopped" {
		t.Errorf("search %+v, want needs_rebuild with indexing stopped", status.Search)
	}
	want := fmt.Sprintf(`{"cause":"index_damaged","since":%q}`, formatTime(since))
	if string(status.Search.NeedsRebuild) != want {
		t.Errorf("needs_rebuild = %s, want %s", status.Search.NeedsRebuild, want)
	}
	// Needs rebuild, and the low disk space event. Indexing shows Stopped,
	// not Paused, so the pause adds nothing.
	response, body = h.do(t, http.MethodGet, "/api/session", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":2,"expires_in":43200}`)
}

// While the program can't start, the status says when the next start is, and
// each failed start counts.
func TestStatusNextStart(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{search: filepath.Join(t.TempDir(), "mailarkiv-search")})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	waitFor(t, "a next start", func() bool {
		report, err := h.search.Report(context.Background())
		return err == nil && report.NextStartAt != nil
	})

	response, body := h.do(t, http.MethodGet, "/api/status", session)
	var status struct {
		Search struct {
			State       string  `json:"state"`
			NextStartAt *string `json:"next_start_at"`
		} `json:"search"`
		Failures []struct {
			Category string            `json:"category"`
			Repeats  int               `json:"repeats"`
			Detail   map[string]string `json:"detail"`
		} `json:"failures"`
	}
	if err := json.Unmarshal([]byte(body), &status); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/status = %d %s", response.StatusCode, body)
	}
	if status.Search.State != "unavailable" || status.Search.NextStartAt == nil {
		t.Fatalf("search %+v, want unavailable with a next start", status.Search)
	}
	// The harness clock stands still, so each wait starts at testNow.
	if next, err := time.Parse(time.RFC3339Nano, *status.Search.NextStartAt); err != nil || !next.After(testNow) {
		t.Errorf("next_start_at %s, want a time after %s", *status.Search.NextStartAt, testTime)
	}
	if len(status.Failures) != 1 || status.Failures[0].Category != "search_process_did_not_start" ||
		status.Failures[0].Detail["cause"] != "missing" {
		t.Fatalf("failures %+v, want one search_process_did_not_start with cause missing", status.Failures)
	}
	response, body = h.do(t, http.MethodGet, "/api/session", session)
	var count struct {
		ProblemCount int `json:"problem_count"`
	}
	if err := json.Unmarshal([]byte(body), &count); err != nil || response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session = %d %s", response.StatusCode, body)
	}
	// Repeats can grow between the two reads, never shrink.
	if count.ProblemCount < status.Failures[0].Repeats+1 {
		t.Errorf("problem_count %d, want at least the %d failed starts and 1 for Unavailable", count.ProblemCount, status.Failures[0].Repeats)
	}
}
