package web

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"
)

type listedObservation struct {
	SourceType   string  `json:"source_type"`
	ObservedAt   string  `json:"observed_at"`
	AccountEmail string  `json:"account_email"`
	FileName     *string `json:"file_name"`
}

type listedHistory struct {
	ID           int64               `json:"id"`
	ContentHash  string              `json:"content_hash"`
	Size         int64               `json:"size"`
	Observations []listedObservation `json:"observations"`
}

// history reads a message's history and fails the test on anything but 200.
func (h *harness) history(t *testing.T, session func(*http.Request), id int64) listedHistory {
	t.Helper()
	response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d", id), session)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/messages/%d = %d %s, want 200", id, response.StatusCode, body)
	}
	if got := response.Header.Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
	var history listedHistory
	if err := json.Unmarshal([]byte(body), &history); err != nil {
		t.Fatalf("body isn't the contract's history: %q: %v", body, err)
	}
	return history
}

// Repeat imports add history and no copies.
func TestRepeatImports(t *testing.T) {
	h := newHarness(t, Routes())
	// Created with a capital letter, and recorded in lowercase.
	h.createAccount(t, "Admin@example.test")
	h.createAccount(t, "second@example.test")
	admin := h.signIn(t, "admin@example.test")
	second := h.signIn(t, "second@example.test")
	content := mail("repeated")

	type sent struct {
		name    string
		account string
		at      time.Time
	}
	var imports []sent
	var id int64
	for i := range 10 {
		h.clock.Advance(time.Minute)
		step := sent{name: "first.eml", account: "admin@example.test", at: h.clock.Now()}
		session := admin
		if i%2 == 1 {
			step.name = "second.eml"
			step.account = "second@example.test"
			session = second
		}
		imports = append(imports, step)

		response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), both(session, withFileName(step.name)))
		var stored struct {
			Outcome string `json:"outcome"`
			Message struct {
				ID          int64 `json:"id"`
				ImportCount int   `json:"import_count"`
			} `json:"message"`
		}
		if err := json.Unmarshal([]byte(body), &stored); err != nil {
			t.Fatalf("import %d: %q: %v", i, body, err)
		}
		wantStatus, wantOutcome := http.StatusOK, "already_archived"
		if i == 0 {
			wantStatus, wantOutcome = http.StatusCreated, "archived"
			id = stored.Message.ID
		}
		if response.StatusCode != wantStatus || stored.Outcome != wantOutcome {
			t.Fatalf("import %d = %d %s, want %d %s", i, response.StatusCode, body, wantStatus, wantOutcome)
		}
		if stored.Message.ID != id || stored.Message.ImportCount != i+1 {
			t.Fatalf("import %d = %s, want id %d and import_count %d", i, body, id, i+1)
		}
	}

	latest := imports[len(imports)-1]
	response, body := h.do(t, http.MethodGet, "/api/messages", admin)
	assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(
		`{"items":[{"id":%d,"content_hash":%q,"size":%d,"file_name":%q,"imported_at":%q,"import_count":10}],"page":1,"page_size":50,"total":1}`,
		id, hashOf(content), len(content), latest.name, formatTime(latest.at)))

	history := h.history(t, admin, id)
	if history.ID != id || history.ContentHash != hashOf(content) || history.Size != int64(len(content)) {
		t.Errorf("history = %d %s %d, want %d %s %d", history.ID, history.ContentHash, history.Size, id, hashOf(content), len(content))
	}
	if len(history.Observations) != len(imports) {
		t.Fatalf("observations = %d, want %d", len(history.Observations), len(imports))
	}
	for i, observation := range history.Observations {
		want := imports[len(imports)-1-i]
		if observation.SourceType != "manual_import" {
			t.Errorf("observation %d source_type = %q, want manual_import", i, observation.SourceType)
		}
		if observation.ObservedAt != formatTime(want.at) {
			t.Errorf("observation %d observed_at = %q, want %q", i, observation.ObservedAt, formatTime(want.at))
		}
		if observation.AccountEmail != want.account {
			t.Errorf("observation %d account_email = %q, want %q", i, observation.AccountEmail, want.account)
		}
		if observation.FileName == nil || *observation.FileName != want.name {
			t.Errorf("observation %d file_name = %v, want %q", i, observation.FileName, want.name)
		}
	}

	if got, want := h.counts(t), (archiveCounts{messages: 1, observations: 10, blobs: 1}); got != want {
		t.Errorf("the archive holds %+v, want %+v", got, want)
	}
}

// Contract: file_name is null when an import supplied none, and an observation
// that has one stays apart from one that doesn't.
func TestHistoryOfAnImportWithNoName(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	content := mail("unnamed")

	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	id := messageID(t, body)

	response, body = h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d", id), session)
	assertJSONResponse(t, response, body, http.StatusOK, fmt.Sprintf(
		`{"id":%d,"content_hash":%q,"size":%d,"observations":[{"source_type":"manual_import","observed_at":%q,"account_email":"admin@example.test","file_name":null}]}`,
		id, hashOf(content), len(content), testTime))
}

// Bytes identify a message, and headers don't.
func TestFilesDifferingByOneByteAreTwoMessages(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	const headers = "From: sender@example.test\r\nSubject: Twin\r\nMessage-ID: <twin@example.test>\r\n\r\n"
	for _, content := range []string{headers + "body a\r\n", headers + "body b\r\n"} {
		response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader([]byte(content)), session)
		if response.StatusCode != http.StatusCreated {
			t.Fatalf("import = %d %s, want 201 for different bytes", response.StatusCode, body)
		}
	}

	response, body := h.do(t, http.MethodGet, "/api/messages", session)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/messages = %d %s", response.StatusCode, body)
	}
	var page listedPage
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != 2 || len(page.Items) != 2 {
		t.Fatalf("list = %s, want two entries", body)
	}
	if got := h.counts(t).blobs; got != 2 {
		t.Errorf("blobs/ holds %d files, want 2", got)
	}
}

// Contract: GET /api/messages/{id} answers 404 not_found for an unknown ID.
func TestHistoryOfAnUnknownMessage(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	for _, id := range []string{"1", "0", "-1", "abc", "99999999999999999999"} {
		t.Run(id, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/messages/"+id, session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
		})
	}
}
