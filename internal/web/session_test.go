package web

import (
	"context"
	"crypto/sha256"
	"net/http"
	"strings"
	"sync"
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

// answer is what a caller can see of a sign-in attempt.
type answer struct {
	status int
	body   string
	cookie string
}

// FR-003, FR-004, risk 13: a known and an unknown address get byte-identical
// answers at the first failure and when blocked, so the answers don't reveal
// which accounts exist.
func TestSignInLimitAnswersAlike(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	const known, unknown = "admin@example.test", "nobody@example.test"

	attempt := func(t *testing.T, address, password string) answer {
		t.Helper()
		body := `{"email":` + quote(address) + `,"password":` + quote(password) + `}`
		response, read := h.send(t, http.MethodPost, "/api/session", strings.NewReader(body), nil)
		return answer{response.StatusCode, read, response.Header.Get("Set-Cookie")}
	}

	var mu sync.Mutex
	firsts := map[string]answer{}
	blocked := map[string]answer{}
	t.Run("attempts", func(t *testing.T) {
		for _, address := range []string{known, unknown} {
			t.Run(address, func(t *testing.T) {
				t.Parallel()
				first := attempt(t, address, "wrong horse battery")
				for i := 2; i <= 10; i++ {
					if got := attempt(t, address, "wrong horse battery"); got.status != http.StatusUnauthorized {
						t.Fatalf("failure %d = %d %s, want 401", i, got.status, got.body)
					}
				}
				refused := attempt(t, address, goodPassword)
				mu.Lock()
				defer mu.Unlock()
				firsts[address], blocked[address] = first, refused
			})
		}
	})
	if t.Failed() {
		return
	}

	if want := (answer{http.StatusUnauthorized, invalidCredentials, ""}); firsts[known] != want || firsts[unknown] != want {
		t.Errorf("first failure: known %+v, unknown %+v, want both %+v", firsts[known], firsts[unknown], want)
	}
	if want := (answer{http.StatusTooManyRequests, `{"error":"sign_in_blocked"}`, ""}); blocked[known] != want || blocked[unknown] != want {
		t.Errorf("blocked: known %+v, unknown %+v, want both %+v", blocked[known], blocked[unknown], want)
	}
}

// FR-003, FR-005: sign-out ends this session and no other, deletes its row, and
// clears the cookie.
func TestSignOut(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	token := h.signInToken(t, "admin@example.test")
	other := h.signIn(t, "admin@example.test")

	response, body := h.do(t, http.MethodDelete, "/api/session", withSession(token))

	if response.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d %q, want 204", response.StatusCode, body)
	}
	if body != "" {
		t.Errorf("body = %q, want none", body)
	}
	const cleared = "__Host-mailarkiv_session=; Path=/; Max-Age=0; HttpOnly; Secure; SameSite=Strict"
	if cookies := response.Header.Values("Set-Cookie"); len(cookies) != 1 || cookies[0] != cleared {
		t.Errorf("Set-Cookie = %q, want %q", cookies, cleared)
	}

	tokenHash := sha256.Sum256([]byte(token))
	var rows int
	if err := h.db.Reader.QueryRowContext(context.Background(), "SELECT COUNT(*) FROM session WHERE token_hash = ?", tokenHash[:]).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 0 {
		t.Errorf("session rows for the token = %d, want 0", rows)
	}

	response, body = h.do(t, http.MethodGet, "/api/session", withSession(token))
	assertJSONResponse(t, response, body, http.StatusUnauthorized, `{"error":"no_session"}`)
	response, body = h.do(t, http.MethodGet, "/api/session", other)
	assertJSONResponse(t, response, body, http.StatusOK, `{"problem_count":0}`)
}
