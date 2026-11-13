package web

import (
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// encodedForms returns marker and the ways an encoder could have written it:
// lowercase and uppercase hex, and standard and URL-safe base64 at each of
// the three byte offsets a longer encoded value could hold it at.
func encodedForms(marker string) []string {
	hexForm := hex.EncodeToString([]byte(marker))
	forms := []string{marker, hexForm, strings.ToUpper(hexForm)}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		for offset := range 3 {
			encoded := encoding.EncodeToString(append(make([]byte, offset), marker...))
			// The first group of four holds bytes from before the marker,
			// and the last group bytes from after it.
			start := 0
			if offset > 0 {
				start = 4
			}
			forms = append(forms, encoded[start:len(encoded)-4])
		}
	}
	return forms
}

// No answer carries a mailbox password. Every source route gets a request
// that sends the marker password where the route takes one, and each answer's
// body and headers are searched for it, as are the answers that show
// collected mail, the session, and the status.
func TestSourceResponsesHaveNoPassword(t *testing.T) {
	double := newMailbox(t)
	if err := double.AddFolder("Archiv"); err != nil {
		t.Fatal(err)
	}
	if _, err := double.AddMessage("INBOX", mail("collected for the secrets test"), testNow); err != nil {
		t.Fatal(err)
	}
	h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, double), start: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	assertNoPassword := func(t *testing.T, label string, response *http.Response, body string) {
		t.Helper()
		if response.StatusCode >= 300 {
			t.Fatalf("%s = %d %s, want success, so the answer shows a source", label, response.StatusCode, body)
		}
		var headers strings.Builder
		if err := response.Header.Write(&headers); err != nil {
			t.Fatal(err)
		}
		for _, form := range encodedForms(mailboxPassword) {
			if strings.Contains(body, form) || strings.Contains(headers.String(), form) {
				t.Errorf("%s answers with %q, a form of the password", label, form)
			}
		}
	}

	// Each source route's request, by route pattern. A new route must add one
	// here, or the test fails.
	const add = "POST /api/sources"
	requests := map[string]string{
		add:                     string(encode(t, sourceBody(t, double))),
		"GET /api/sources":      "",
		"GET /api/sources/{id}": "",
	}

	// Sent first, so the other routes have a source to answer about.
	response, body := h.send(t, http.MethodPost, "/api/sources", strings.NewReader(requests[add]), session)
	assertNoPassword(t, add, response, body)
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the first sync", func() bool {
		response, body := h.do(t, http.MethodGet, "/api/sources/"+strconv.FormatInt(created.ID, 10), session)
		return response.StatusCode == http.StatusOK && !strings.Contains(body, `"last_success":null`)
	})

	var checked int
	for _, route := range Routes() {
		if !strings.HasPrefix(route.Path, "/api/sources") {
			continue
		}
		checked++
		request, ok := requests[route.pattern()]
		if !ok {
			t.Errorf("%s has no request case, so its answer isn't searched", route.pattern())
			continue
		}
		if route.pattern() == add {
			continue
		}
		t.Run(route.pattern(), func(t *testing.T) {
			response, body := h.send(t, route.Method, concretePath(route.Path, created.ID), strings.NewReader(request), session)
			assertNoPassword(t, route.pattern(), response, body)
		})
	}
	if checked != len(requests) {
		t.Errorf("%d source routes, but %d request cases", checked, len(requests))
	}

	for _, path := range []string{"/api/status", "/api/session", "/api/messages"} {
		t.Run(path, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, path, session)
			assertNoPassword(t, path, response, body)
		})
	}

	// The list shows the collected message, so its answer had a source to
	// leak.
	response, body = h.do(t, http.MethodGet, "/api/messages", session)
	if !strings.Contains(body, `"source_type":"imap_collection"`) {
		t.Errorf("GET /api/messages = %d %s, want a collected message", response.StatusCode, body)
	}
}
