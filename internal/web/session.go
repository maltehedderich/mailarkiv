package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/maltehedderich/mailarkiv/internal/identity"
)

const (
	// Even six-byte JSON escapes for every credential byte fit, including
	// the email/password keys, quotes, and punctuation (at most 30,746 bytes).
	maxSignInBody = 32 << 10

	// sessionMaxAge matches the session's 12 hours in the catalog.
	sessionMaxAge = 12 * 60 * 60
)

// signIn answers POST /api/session. A body that can't be read as credentials
// answers like wrong credentials, because no address was read to count the
// failure against.
func signIn(s *server, w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxSignInBody))
	if err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	var credentials struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.Unmarshal(body, &credentials); err != nil {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}

	token, err := s.identity.SignIn(r.Context(), credentials.Email, credentials.Password)
	if errors.Is(err, identity.ErrInvalidCredentials) {
		writeError(w, http.StatusUnauthorized, "invalid_credentials")
		return
	}
	if errors.Is(err, identity.ErrSignInBlocked) {
		writeError(w, http.StatusTooManyRequests, "sign_in_blocked")
		return
	}
	if errors.Is(err, identity.ErrSignInUnavailable) {
		writeError(w, http.StatusServiceUnavailable, "sign_in_unavailable")
		return
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	http.SetCookie(w, newSessionCookie(token, sessionMaxAge))
	w.WriteHeader(http.StatusNoContent)
}

// signOut answers DELETE /api/session. The guard has already checked the
// session, so this ends it and clears the cookie.
func signOut(s *server, w http.ResponseWriter, r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		s.writeInternalError(w, r, fmt.Errorf("read the session cookie: %w", err))
		return
	}
	if err := s.identity.SignOut(r.Context(), cookie.Value); err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	// A negative MaxAge sends Max-Age=0, which removes the cookie at once.
	http.SetCookie(w, newSessionCookie("", -1))
	w.WriteHeader(http.StatusNoContent)
}

func newSessionCookie(value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name:     sessionCookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	}
}

// readSession answers GET /api/session. The guard has already checked the
// session, so this only reports the problem count.
func readSession(s *server, w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		ProblemCount int `json:"problem_count"`
	}{s.status.ProblemCount()})
}
