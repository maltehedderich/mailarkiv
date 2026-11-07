package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// maxImport is the contract's limit: 150,000,000 bytes is accepted, one more
// byte is refused.
const maxImport = 150_000_000

// testTime is testNow as the contract writes times.
const testTime = "2026-11-06T15:50:52.123Z"

func storedJSON(outcome string, id int64, content []byte, fileName string, importCount int) string {
	return fmt.Sprintf(`{"outcome":%q,"message":{"id":%d,"content_hash":%q,"size":%d,"file_name":%s,"imported_at":%q,"import_count":%d}}`,
		outcome, id, hashOf(content), len(content), fileName, testTime, importCount)
}

// messageID reads the ID from an import's answer.
func messageID(t *testing.T, body string) int64 {
	t.Helper()
	var answer storedResponse
	if err := json.Unmarshal([]byte(body), &answer); err != nil {
		t.Fatalf("body isn't JSON: %q: %v", body, err)
	}
	return answer.Message.ID
}

// Rows from the POST /api/imports contract, in one archive, so each step also
// shows what the earlier ones left behind.
func TestImportOutcomes(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	importFile := func(t *testing.T, body io.Reader, change func(*http.Request)) (*http.Response, string) {
		t.Helper()
		if change == nil {
			change = session
		} else {
			change = both(session, change)
		}
		return h.send(t, http.MethodPost, "/api/imports", body, change)
	}

	// The data model promises an ID is never reused. It doesn't promise that IDs
	// are consecutive.
	seen := map[int64]bool{}
	assertArchived := func(t *testing.T, response *http.Response, body string, content []byte, fileName string) int64 {
		t.Helper()
		id := messageID(t, body)
		if id < 1 || seen[id] {
			t.Fatalf("id = %d, want a positive ID not seen before; body %s", id, body)
		}
		seen[id] = true
		assertJSONResponse(t, response, body, http.StatusCreated, storedJSON("archived", id, content, fileName, 1))
		return id
	}

	first := mail("first")
	var firstID int64

	t.Run("new bytes are archived", func(t *testing.T) {
		response, body := importFile(t, bytes.NewReader(first), withFileName("invoice.eml"))
		firstID = assertArchived(t, response, body, first, `"invoice.eml"`)
	})

	t.Run("the same bytes again are already archived", func(t *testing.T) {
		response, body := importFile(t, bytes.NewReader(first), withFileName("again.eml"))
		assertJSONResponse(t, response, body, http.StatusOK, storedJSON("already_archived", firstID, first, `"again.eml"`, 2))
	})

	t.Run("no name header gives a null file name", func(t *testing.T) {
		content := mail("second")
		response, body := importFile(t, bytes.NewReader(content), nil)
		assertArchived(t, response, body, content, `null`)
	})

	t.Run("invalid UTF-8 after decoding becomes U+FFFD", func(t *testing.T) {
		content := mail("third")
		response, body := importFile(t, bytes.NewReader(content), withFileName("bad%FFname.eml"))
		assertArchived(t, response, body, content, `"bad\ufffdname.eml"`)
	})

	// encodeURIComponent's output. A plus sign is literal, not a space.
	t.Run("percent-encoded UTF-8 is decoded", func(t *testing.T) {
		content := mail("fourth")
		response, body := importFile(t, bytes.NewReader(content), withFileName("R%C3%A9sum%C3%A9+final%20v2.eml"))
		assertArchived(t, response, body, content, `"R\u00e9sum\u00e9+final v2.eml"`)
	})

	for _, encoded := range []string{"%zz.eml", "name%E", "%"} {
		t.Run("bad percent-encoding "+encoded, func(t *testing.T) {
			response, body := importFile(t, bytes.NewReader(mail("named badly")), withFileName(encoded))
			assertJSONResponse(t, response, body, http.StatusBadRequest, `{"error":"bad_file_name"}`)
		})
	}

	refusals := []struct {
		name    string
		content []byte
		want    string
	}{
		{"empty file", nil, `{"outcome":"refused","reason":"empty"}`},
		{"PDF", []byte("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n1 0 obj\n"), `{"outcome":"refused","reason":"no_mail_header"}`},
	}
	for _, tt := range refusals {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			response, body := importFile(t, bytes.NewReader(tt.content), withFileName("refused.eml"))
			assertJSONResponse(t, response, body, http.StatusUnprocessableEntity, tt.want)
		})
	}

	// A body the client never sends proves the answer came before any read.
	t.Run("Content-Length over the limit is refused before any read", func(t *testing.T) {
		unsent, never := io.Pipe()
		defer never.Close()
		response, body := importFile(t, unsent, func(request *http.Request) {
			request.ContentLength = maxImport + 1
		})
		assertJSONResponse(t, response, body, http.StatusRequestEntityTooLarge, `{"outcome":"refused","reason":"too_large"}`)
	})

	// The client can't know the length of a reader of unknown type, so it sends
	// the body chunked.
	t.Run("a chunked body is refused once it passes the limit", func(t *testing.T) {
		response, body := importFile(t, newGenerated(maxImport+1), nil)
		assertJSONResponse(t, response, body, http.StatusRequestEntityTooLarge, `{"outcome":"refused","reason":"too_large"}`)
		assertNoTempFiles(t, h.dataDir)
	})

	t.Run("refused imports list nothing", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, "/api/messages", session)
		var page listedPage
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatalf("GET /api/messages = %d %s: %v", response.StatusCode, body, err)
		}
		if response.StatusCode != http.StatusOK || page.Total != 4 || len(page.Items) != 4 {
			t.Fatalf("GET /api/messages = %d %s, want the 4 archived messages", response.StatusCode, body)
		}
		assertNoTempFiles(t, h.dataDir)
	})

	// One row per line of the data model's Import outcome table that a single
	// import can reach. The two lines with no row before the check and a row at
	// commit need two imports racing, so TestDecideOutcome and
	// TestConcurrentImports cover them. Each row seeds its state by editing real
	// files, and each adds exactly one observation.
	seedRow := func(t *testing.T, content []byte) int64 {
		t.Helper()
		response, body := importFile(t, bytes.NewReader(content), withFileName("earlier.eml"))
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("the earlier import = %d %s, want 201", response.StatusCode, body)
		}
		return messageID(t, body)
	}
	// Seeds return the ID of a message that already exists, or 0 when none does.
	rows := []struct {
		name string
		seed func(t *testing.T, content []byte) int64
		// status, outcome, and importCount are what the import under test answers.
		status      int
		outcome     string
		importCount int
		// event is the failure event it records. The zero value means none.
		event opstatus.Category
		// reuses is set when the stored file must stay the same file (FR-018).
		reuses bool
	}{
		{
			name:        "no row, no blob",
			seed:        func(*testing.T, []byte) int64 { return 0 },
			status:      http.StatusCreated,
			outcome:     "archived",
			importCount: 1,
		},
		{
			name: "no row, matching blob, on the unreferenced list",
			seed: func(t *testing.T, content []byte) int64 {
				h.writeBlob(t, hashOf(content), content)
				h.status.AddUnreferenced(opstatus.UnreferencedOriginal{ContentHash: hashOf(content), Size: int64(len(content))})
				return 0
			},
			status:      http.StatusCreated,
			outcome:     "archived",
			importCount: 1,
			reuses:      true,
		},
		{
			name: "no row, damaged blob",
			seed: func(t *testing.T, content []byte) int64 {
				h.writeBlob(t, hashOf(content), []byte("other bytes"))
				return 0
			},
			status:      http.StatusCreated,
			outcome:     "archived",
			importCount: 1,
			event:       opstatus.StoredOriginalDamaged,
		},
		{
			name: "no row, unreadable blob",
			seed: func(t *testing.T, content []byte) int64 {
				h.makeBlobUnreadable(t, hashOf(content))
				return 0
			},
			status:      http.StatusCreated,
			outcome:     "archived",
			importCount: 1,
			event:       opstatus.StoredOriginalDamaged,
		},
		{
			name:        "row, matching blob",
			seed:        seedRow,
			status:      http.StatusOK,
			outcome:     "already_archived",
			importCount: 2,
			reuses:      true,
		},
		{
			name: "row, missing blob",
			seed: func(t *testing.T, content []byte) int64 {
				id := seedRow(t, content)
				if err := os.Remove(h.blobFile(hashOf(content))); err != nil {
					t.Fatal(err)
				}
				return id
			},
			status:      http.StatusOK,
			outcome:     "restored",
			importCount: 2,
			event:       opstatus.StoredOriginalMissing,
		},
		{
			name: "row, blob with extra bytes",
			seed: func(t *testing.T, content []byte) int64 {
				id := seedRow(t, content)
				h.writeBlob(t, hashOf(content), append(bytes.Clone(content), 'x'))
				return id
			},
			status:      http.StatusOK,
			outcome:     "restored",
			importCount: 2,
			event:       opstatus.StoredOriginalDamaged,
		},
		// The same size, so only hashing the whole file can find it.
		{
			name: "row, blob with its last byte changed",
			seed: func(t *testing.T, content []byte) int64 {
				id := seedRow(t, content)
				damaged := bytes.Clone(content)
				damaged[len(damaged)-1] ^= 0xff
				h.writeBlob(t, hashOf(content), damaged)
				return id
			},
			status:      http.StatusOK,
			outcome:     "restored",
			importCount: 2,
			event:       opstatus.StoredOriginalDamaged,
		},
		{
			name: "row, unreadable blob",
			seed: func(t *testing.T, content []byte) int64 {
				id := seedRow(t, content)
				h.makeBlobUnreadable(t, hashOf(content))
				return id
			},
			status:      http.StatusOK,
			outcome:     "restored",
			importCount: 2,
			event:       opstatus.StoredOriginalDamaged,
		},
	}
	for _, row := range rows {
		t.Run(row.name, func(t *testing.T) {
			content := mail("outcome row: " + row.name)
			hash := hashOf(content)
			atStart := h.counts(t)
			id := row.seed(t, content)
			before := h.counts(t)
			eventsBefore := len(h.status.Snapshot().Failures)
			var storedBefore os.FileInfo
			if row.reuses {
				info, err := os.Stat(h.blobFile(hash))
				if err != nil {
					t.Fatal(err)
				}
				storedBefore = info
			}

			response, body := importFile(t, bytes.NewReader(content), withFileName("row.eml"))

			if id == 0 {
				id = messageID(t, body)
			}
			assertJSONResponse(t, response, body, row.status, storedJSON(row.outcome, id, content, `"row.eml"`, row.importCount))

			after := h.counts(t)
			if after.observations != before.observations+1 {
				t.Errorf("observations went from %d to %d, want one more", before.observations, after.observations)
			}
			if after.messages != atStart.messages+1 {
				t.Errorf("messages went from %d to %d, want one more than before the row", atStart.messages, after.messages)
			}
			if after.blobs != atStart.blobs+1 {
				t.Errorf("stored files went from %d to %d, want one copy", atStart.blobs, after.blobs)
			}
			stored, err := os.ReadFile(h.blobFile(hash))
			if err != nil {
				t.Fatalf("the blob can't be read: %v", err)
			}
			if !bytes.Equal(stored, content) {
				t.Errorf("the blob holds %q, want the imported bytes", stored)
			}
			if row.reuses {
				info, err := os.Stat(h.blobFile(hash))
				if err != nil {
					t.Fatal(err)
				}
				if !os.SameFile(storedBefore, info) {
					t.Error("the import replaced the stored copy, and should have used it")
				}
			}
			assertNoTempFiles(t, h.dataDir)

			snapshot := h.status.Snapshot()
			for _, original := range snapshot.Unreferenced {
				if original.ContentHash == hash {
					t.Errorf("%s is still on the unreferenced list", hash)
				}
			}
			events := snapshot.Failures[eventsBefore:]
			if row.event == (opstatus.Category{}) {
				if len(events) != 0 {
					t.Fatalf("failure events = %+v, want none", events)
				}
				return
			}
			if len(events) != 1 {
				t.Fatalf("failure events = %+v, want one %v", events, row.event)
			}
			event := events[0]
			if event.Category != row.event {
				t.Errorf("category = %v, want %v", event.Category, row.event)
			}
			if !event.Restored {
				t.Error("restored = false, want true")
			}
			if event.MessageID == nil || *event.MessageID != id {
				t.Errorf("message ID = %v, want %d", event.MessageID, id)
			}
			if event.ContentHash == nil || *event.ContentHash != hash {
				t.Errorf("content hash = %v, want %s", event.ContentHash, hash)
			}
			if !event.At.Equal(testNow) {
				t.Errorf("time = %v, want %v", event.At, testNow)
			}
		})
	}
}

// Contract: storage_write_failed answers 500 with the outcome, and the failure
// event counts as a problem (FR-038, FR-039).
func TestImportStorageWriteFailure(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	tmp := filepath.Join(h.dataDir, "tmp")
	if err := os.Remove(tmp); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tmp, nil, 0o600); err != nil {
		t.Fatal(err)
	}

	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(mail("lost")), session)
	assertJSONResponse(t, response, body, http.StatusInternalServerError, `{"outcome":"failed","reason":"storage_write_failed"}`)

	response, body = h.do(t, http.MethodGet, "/api/session", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":1}`)
	response, body = h.do(t, http.MethodGet, "/api/messages", session)
	assertJSONResponse(t, response, body, http.StatusOK, `{"items":[],"page":1,"page_size":50,"total":0}`)
}

func assertNoTempFiles(t *testing.T, dataDir string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(dataDir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("tmp/ holds %d entries, want none", len(entries))
	}
}

// User Story 3 scenario 7, FR-038: a connection that drops during an upload
// removes the partial file, lists nothing, and is no failure event. The client
// sees Unconfirmed, because no answer comes.
func TestDroppedUpload(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	token := h.signInToken(t, "admin@example.test")
	address := strings.TrimPrefix(h.server.URL, "http://")

	// startUpload sends the headers and one chunk, past the 4 KiB the file check
	// reads, and returns once the temp file exists. The body never ends.
	startUpload := func(t *testing.T) *net.TCPConn {
		t.Helper()
		connection, err := net.DialTimeout("tcp", address, 10*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { connection.Close() })
		if err := connection.SetDeadline(time.Now().Add(30 * time.Second)); err != nil {
			t.Fatal(err)
		}
		chunk := append([]byte("Subject: dropped\r\n\r\n"), bytes.Repeat([]byte("x"), 8192)...)
		_, err = fmt.Fprintf(connection,
			"POST /api/imports HTTP/1.1\r\nHost: %s\r\nCookie: __Host-mailarkiv_session=%s\r\nMailArkiv-File-Name: dropped.eml\r\nTransfer-Encoding: chunked\r\n\r\n%x\r\n%s\r\n",
			address, token, len(chunk), chunk)
		if err != nil {
			t.Fatal(err)
		}
		waitFor(t, "the temp file appearing", func() bool { return h.tempFileCount(t) == 1 })
		return connection.(*net.TCPConn)
	}

	assertNothingLeft := func(t *testing.T) {
		t.Helper()
		waitFor(t, "the temp file being removed", func() bool { return h.tempFileCount(t) == 0 })
		if got := h.counts(t); got != (archiveCounts{}) {
			t.Errorf("the archive holds %+v, want nothing", got)
		}
		if failures := h.status.Snapshot().Failures; len(failures) != 0 {
			t.Errorf("failure events = %+v, want none", failures)
		}
		session := withSession(token)
		response, body := h.do(t, http.MethodGet, "/api/messages", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"items":[],"page":1,"page_size":50,"total":0}`)
		response, body = h.do(t, http.MethodGet, "/api/session", session)
		assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":0}`)
	}

	t.Run("the connection closes mid-body", func(t *testing.T) {
		connection := startUpload(t)
		if err := connection.Close(); err != nil {
			t.Fatal(err)
		}
		assertNothingLeft(t)
	})

	// The sending side closes and the reading side stays open, so the test can
	// see that the server sends no answer at all.
	t.Run("the body stops, and no answer comes", func(t *testing.T) {
		connection := startUpload(t)
		if err := connection.CloseWrite(); err != nil {
			t.Fatal(err)
		}
		// The server ends the connection, so a reset here is an empty answer too.
		answer, _ := io.ReadAll(connection)
		if len(answer) != 0 {
			t.Errorf("the server answered %q, want nothing", answer)
		}
		assertNothingLeft(t)
	})
}
