package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/maltehedderich/mailarkiv/internal/identity"
)

const (
	// maxSignInBody is the contract's limit on a sign-in request.
	maxSignInBody = 16 << 10

	// sessionMaxAge matches the session's 12 hours in the catalog (FR-005).
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
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   sessionMaxAge,
		HttpOnly: true,
		Secure:   true,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// readSession answers GET /api/session. The guard has already checked the
// session, so this only reports the problem count (FR-039).
func readSession(s *server, w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, struct {
		ProblemCount int `json:"problem_count"`
	}{s.status.ProblemCount()})
}
