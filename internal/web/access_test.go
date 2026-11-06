package web

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const noSession = `{"error":"no_session"}`

// wildcard matches a path wildcard such as {id} in a route pattern.
var wildcard = regexp.MustCompile(`\{[^}]+\}`)

// concretePath fills each wildcard in a route's path with id.
func concretePath(path string, id int64) string {
	return wildcard.ReplaceAllString(path, strconv.FormatInt(id, 10))
}

// requestBody is what a direct request to the route sends: new mail for a
// route that takes a body, so a request that got through would store it.
func requestBody(route Route) io.Reader {
	if route.Method == http.MethodPost {
		return bytes.NewReader(mail("sent without a session"))
	}
	return nil
}

// FR-006, SC-004, User Story 2 scenarios 1 and 2. Generated from the route
// table, so a new route is covered as soon as it's added.
func TestEveryRouteDeniesWithoutSession(t *testing.T) {
	h := newHarness(t, Routes())
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
	var checked int
	for _, route := range Routes() {
		if route.Access != SignedIn {
			continue
		}
		checked++
		path := concretePath(route.Path, id)
		for _, tt := range without {
			t.Run(route.pattern()+"/"+tt.name, func(t *testing.T) {
				response, body := h.send(t, route.Method, path, requestBody(route), tt.change)

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

	// The valid session still works, so the denials above weren't about it.
	response, body = h.do(t, http.MethodGet, "/api/session", valid)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session with a valid session = %d %s", response.StatusCode, body)
	}
}

// FR-006: only sign-in and the static application files are reachable without
// a session. Adding another Public route, or one with no rule, fails here.
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
				t.Errorf("%s is Public, and FR-006 allows only sign-in and the static files", pattern)
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

// User Story 2 scenario 5: a route with no declared rule answers 403, even to
// a caller with a valid session.
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

// Contract, Download: a plain link followed with no session lands on sign-in
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
			response, body := h.send(t, route.Method, path, requestBody(route), change)

			assertJSONResponse(t, response, body, http.StatusInternalServerError, `{"error":"internal_error"}`)
			if ran.Load() {
				t.Fatal("the handler ran")
			}
		})
	}
}
