package web

import (
	"bytes"
	"fmt"
	"net/http"
	"testing"
)

// Contract: GET /api/messages lists the most recent import first. page starts
// at 1 and defaults to 1, anything else is 400 bad_page, and a page past the
// end has no items and the real total.
func TestListMessages(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	older := mail("older")
	newer := mail("newer")
	ids := map[string]int64{}
	for _, step := range []struct {
		content []byte
		name    string
	}{
		{older, "older.eml"},
		{newer, "newer.eml"},
		// Importing the older bytes again makes them the most recent import.
		{older, "older-again.eml"},
	} {
		response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(step.content), both(session, withFileName(step.name)))
		if response.StatusCode != http.StatusCreated && response.StatusCode != http.StatusOK {
			t.Fatalf("import %s = %d %s", step.name, response.StatusCode, body)
		}
		ids[string(step.content)] = messageID(t, body)
	}

	firstPage := fmt.Sprintf(`{"items":[
		{"id":%d,"content_hash":%q,"size":%d,"file_name":"older-again.eml","imported_at":%q,"import_count":2},
		{"id":%d,"content_hash":%q,"size":%d,"file_name":"newer.eml","imported_at":%q,"import_count":1}
	],"page":1,"page_size":50,"total":2}`,
		ids[string(older)], hashOf(older), len(older), testTime,
		ids[string(newer)], hashOf(newer), len(newer), testTime)

	for _, tt := range []struct {
		name   string
		query  string
		status int
		want   string
	}{
		{"no page", "", http.StatusOK, firstPage},
		{"page 1", "?page=1", http.StatusOK, firstPage},
		{"page past the end", "?page=2", http.StatusOK, `{"items":[],"page":2,"page_size":50,"total":2}`},
		{"largest page", "?page=9223372036854775807", http.StatusOK, `{"items":[],"page":9223372036854775807,"page_size":50,"total":2}`},
		{"page 0", "?page=0", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"negative page", "?page=-1", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"fraction", "?page=1.5", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"word", "?page=two", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"empty", "?page=", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"two pages", "?page=1&page=2", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"too large for a number", "?page=99999999999999999999", http.StatusBadRequest, `{"error":"bad_page"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/messages"+tt.query, session)
			assertJSONResponse(t, response, body, tt.status, tt.want)
		})
	}
}
