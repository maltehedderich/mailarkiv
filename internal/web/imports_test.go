package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
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
