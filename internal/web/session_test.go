package web

import (
	"context"
	"net/http"
	"strings"
	"testing"
)

const invalidCredentials = `{"error":"invalid_credentials"}`

// Contract: POST /api/session answers 204 and sets the session cookie, and the
// cookie opens GET /api/session.
func TestSignIn(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "Admin@example.test")

	body := `{"email":"admin@example.test","password":"correct horse battery"}`
	response, read := h.send(t, http.MethodPost, "/api/session", strings.NewReader(body), nil)

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body %s", response.StatusCode, read)
	}
	if read != "" {
		t.Errorf("body = %q, want none", read)
	}
	cookies := response.Header.Values("Set-Cookie")
	if len(cookies) != 1 {
		t.Fatalf("Set-Cookie = %q, want one cookie", cookies)
	}
	match := sessionCookie.FindStringSubmatch(cookies[0])
	if match == nil {
		t.Fatalf("Set-Cookie = %q, want __Host-mailarkiv_session=<token>; Path=/; Max-Age=43200; HttpOnly; Secure; SameSite=Strict", cookies[0])
	}

	response, read = h.do(t, http.MethodGet, "/api/session", withSession(match[1]))
	assertJSONResponse(t, response, read, http.StatusOK, `{"problem_count":0}`)
}

// FR-003, scenario 5: a wrong password, an unknown address, and a malformed
// address get the same answer, so the answer doesn't reveal which accounts
// exist.
func TestSignInRefusals(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")

	tests := []struct {
		name string
		body string
	}{
		{"wrong password", `{"email":"admin@example.test","password":"wrong horse battery"}`},
		{"unknown address", `{"email":"nobody@example.test","password":"correct horse battery"}`},
		{"malformed address", `{"email":"admin.example.test","password":"correct horse battery"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, read := h.send(t, http.MethodPost, "/api/session", strings.NewReader(tt.body), nil)

			assertJSONResponse(t, response, read, http.StatusUnauthorized, invalidCredentials)
			if read != invalidCredentials {
				t.Errorf("body = %q, want exactly %q", read, invalidCredentials)
			}
			if cookie := response.Header.Get("Set-Cookie"); cookie != "" {
				t.Errorf("Set-Cookie = %q, want none", cookie)
			}
		})
	}
}

// Contract: the request is at most 16 KiB. A body that can't be read as
// credentials answers like wrong credentials and names no address.
func TestSignInBodyLimits(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	credentials := `{"email":"admin@example.test","password":"correct horse battery"}`
	padTo := func(size int) string {
		return credentials + strings.Repeat(" ", size-len(credentials))
	}

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"not JSON", `email=admin@example.test&password=correct+horse+battery`, http.StatusUnauthorized},
		{"cut-off JSON", credentials[:len(credentials)-1], http.StatusUnauthorized},
		{"wrong types", `{"email":["admin@example.test"],"password":"correct horse battery"}`, http.StatusUnauthorized},
		{"empty", ``, http.StatusUnauthorized},
		{"correct credentials padded to 16 KiB and 1 byte", padTo(16<<10 + 1), http.StatusUnauthorized},
		{"correct credentials padded to exactly 16 KiB", padTo(16 << 10), http.StatusNoContent},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, read := h.send(t, http.MethodPost, "/api/session", strings.NewReader(tt.body), nil)

			if tt.status == http.StatusNoContent {
				if response.StatusCode != http.StatusNoContent {
					t.Fatalf("status = %d, want 204; body %s", response.StatusCode, read)
				}
				return
			}
			assertJSONResponse(t, response, read, tt.status, invalidCredentials)
		})
	}

	var failures int
	if err := h.db.Reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM sign_in_failure").Scan(&failures); err != nil {
		t.Fatal(err)
	}
	if failures != 0 {
		t.Errorf("sign_in_failure has %d rows, want 0: no address was read", failures)
	}
}

// Contract: a failed sign-in or session lookup answers 500. It never answers
// 401, because the credentials or the session may still be valid.
func TestSessionWhenTheCatalogFails(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}

	body := `{"email":"admin@example.test","password":"correct horse battery"}`
	response, read := h.send(t, http.MethodPost, "/api/session", strings.NewReader(body), nil)
	assertJSONResponse(t, response, read, http.StatusInternalServerError, `{"error":"internal_error"}`)

	response, read = h.do(t, http.MethodGet, "/api/session", session)
	assertJSONResponse(t, response, read, http.StatusInternalServerError, `{"error":"internal_error"}`)
}
