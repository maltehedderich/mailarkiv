package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/search"
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
	type rule struct {
		name   string
		body   string
		status int
		want   string
	}
	tests := []rule{
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
		{"an instant", withField("list_date_from", `"2026-01-01T00:00:00.000+01:00"`), http.StatusServiceUnavailable, unavailable},
		{"an instant in UTC", withField("list_date_before", `"2026-01-01T00:00:00Z"`), http.StatusServiceUnavailable, unavailable},
		{"a day isn't an instant", withField("list_date_before", `"2026-01-01"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_before"]}`},
		{"a time without a zone", withField("list_date_from", `"2026-01-01T00:00:00"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_from"]}`},
		{"a German date", withField("list_date_from", `"01.01.2026"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_from"]}`},
		{"Unix milliseconds", withField("list_date_from", `1767222000000`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_from"]}`},
		{"a date missing", withField("list_date_before", ""), http.StatusBadRequest, `{"error":"invalid_fields","fields":["list_date_before"]}`},
		{"has_attachments true", withField("has_attachments", "true"), http.StatusServiceUnavailable, unavailable},
		{"has_attachments null", withField("has_attachments", "null"), http.StatusBadRequest, `{"error":"invalid_fields","fields":["has_attachments"]}`},
		{"has_attachments a string", withField("has_attachments", `"true"`), http.StatusBadRequest, `{"error":"invalid_fields","fields":["has_attachments"]}`},
		{"has_attachments a number", withField("has_attachments", "1"), http.StatusBadRequest, `{"error":"invalid_fields","fields":["has_attachments"]}`},
		{"several broken fields", `{"words":"","from":7,"to":"","subject":"","attachment":"","list_date_from":null,"list_date_before":"morgen","has_attachments":"ja","page":1}`,
			http.StatusBadRequest, `{"error":"invalid_fields","fields":["from","list_date_before","has_attachments"]}`},
		{"every broken field named", `{"words":1,"from":"","to":"","subject":"","attachment":"","list_date_from":"x","list_date_before":null,"has_attachments":false,"page":0}`,
			http.StatusBadRequest, `{"error":"invalid_fields","fields":["words","list_date_from","page"]}`},
	}
	for _, field := range []string{"from", "to", "subject", "attachment"} {
		tests = append(tests,
			rule{field + " of 1,000 characters", withField(field, quote(strings.Repeat("ä", 1000))), http.StatusServiceUnavailable, unavailable},
			rule{field + " of 1,001 characters", withField(field, quote(strings.Repeat("ä", 1001))), http.StatusBadRequest,
				`{"error":"invalid_fields","fields":["` + field + `"]}`},
		)
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

// searchSet is the fixed set in testdata/searchset, in the order the test
// imports it. unreadable-header comes last, through collection, because an
// import refuses it.
var searchSet = []string{
	"anna-com-bericht", "anna-com-vertrag", "anna-com-rueckfrage", "anna-org-abstimmung",
	"anna-org-apfelernte", "apfel", "attacker-zahlung", "bob-com-termin", "carol-rechnung",
	"embedded-image", "entwurf", "punkt-a", "no-date", "unreadable-date", "future-date",
}

// searchAnswer is the part of a search's answer the fixed set checks.
type searchAnswer struct {
	Items []struct {
		ID int64 `json:"id"`
	} `json:"items"`
	Total       int  `json:"total"`
	NotIncluded int  `json:"not_included"`
	FullList    bool `json:"full_list"`
}

// filtered is a search body for page 1 with every field empty except those
// in fields.
func filtered(t *testing.T, fields map[string]any) string {
	t.Helper()
	body := map[string]any{
		"words": "", "from": "", "to": "", "subject": "", "attachment": "",
		"list_date_from": nil, "list_date_before": nil, "has_attachments": false, "page": 1,
	}
	for field, value := range fields {
		body[field] = value
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

// Every case in the quickstart's search table and every filter, against the
// real search process. Day ranges are instants for Europe/Berlin, as a
// browser there sends them, so the days around both daylight saving changes
// in 2026 are 23 and 25 hours long.
func TestSearchFixedSet(t *testing.T) {
	h := newHarnessWith(t, Routes(), harnessOptions{search: searchProgram(t)})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	ids := map[string]int64{}
	read := func(name string) []byte {
		content, err := os.ReadFile(filepath.Join("testdata", "searchset", name+".eml"))
		if err != nil {
			t.Fatal(err)
		}
		return content
	}
	for _, name := range searchSet {
		ids[name] = h.importMail(t, session, read(name), name+".eml")
	}
	sourceID := h.insertKeylessSource(t)
	var folderID int64
	if err := h.db.Reader.QueryRow("SELECT id FROM imap_folder WHERE source_id = ?", sourceID).Scan(&folderID); err != nil {
		t.Fatal(err)
	}
	collected, err := h.archive.StoreCollected(context.Background(), bytes.NewReader(read("unreadable-header")),
		archive.CollectedMeta{SourceID: sourceID, FolderID: folderID})
	if err != nil {
		t.Fatal(err)
	}
	ids["unreadable-header"] = collected.(archive.Stored).Message.ID

	run := func(t *testing.T, body string) searchAnswer {
		t.Helper()
		response, text := h.send(t, http.MethodPost, "/api/search", strings.NewReader(body), session)
		var answer searchAnswer
		if response.StatusCode != http.StatusOK || json.Unmarshal([]byte(text), &answer) != nil {
			t.Fatalf("search %s = %d %s", body, response.StatusCode, text)
		}
		return answer
	}
	names := func(answer searchAnswer) []string {
		byID := map[int64]string{}
		for name, id := range ids {
			byID[id] = name
		}
		found := []string{}
		for _, item := range answer.Items {
			found = append(found, byID[item.ID])
		}
		return found
	}

	// Every message but the one that couldn't be read gets a document.
	everything := filtered(t, map[string]any{"list_date_from": "2000-01-01T00:00:00.000Z"})
	waitFor(t, "every readable message in the index", func() bool {
		answer := run(t, everything)
		return answer.Total == len(searchSet) && answer.NotIncluded == 0
	})

	day := func(first, after string) map[string]any {
		return map[string]any{"list_date_from": first, "list_date_before": after}
	}
	with := func(fields map[string]any, field string, value any) map[string]any {
		fields[field] = value
		return fields
	}
	tests := []struct {
		name   string
		fields map[string]any
		want   []string
	}{
		{"words quartals", map[string]any{"words": "quartals"}, []string{"anna-com-bericht"}},
		{"from an address", map[string]any{"from": "anna@example.com"},
			[]string{"anna-com-rueckfrage", "anna-com-vertrag", "anna-com-bericht"}},
		{"from an address in capitals", map[string]any{"from": "Anna@Example.COM"},
			[]string{"anna-com-rueckfrage", "anna-com-vertrag", "anna-com-bericht"}},
		{"from a domain", map[string]any{"from": "example.com"},
			[]string{"anna-com-rueckfrage", "bob-com-termin", "anna-com-vertrag", "anna-com-bericht"}},
		{"from a domain with @", map[string]any{"from": "@example.com"},
			[]string{"anna-com-rueckfrage", "bob-com-termin", "anna-com-vertrag", "anna-com-bericht"}},
		{"from a subdomain", map[string]any{"from": "mail.example.com"}, []string{"carol-rechnung"}},
		{"from words", map[string]any{"from": "anna"},
			[]string{"anna-com-rueckfrage", "anna-org-abstimmung", "anna-org-apfelernte", "anna-com-vertrag", "anna-com-bericht"}},
		{"to an address", map[string]any{"to": "anna@example.com"}, []string{"attacker-zahlung", "bob-com-termin"}},
		{"to an address only in Cc", map[string]any{"to": "dora@example.com"}, []string{"anna-org-abstimmung"}},
		{"to an address only in Bcc", map[string]any{"to": "erik@example.net"}, []string{"bob-com-termin"}},
		{"to a domain", map[string]any{"to": "example.com"},
			[]string{"attacker-zahlung", "anna-org-abstimmung", "bob-com-termin", "anna-com-vertrag"}},
		{"subject", map[string]any{"subject": "vertrag"}, []string{"anna-com-vertrag"}},
		{"words in any field", map[string]any{"words": "vertrag"}, []string{"entwurf", "anna-com-vertrag"}},
		{"attachment name", map[string]any{"attachment": "vertrag"}, []string{"anna-com-vertrag"}},
		{"words with an accent", map[string]any{"words": "müller"}, []string{"carol-rechnung", "bob-com-termin"}},
		{"words without the accent", map[string]any{"words": "muller"}, []string{"attacker-zahlung"}},
		{"one character", map[string]any{"words": "a"}, []string{"punkt-a"}},
		{"a prefix", map[string]any{"words": "apfel"}, []string{"apfel", "anna-org-apfelernte"}},
		{"has attachments", map[string]any{"has_attachments": true}, []string{"entwurf", "anna-com-vertrag"}},
		{"words and a filter", map[string]any{"words": "vertrag", "from": "anna@example.com"}, []string{"anna-com-vertrag"}},
		{"punctuation with a filter", map[string]any{"words": "?!", "has_attachments": true}, []string{"entwurf", "anna-com-vertrag"}},
		{"a message that couldn't be read", map[string]any{"words": "unlesbar"}, []string{}},
		{"2026-03-28", day("2026-03-28T00:00:00.000+01:00", "2026-03-29T00:00:00.000+01:00"),
			[]string{"embedded-image", "punkt-a", "anna-com-bericht"}},
		{"2026-03-29, 23 hours", day("2026-03-29T00:00:00.000+01:00", "2026-03-30T00:00:00.000+02:00"),
			[]string{"bob-com-termin", "anna-com-vertrag"}},
		{"2026-03-30", day("2026-03-30T00:00:00.000+02:00", "2026-03-31T00:00:00.000+02:00"),
			[]string{"apfel", "anna-org-apfelernte"}},
		{"2026-10-24", day("2026-10-24T00:00:00.000+02:00", "2026-10-25T00:00:00.000+02:00"),
			[]string{"anna-org-abstimmung"}},
		{"2026-10-25, 25 hours", day("2026-10-25T00:00:00.000+02:00", "2026-10-26T00:00:00.000+01:00"),
			[]string{"anna-com-rueckfrage", "carol-rechnung"}},
		{"2026-10-26", day("2026-10-26T00:00:00.000+01:00", "2026-10-27T00:00:00.000+01:00"),
			[]string{"entwurf", "attacker-zahlung"}},
		{"words in a range", with(day("2026-03-29T00:00:00.000+01:00", "2026-03-31T00:00:00.000+02:00"), "words", "anna"),
			[]string{"anna-org-apfelernte", "bob-com-termin", "anna-com-vertrag"}},
		{"only a first day", map[string]any{"list_date_from": "2026-10-26T00:00:00.000+01:00"},
			[]string{"future-date", "unreadable-date", "no-date", "entwurf", "attacker-zahlung"}},
		{"only a last day", map[string]any{"list_date_before": "2026-03-29T00:00:00.000+01:00"},
			[]string{"embedded-image", "punkt-a", "anna-com-bericht"}},
		// A missing, unreadable, or far-ahead Date lists the message at its
		// archive time.
		{"the archive day", day("2026-11-06T00:00:00.000+01:00", "2026-11-07T00:00:00.000+01:00"),
			[]string{"future-date", "unreadable-date", "no-date"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			answer := run(t, filtered(t, tt.fields))
			if got := names(answer); !slices.Equal(got, tt.want) || answer.Total != len(tt.want) ||
				answer.NotIncluded != 0 || answer.FullList {
				t.Errorf("found %v, total %d, %d not included, full list %t; want %v",
					got, answer.Total, answer.NotIncluded, answer.FullList, tt.want)
			}
		})
	}

	t.Run("punctuation alone gives the full list", func(t *testing.T) {
		answer := run(t, filtered(t, map[string]any{"words": "?!"}))
		want := []string{
			"unreadable-header", "future-date", "unreadable-date", "no-date", "entwurf", "attacker-zahlung",
			"anna-com-rueckfrage", "carol-rechnung", "anna-org-abstimmung", "apfel", "anna-org-apfelernte",
			"bob-com-termin", "anna-com-vertrag", "embedded-image", "punkt-a", "anna-com-bericht",
		}
		if got := names(answer); !slices.Equal(got, want) || answer.Total != len(want) || !answer.FullList {
			t.Errorf("found %v, total %d, full list %t; want the full list %v", got, answer.Total, answer.FullList, want)
		}
	})

	t.Run("a message not processed yet isn't included", func(t *testing.T) {
		h.stopProcessing()
		h.importMail(t, session, plainFixture(t, "plain-text.eml"), "plan.eml")
		answer := run(t, filtered(t, map[string]any{"words": "quartals"}))
		if got := names(answer); !slices.Equal(got, []string{"anna-com-bericht"}) || answer.NotIncluded != 1 {
			t.Errorf("found %v with %d not included, want anna-com-bericht with 1", got, answer.NotIncluded)
		}
	})
}

func TestSearchCallLimitAnswers(t *testing.T) {
	answers := func(t *testing.T, faults string) (*http.Response, string, time.Duration) {
		t.Setenv("MAILARKIV_SEARCH_FAULTS", faults)
		h := newHarnessWith(t, Routes(), harnessOptions{search: faultsSearchProgram(t)})
		h.createAccount(t, "admin@example.test")
		session := h.signIn(t, "admin@example.test")
		h.importMail(t, session, plainFixture(t, "plain-text.eml"), "plan.eml")
		waitFor(t, "the message in the index", func() bool {
			return h.search.State().Indexing == search.IndexingUpToDate && h.search.State().Search == search.SearchAvailable
		})
		started := time.Now()
		response, body := h.send(t, http.MethodPost, "/api/search", strings.NewReader(searchBody("wochenplan", 1)), session)
		return response, body, time.Since(started)
	}

	t.Run("a search stopped at its deadline", func(t *testing.T) {
		response, body, _ := answers(t, "during-search=fail")
		assertJSONResponse(t, response, body, http.StatusUnprocessableEntity, `{"error":"search_took_too_long"}`)
	})

	t.Run("a search that doesn't answer", func(t *testing.T) {
		response, body, took := answers(t, "during-search=hang")
		assertJSONResponse(t, response, body, http.StatusServiceUnavailable, `{"error":"search_did_not_answer"}`)
		if took > 13*time.Second {
			t.Errorf("the answer took %s, want at most 13 s", took)
		}
	})
}
