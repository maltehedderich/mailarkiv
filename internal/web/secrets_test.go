package web

import (
	"encoding/base64"
	"encoding/hex"
	"net/http"
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

	// Each source route's request, in an order where each answers with
	// success: the add first, so the others have a source, and the removal
	// last. then waits for what the request started. A new route must add a
	// step here, or the test fails.
	check := sourceBody(t, double)
	delete(check, "name")
	delete(check, "folders")
	// localhost names the same double, and isn't a duplicate of 127.0.0.1.
	check["host"] = "localhost"
	var id int64
	waiting := func(t *testing.T) { h.waitUntilWaiting(t, session, id) }
	marker := quote(mailboxPassword)
	steps := []struct {
		pattern string
		body    string
		then    func(t *testing.T)
	}{
		{"POST /api/sources", string(encode(t, sourceBody(t, double))), waiting},
		{"POST /api/sources/check", string(encode(t, check)), nil},
		{"GET /api/sources", "", nil},
		{"GET /api/sources/{id}", "", nil},
		{"POST /api/sources/{id}/check", `{"password":` + marker + `}`, nil},
		// Saves the marker password, which signs in and syncs.
		{"PATCH /api/sources/{id}", `{"name":"Anna","password":` + marker + `}`, waiting},
		{"POST /api/sources/{id}/pause", "", nil},
		{"POST /api/sources/{id}/resume", "", waiting},
		{"POST /api/sources/{id}/sync", "", waiting},
		{"DELETE /api/sources/{id}", "", nil},
	}

	routes := map[string]Route{}
	for _, route := range Routes() {
		if strings.HasPrefix(route.Path, "/api/sources") {
			routes[route.pattern()] = route
		}
	}
	covered := map[string]bool{}
	for _, step := range steps {
		covered[step.pattern] = true
	}
	for pattern := range routes {
		if !covered[pattern] {
			t.Errorf("%s has no step, so its answer isn't searched", pattern)
		}
	}

	for _, step := range steps {
		route, ok := routes[step.pattern]
		if !ok {
			t.Fatalf("the step %s names no route", step.pattern)
		}
		response, body := h.send(t, route.Method, concretePath(route.Path, id), strings.NewReader(step.body), session)
		assertNoPassword(t, step.pattern, response, body)
		if id == 0 {
			id = createdID(t, body)
		}
		if step.then != nil {
			step.then(t)
		}
	}

	for _, path := range []string{"/api/status", "/api/session", "/api/messages"} {
		t.Run(path, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, path, session)
			assertNoPassword(t, path, response, body)
		})
	}

	// The list shows the collected message, so its answer had a source to
	// leak.
	response, body := h.do(t, http.MethodGet, "/api/messages", session)
	if !strings.Contains(body, `"source_type":"imap_collection"`) {
		t.Errorf("GET /api/messages = %d %s, want a collected message", response.StatusCode, body)
	}
}
