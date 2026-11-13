package web

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/collect"
	"github.com/maltehedderich/mailarkiv/internal/identity"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// Access is a route's rule for who may call it. The zero value, Undeclared,
// denies everyone, so a route added without a decision stays closed. Its
// field is private, so no other package can make a fourth rule.
type Access struct{ name string }

var (
	Undeclared = Access{}
	Public     = Access{"public"}
	SignedIn   = Access{"signed_in"}
)

// Route is one entry in the route table.
type Route struct {
	// Method is empty only for the static files. Their handler answers 405 to
	// anything but GET and HEAD itself, because a "GET /" pattern would
	// conflict with the /api/ catch-all in ServeMux.
	Method  string
	Path    string
	Access  Access
	Handler func(*server, http.ResponseWriter, *http.Request)
	// NoSession replaces a SignedIn route's 401 answer for a caller without a
	// valid session. Nil means the 401. It never runs when the lookup failed.
	NoSession func(http.ResponseWriter, *http.Request)
	// Collection routes exist only when Deps.Collect is set. Without it their
	// paths are unknown, like any other.
	Collection bool
}

func (r Route) pattern() string {
	if r.Method == "" {
		return r.Path
	}
	return r.Method + " " + r.Path
}

// Routes returns the route table, newly built on each call so a caller can
// extend it.
func Routes() []Route {
	return []Route{
		{Method: http.MethodPost, Path: "/api/session", Access: Public, Handler: signIn},
		{Method: http.MethodGet, Path: "/api/session", Access: SignedIn, Handler: readSession},
		{Method: http.MethodDelete, Path: "/api/session", Access: SignedIn, Handler: signOut},
		{Method: http.MethodPost, Path: "/api/imports", Access: SignedIn, Handler: importFile},
		{Method: http.MethodGet, Path: "/api/messages", Access: SignedIn, Handler: listMessages},
		{Method: http.MethodGet, Path: "/api/messages/{id}", Access: SignedIn, Handler: readHistory},
		{Method: http.MethodGet, Path: "/api/messages/{id}/original", Access: SignedIn, Handler: downloadOriginal, NoSession: redirectToSignIn},
		{Method: http.MethodGet, Path: "/api/status", Access: SignedIn, Handler: readStatus},
		{Method: http.MethodPost, Path: "/api/sources/check", Access: SignedIn, Handler: checkMailbox, Collection: true},
		{Method: http.MethodPost, Path: "/api/sources", Access: SignedIn, Handler: addSource, Collection: true},
		{Method: http.MethodGet, Path: "/api/sources", Access: SignedIn, Handler: listSources, Collection: true},
		{Method: http.MethodGet, Path: "/api/sources/{id}", Access: SignedIn, Handler: readSource, Collection: true},
		{Method: http.MethodPatch, Path: "/api/sources/{id}", Access: SignedIn, Handler: editSource, Collection: true},
		{Method: http.MethodPost, Path: "/api/sources/{id}/check", Access: SignedIn, Handler: checkStoredSource, Collection: true},
		{Method: http.MethodPost, Path: "/api/sources/{id}/pause", Access: SignedIn, Handler: pauseSource, Collection: true},
		{Method: http.MethodPost, Path: "/api/sources/{id}/resume", Access: SignedIn, Handler: resumeSource, Collection: true},
		{Method: http.MethodPost, Path: "/api/sources/{id}/sync", Access: SignedIn, Handler: syncSource, Collection: true},
		{Method: http.MethodDelete, Path: "/api/sources/{id}", Access: SignedIn, Handler: removeSource, Collection: true},
		{Path: "/", Access: Public, Handler: serveStatic},
	}
}

// Deps is what the HTTP adapter needs from the rest of the program.
type Deps struct {
	// Files holds the built interface, with app.html at its root.
	Files    fs.FS
	Logger   *slog.Logger
	Identity *identity.Identity
	Archive  *archive.Archive
	Status   *opstatus.Status
	// Labels names the folders of collection entries.
	Labels *collect.Labeler
	// Collect is nil when serve doesn't collect mail.
	Collect *collect.Collector
}

type server struct {
	logger   *slog.Logger
	static   http.Handler
	identity *identity.Identity
	archive  *archive.Archive
	status   *opstatus.Status
	labels   *collect.Labeler
	collect  *collect.Collector
}

// New returns the server's handler.
func New(deps Deps) http.Handler {
	return newHandler(deps, Routes())
}

func newHandler(deps Deps, routes []Route) http.Handler {
	s := &server{
		logger:   deps.Logger,
		static:   Static(deps.Files),
		identity: deps.Identity,
		archive:  deps.Archive,
		status:   deps.Status,
		labels:   deps.Labels,
		collect:  deps.Collect,
	}

	mux := http.NewServeMux()
	for _, route := range routes {
		if route.Collection && deps.Collect == nil {
			continue
		}
		mux.Handle(route.pattern(), s.guard(route))
	}
	// Any method on an /api/ path with no route lands here, including a known
	// path with an unknown method.
	mux.HandleFunc(apiPrefix, s.unknownAPI)

	return withHeaders(withCrossOriginCheck(mux))
}

// guard applies the route's access rule before its handler runs. A handler that
// reads a body therefore never reads one from a caller without a session.
func (s *server) guard(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch route.Access {
		case Public:
		case SignedIn:
			noSession := route.NoSession
			if noSession == nil {
				noSession = answerNoSession
			}
			account, ok := s.checkSession(w, r, noSession)
			if !ok {
				return
			}
			r = r.WithContext(context.WithValue(r.Context(), accountKey{}, account))
		default:
			writeError(w, http.StatusForbidden, "no_access_rule")
			return
		}
		route.Handler(s, w, r)
	})
}

// sessionCookieName has the __Host- prefix, so the browser accepts the cookie
// only with Secure, Path=/, and no Domain.
const sessionCookieName = "__Host-mailarkiv_session"

// answerNoSession is the answer to a caller without a valid session.
func answerNoSession(w http.ResponseWriter, _ *http.Request) {
	writeError(w, http.StatusUnauthorized, "no_session")
}

// checkSession finds the request's account. When it returns false, it has
// already answered: through noSession for no valid session, or 500 when the
// lookup failed, because the session may still be valid.
func (s *server) checkSession(w http.ResponseWriter, r *http.Request, noSession func(http.ResponseWriter, *http.Request)) (identity.Account, bool) {
	cookie, err := r.Cookie(sessionCookieName)
	if err != nil {
		noSession(w, r)
		return identity.Account{}, false
	}
	account, err := s.identity.LookupSession(r.Context(), cookie.Value)
	if errors.Is(err, identity.ErrNoSession) {
		noSession(w, r)
		return identity.Account{}, false
	}
	if err != nil {
		s.writeInternalError(w, r, err)
		return identity.Account{}, false
	}
	return account, true
}

type accountKey struct{}

// signedInAccount returns the account the guard found. Only a SignedIn route's
// handler may call it.
func signedInAccount(r *http.Request) identity.Account {
	account, ok := r.Context().Value(accountKey{}).(identity.Account)
	if !ok {
		panic("signedInAccount called outside a SignedIn route")
	}
	return account
}

// unknownAPI answers 404 only to a caller with a session, so a caller without
// one learns nothing about which paths exist.
func (s *server) unknownAPI(w http.ResponseWriter, r *http.Request) {
	if _, ok := s.checkSession(w, r, answerNoSession); !ok {
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func serveStatic(s *server, w http.ResponseWriter, r *http.Request) {
	s.static.ServeHTTP(w, r)
}

// writeError writes the API error body, { "error": "<code>" }.
func writeError(w http.ResponseWriter, status int, code string) {
	writeJSON(w, status, struct {
		Error string `json:"error"`
	}{code})
}

// writeJSON writes v as the response body. v holds only strings, numbers, and
// structs of them, so it always marshals.
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	// Without a length, a flush before the handler returns would send the answer
	// chunked, and a client couldn't tell it was complete until the handler ended.
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	// A failed write means the client left. There's no one to tell.
	_, _ = w.Write(body)
}

// writeInternalError logs the route pattern and the error, and nothing the
// request supplied, then answers 500 internal_error.
func (s *server) writeInternalError(w http.ResponseWriter, r *http.Request, err error) {
	s.logger.LogAttrs(r.Context(), slog.LevelError, "request failed",
		slog.String("route", r.Pattern),
		slog.String("error", err.Error()),
	)
	writeError(w, http.StatusInternalServerError, "internal_error")
}

// formatTime writes a time the way the API contract does: RFC 3339 in UTC with
// milliseconds.
func formatTime(t time.Time) string {
	return t.UTC().Format("2006-01-02T15:04:05.000Z07:00")
}
