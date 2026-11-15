package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

const noSession = `{"error":"no_session"}`

// wildcard matches a path wildcard such as {id} in a route pattern.
var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// concretePath fills each wildcard in a route's path with id.
func concretePath(path string, id int64) string {
	return wildcard.ReplaceAllString(path, strconv.FormatInt(id, 10))
}

// routePath fills a route's wildcards: with sourceID under /api/sources, and
// with messageID elsewhere.
func routePath(route Route, messageID, sourceID int64) string {
	if strings.HasPrefix(route.Path, "/api/sources/") {
		return concretePath(route.Path, sourceID)
	}
	return concretePath(route.Path, messageID)
}

// requestBody is what a direct request to the route sends, so a request
// that got through would change something: a valid source for the source
// routes, for the mailbox at addr, and new mail for any other route that
// takes a body. The host is localhost, so a source for 127.0.0.1 isn't a
// duplicate. An empty addr stands for port 993.
func requestBody(t *testing.T, route Route, addr string) io.Reader {
	t.Helper()
	port := 993
	if addr != "" {
		_, portText, err := net.SplitHostPort(addr)
		if err != nil {
			t.Fatal(err)
		}
		if port, err = strconv.Atoi(portText); err != nil {
			t.Fatal(err)
		}
	}
	mailbox := map[string]any{
		"host": "localhost", "port": port, "security": "tls", "user_name": mailboxUser, "password": mailboxPassword,
	}
	switch route.pattern() {
	case "POST /api/sources":
		mailbox["name"] = "Sent directly"
		mailbox["folders"] = []string{"INBOX"}
		return bytes.NewReader(encode(t, mailbox))
	case "POST /api/sources/check":
		return bytes.NewReader(encode(t, mailbox))
	case "PATCH /api/sources/{id}":
		return strings.NewReader(`{"name":"Changed directly","folders":["INBOX","Sent directly"]}`)
	case "POST /api/sources/{id}/check":
		return strings.NewReader(`{}`)
	}
	if route.Method == http.MethodPost && !strings.HasPrefix(route.Path, "/api/sources/") {
		return bytes.NewReader(mail("sent without a session"))
	}
	return nil
}

// sourceState dumps every source and folder row, sealed passwords included,
// so a test can tell whether a request changed any.
func (h *harness) sourceState(t *testing.T) string {
	t.Helper()
	var dump strings.Builder
	for _, table := range []string{"imap_source", "imap_folder", "imap_message_problem"} {
		rows, err := h.db.Reader.Query("SELECT * FROM " + table + " ORDER BY rowid")
		if err != nil {
			t.Fatal(err)
		}
		columns, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(columns))
			pointers := make([]any, len(columns))
			for i := range values {
				pointers[i] = &values[i]
			}
			if err := rows.Scan(pointers...); err != nil {
				t.Fatal(err)
			}
			fmt.Fprintf(&dump, "%s %v\n", table, values)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		rows.Close()
	}
	return dump.String()
}

// withSource is a harness whose collector has a key and trusts a double,
// with one source for it, saved over the API. The collector doesn't start,
// so nothing syncs.
func withSource(t *testing.T) (*harness, *imaptest.Server, int64) {
	t.Helper()
	double := newMailbox(t)
	h := newHarnessWith(t, Routes(), harnessOptions{key: true, roots: trusting(t, double)})
	h.createAccount(t, "source-owner@example.test")
	owner := h.signIn(t, "source-owner@example.test")
	response, body := h.send(t, http.MethodPost, "/api/sources", bytes.NewReader(encode(t, sourceBody(t, double))), owner)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, body)
	}
	return h, double, createdID(t, body)
}

// createdID reads the id from a source answer.
func createdID(t *testing.T, body string) int64 {
	t.Helper()
	var created struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal([]byte(body), &created); err != nil || created.ID == 0 {
		t.Fatalf("no source ID in %q: %v", body, err)
	}
	return created.ID
}

// Generated from the route table, so a new route is covered as soon as it's
// added.
func TestEveryRouteDeniesWithoutSession(t *testing.T) {
	h, double, sourceID := withSource(t)
	h.createAccount(t, "admin@example.test")
	h.createAccount(t, "second@example.test")

	// Each way a session ends, set up in turn. Expiry comes first, because
	// moving the clock ends every session started before it.
	expired := h.signInToken(t, "admin@example.test")
	h.clock.Advance(12 * time.Hour)

	valid := h.signIn(t, "admin@example.test")
	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(mail("already here")), valid)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	id := messageID(t, body)

	signedOut := h.signInToken(t, "admin@example.test")
	response, body = h.do(t, http.MethodDelete, "/api/session", withSession(signedOut))
	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("sign out = %d %s, want 204", response.StatusCode, body)
	}

	passwordChanged := h.signInToken(t, "second@example.test")
	if err := h.identity.SetPassword(context.Background(), "second@example.test", "a brand new password"); err != nil {
		t.Fatal(err)
	}

	without := []struct {
		name   string
		change func(*http.Request)
	}{
		{"no cookie", nil},
		{"garbage token", withGarbageCookie},
		{"expired session", withSession(expired)},
		{"signed-out session", withSession(signedOut)},
		{"session ended by a password change", withSession(passwordChanged)},
	}

	before := h.counts(t)
	sourcesBefore := h.sourceState(t)
	var checked int
	for _, route := range Routes() {
		if route.Access != SignedIn {
			continue
		}
		checked++
		path := routePath(route, id, sourceID)
		for _, tt := range without {
			t.Run(route.pattern()+"/"+tt.name, func(t *testing.T) {
				response, body := h.send(t, route.Method, path, requestBody(t, route, double.Addr()), tt.change)

				if response.StatusCode != http.StatusUnauthorized || body != noSession {
					t.Fatalf("%s %s = %d %q, want 401 %s", route.Method, path, response.StatusCode, body, noSession)
				}
			})
		}
	}
	if checked == 0 {
		t.Fatal("the route table has no SignedIn route")
	}
	if after := h.counts(t); after != before {
		t.Fatalf("the archive changed: %+v, then %+v", before, after)
	}
	if after := h.sourceState(t); after != sourcesBefore {
		t.Fatalf("the sources changed:\n%s\nthen\n%s", sourcesBefore, after)
	}
	if n := double.Logins(); n != 1 {
		t.Errorf("the double saw %d LOGIN commands, want 1: the source's own", n)
	}

	// The valid session still works, so the denials above weren't about it.
	response, body = h.do(t, http.MethodGet, "/api/session", valid)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session with a valid session = %d %s", response.StatusCode, body)
	}
}

// Only sign-in and the static application files are reachable without a
// session. Adding another Public route, or one with no rule, fails here.
func TestOnlyTheAllowlistIsPublic(t *testing.T) {
	allowlist := map[string]bool{
		"POST /api/session": true,
		"/":                 true, // The static files. Their handler takes GET and HEAD only.
	}

	seen := map[string]bool{}
	for _, route := range Routes() {
		pattern := route.pattern()
		switch route.Access {
		case Public:
			if !allowlist[pattern] {
				t.Errorf("%s is Public; only sign-in and static files may be public", pattern)
			}
			seen[pattern] = true
		case SignedIn:
		default:
			t.Errorf("%s declares no access rule", pattern)
		}
		if route.Handler == nil {
			t.Errorf("%s has no handler", pattern)
		}
	}
	for pattern := range allowlist {
		if !seen[pattern] {
			t.Errorf("the allowlist names %s, which isn't a Public route", pattern)
		}
	}
}

// A route with no declared rule answers 403, even to a caller with a valid
// session.
func TestUndeclaredRouteRefusesASignedInCaller(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, append(Routes(),
		Route{Method: http.MethodGet, Path: "/api/undeclared", Handler: recordRun(&ran)},
	))
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	response, body := h.do(t, http.MethodGet, "/api/undeclared", session)

	assertJSONResponse(t, response, body, http.StatusForbidden, `{"error":"no_access_rule"}`)
	if ran.Load() {
		t.Fatal("the handler ran")
	}
}

// Contract: a path under /api/ with no route answers 401 without a session and
// 404 with one.
func TestUnknownAPIPath(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	for _, tt := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/nothing-here"},
		{http.MethodGet, "/api/messages/1/elsewhere"},
		{http.MethodPut, "/api/messages"},
		{http.MethodPatch, "/api/session"},
	} {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			response, body := h.do(t, tt.method, tt.path, nil)
			assertJSONResponse(t, response, body, http.StatusUnauthorized, noSession)

			response, body = h.do(t, tt.method, tt.path, session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
		})
	}
}

func withHeader(name, value string) func(*http.Request) {
	return func(request *http.Request) {
		request.Header.Set(name, value)
	}
}

// Contract, Download: the address opened in a tab with no session lands on sign-in
// with the session-ended message, and returns to the page it came from. Any
// other request without a session gets 401.
func TestDownloadWithoutSessionRedirectsANavigation(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	expired := h.signInToken(t, "admin@example.test")
	h.clock.Advance(12 * time.Hour)
	session := h.signIn(t, "admin@example.test")
	content := mail("linked")
	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	path := "/api/messages/" + strconv.FormatInt(messageID(t, body), 10) + "/original"
	navigate := withHeader("Sec-Fetch-Mode", "navigate")
	sameOrigin := h.server.URL

	redirects := []struct {
		name     string
		change   func(*http.Request)
		location string
	}{
		{"same-origin Referer with a query", both(navigate, withHeader("Referer", sameOrigin+"/?page=3")), "/sign-in?ended=1&next=%2F%3Fpage%3D3"},
		{"same-origin Referer at the root", both(navigate, withHeader("Referer", sameOrigin+"/")), "/sign-in?ended=1&next=%2F"},
		{"same-origin Referer with two parameters", both(navigate, withHeader("Referer", sameOrigin+"/?message=5&page=2")), "/sign-in?ended=1&next=%2F%3Fmessage%3D5%26page%3D2"},
		{"Referer from another site", both(navigate, withHeader("Referer", "http://elsewhere.test/?page=3")), "/sign-in?ended=1&next=%2F"},
		{"Referer from another port on the same host", both(navigate, withHeader("Referer", "http://127.0.0.1:1/?page=3")), "/sign-in?ended=1&next=%2F"},
		{"no Referer", navigate, "/sign-in?ended=1&next=%2F"},
		{"garbage token", both(navigate, withGarbageCookie, withHeader("Referer", sameOrigin+"/?page=3")), "/sign-in?ended=1&next=%2F%3Fpage%3D3"},
		{"expired session", both(navigate, withSession(expired), withHeader("Referer", sameOrigin+"/?page=3")), "/sign-in?ended=1&next=%2F%3Fpage%3D3"},
	}
	for _, tt := range redirects {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, path, tt.change)

			if response.StatusCode != http.StatusSeeOther {
				t.Fatalf("status = %d %q, want 303", response.StatusCode, body)
			}
			if got := response.Header.Get("Location"); got != tt.location {
				t.Errorf("Location = %q, want %q", got, tt.location)
			}
			if got := response.Header.Get("MailArkiv-Response"); got != "1" {
				t.Errorf("MailArkiv-Response = %q, want 1", got)
			}
			if response.Header.Get("Content-Disposition") != "" || strings.Contains(body, "linked") {
				t.Errorf("the redirect carries the message: %q", body)
			}
		})
	}

	t.Run("a request that isn't a navigation", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, both(withHeader("Sec-Fetch-Mode", "cors"), withHeader("Referer", sameOrigin+"/?page=3")))
		assertJSONResponse(t, response, body, http.StatusUnauthorized, noSession)
	})

	t.Run("a navigation with a session downloads", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, both(navigate, session, withHeader("Referer", sameOrigin+"/")))
		if response.StatusCode != http.StatusOK || body != string(content) {
			t.Fatalf("status = %d, want 200 with the original", response.StatusCode)
		}
	})
}

// Contract: a failed session lookup answers 500 and doesn't run the route. It
// never answers 401 or redirects, because the session may still be valid.
func TestEverySignedInRouteFailsClosedWhenTheCatalogFails(t *testing.T) {
	var ran atomic.Bool
	routes := Routes()
	for i := range routes {
		if routes[i].Access == SignedIn {
			routes[i].Handler = recordRun(&ran)
		}
	}
	h := newHarness(t, routes)
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}

	for _, route := range routes {
		if route.Access != SignedIn {
			continue
		}
		path := concretePath(route.Path, 1)
		t.Run(route.pattern(), func(t *testing.T) {
			change := both(session, withHeader("Sec-Fetch-Mode", "navigate"), withHeader("Referer", h.server.URL+"/"))
			response, body := h.send(t, route.Method, path, requestBody(t, route, ""), change)

			assertJSONResponse(t, response, body, http.StatusInternalServerError, `{"error":"internal_error"}`)
			if ran.Load() {
				t.Fatal("the handler ran")
			}
		})
	}
}
