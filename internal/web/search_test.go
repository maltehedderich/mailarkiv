package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// searchBody is a words-only search, the way the interface sends one.
func searchBody(words string, page int) string {
	return fmt.Sprintf(`{"words":%s,"from":"","to":"","subject":"","attachment":"",`+
		`"list_date_from":null,"list_date_before":null,"has_attachments":false,"page":%d}`, quote(words), page)
}

// plainFixture reads one of the synthetic messages processing is tested with.
func plainFixture(t *testing.T, name string) []byte {
	t.Helper()
	content, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", "plain", name))
	if err != nil {
		t.Fatal(err)
	}
	return content
}

func TestSearchFindsWords(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{search: searchProgram(t)})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	report := plainFixture(t, "html-only.eml")
	_, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(report), both(session, withFileName("bericht.eml")))
	id := messageID(t, body)
	h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(plainFixture(t, "plain-text.eml")), session)

	var response *http.Response
	waitFor(t, "both messages in the index", func() bool {
		response, body = h.send(t, http.MethodPost, "/api/search", strings.NewReader(searchBody("quartals", 1)), session)
		var answer struct {
			Total       int `json:"total"`
			NotIncluded int `json:"not_included"`
		}
		return response.StatusCode == http.StatusOK && json.Unmarshal([]byte(body), &answer) == nil &&
			answer.Total == 1 && answer.NotIncluded == 0
	})
	row := fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,"processing":"processed",`+
		`"list_date":"2026-11-03T10:00:00.000Z","list_date_is_archive_time":false,`+
		`"sender":{"name":"Bernd Weber","address":"bernd@example.test","more":0},"subject":"Bericht",`+
		`"has_attachments":false,"observation_count":1,"latest":%s}`,
		id, hashOf(report), len(report), importEntry(testTime, "admin@example.test", `"bericht.eml"`))
	assertJSONResponse(t, response, body, http.StatusOK, `{"items":[`+row+`],"page":1,"page_size":50,"total":1,`+
		`"not_included":0,"full_list":false,"may_be_incomplete":false}`)

	// Words without letters or digits give the full list.
	response, body = h.send(t, http.MethodPost, "/api/search", strings.NewReader(searchBody("--", 1)), session)
	var full struct {
		Items       []json.RawMessage `json:"items"`
		Total       int               `json:"total"`
		NotIncluded int               `json:"not_included"`
		FullList    bool              `json:"full_list"`
	}
	if err := json.Unmarshal([]byte(body), &full); err != nil || response.StatusCode != http.StatusOK ||
		!full.FullList || full.Total != 2 || len(full.Items) != 2 || full.NotIncluded != 0 {
		t.Errorf("search -- = %d %s, want the full list of 2", response.StatusCode, body)
	}
	if strings.Contains(h.log.String(), "quartals") {
		t.Error("the log holds a search word")
	}
}

func TestSearchBodyRules(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	// With the rules met, the search module answers. It never started here.
	const unavailable = `{"error":"search_unavailable","cause":"search_process_did_not_start","next_start_at":null}`
	valid := searchBody("quartals", 1)
	padded := func(size int) string { return valid + strings.Repeat(" ", size-len(valid)) }
	withField := func(field, value string) string {
		var body map[string]json.RawMessage
		if err := json.Unmarshal([]byte(valid), &body); err != nil {
			t.Fatal(err)
		}
		if value == "" {
			delete(body, field)
		} else {
			body[field] = json.RawMessage(value)
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		return string(encoded)
	}
	tests := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"not JSON", "{", http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`},
		{"two objects", valid + valid, http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`},
		{"unknown field", withField("sender", `"x"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`},
		{"64 KiB", padded(64 << 10), http.StatusServiceUnavailable, unavailable},
		{"over 64 KiB", padded(64<<10 + 1), http.StatusBadRequest, `{"error":"invalid_fields","fields":[]}`},
		{"page 0", searchBody("quartals", 0), http.StatusBadRequest, `{"error":"invalid_fields","fields":["page"]}`},
		{"page 2,000", searchBody("quartals", 2000), http.StatusServiceUnavailable, unavailable},
		{"page 2,001", searchBody("quartals", 2001), http.StatusBadRequest, `{"error":"page_too_deep"}`},
		{"page not a whole number", withField("page", "1.5"), http.StatusBadRequest, `{"error":"invalid_fields","fields":["page"]}`},
		{"1,000 characters", searchBody(strings.Repeat("ä", 1000), 1), http.StatusServiceUnavailable, unavailable},
		{"1,001 characters", searchBody(strings.Repeat("ä", 1001), 1), http.StatusBadRequest, `{"error":"invalid_fields","fields":["words"]}`},
		{"words missing", withField("words", ""), http.StatusBadRequest, `{"error":"invalid_fields","fields":["words"]}`},
		{"words not a string", withField("words", "7"), http.StatusBadRequest, `{"error":"invalid_fields","fields":["words"]}`},
		{"a filter too long", withField("subject", quote(strings.Repeat("x", 1001))), http.StatusBadRequest, `{"error":"invalid_fields","fields":["subject"]}`},
		{"an instant", withField("list_date_from", `"2026-01-01T00:00:00.000+01:00"`), http.StatusServiceUnavailable, unavailable},
		{"a day isn't an instant", withField("list_date_before", `"2026-01-01"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_before"]}`},
		{"has_attachments null", withField("has_attachments", "null"), http.StatusBadRequest, `{"error":"invalid_fields","fields":["has_attachments"]}`},
		{"every broken field named", `{"words":1,"from":"","to":"","subject":"","attachment":"","list_date_from":"x","list_date_before":null,"has_attachments":false,"page":0}`,
			http.StatusBadRequest, `{"error":"invalid_fields","fields":["words","list_date_from","page"]}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.send(t, http.MethodPost, "/api/search", strings.NewReader(tt.body), session)
			assertJSONResponse(t, response, body, tt.status, tt.want)
		})
	}
}

func TestSearchWhileTheProgramDoesNotStart(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{search: filepath.Join(t.TempDir(), "mailarkiv-search")})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	var answer struct {
		Error       string  `json:"error"`
		Cause       string  `json:"cause"`
		NextStartAt *string `json:"next_start_at"`
	}
	var response *http.Response
	var body string
	waitFor(t, "a next start time", func() bool {
		response, body = h.send(t, http.MethodPost, "/api/search", strings.NewReader(searchBody("quartals", 1)), session)
		return json.Unmarshal([]byte(body), &answer) == nil && answer.NextStartAt != nil
	})
	if response.StatusCode != http.StatusServiceUnavailable || answer.Error != "search_unavailable" ||
		answer.Cause != "search_process_did_not_start" {
		t.Fatalf("search = %d %s, want 503 search_unavailable for search_process_did_not_start", response.StatusCode, body)
	}
	// The waits run from 1 to 60 s on the server's clock, which stands still
	// here.
	next, err := time.Parse(time.RFC3339Nano, *answer.NextStartAt)
	if err != nil || *answer.NextStartAt != formatTime(next) || !next.After(testNow) || next.After(testNow.Add(time.Minute)) {
		t.Errorf("next_start_at %q, want a contract time 1 to 60 s after %s", *answer.NextStartAt, testTime)
	}
}
