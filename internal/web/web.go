package web

import (
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"time"
)

// Access is a route's rule for who may call it. The zero value, Undeclared,
// denies everyone, so a route added without a decision stays closed (FR-006).
// Its field is private, so no other package can make a fourth rule.
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
		{Path: "/", Access: Public, Handler: serveStatic},
	}
}

// Deps is what the HTTP adapter needs from the rest of the program.
type Deps struct {
	// Files holds the built interface, with app.html at its root.
	Files  fs.FS
	Logger *slog.Logger
}

type server struct {
	logger *slog.Logger
	static http.Handler
}

// New returns the server's handler.
func New(deps Deps) http.Handler {
	return newHandler(deps, Routes())
}

func newHandler(deps Deps, routes []Route) http.Handler {
	s := &server{logger: deps.Logger, static: Static(deps.Files)}

	mux := http.NewServeMux()
	for _, route := range routes {
		mux.Handle(route.pattern(), s.guard(route))
	}
	// Any method on an /api/ path with no route lands here, including a known
	// path with an unknown method.
	mux.HandleFunc(apiPrefix, s.unknownAPI)

	return withHeaders(mux)
}

// guard applies the route's access rule before its handler runs. A handler that
// reads a body therefore never reads one from a caller without a session.
func (s *server) guard(route Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch route.Access {
		case Public:
		case SignedIn:
			if !s.hasSession(r) {
				writeError(w, http.StatusUnauthorized, "no_session")
				return
			}
		default:
			writeError(w, http.StatusForbidden, "no_access_rule")
			return
		}
		route.Handler(s, w, r)
	})
}

// hasSession reports whether the request carries a valid session. Accounts
// arrive with the identity module. Until then no request has a session, so
// every SignedIn route fails closed.
func (s *server) hasSession(*http.Request) bool {
	return false
}

// unknownAPI answers 404 only to a caller with a session, so a caller without
// one learns nothing about which paths exist.
func (s *server) unknownAPI(w http.ResponseWriter, r *http.Request) {
	if !s.hasSession(r) {
		writeError(w, http.StatusUnauthorized, "no_session")
		return
	}
	writeError(w, http.StatusNotFound, "not_found")
}

func serveStatic(s *server, w http.ResponseWriter, r *http.Request) {
	s.static.ServeHTTP(w, r)
}

// writeError writes the contract's error body, { "error": "<code>" }.
func writeError(w http.ResponseWriter, status int, code string) {
	body, err := json.Marshal(struct {
		Error string `json:"error"`
	}{code})
	if err != nil {
		// A struct holding one string always marshals.
		panic(err)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	// A failed write means the client left. There's no one to tell.
	_, _ = w.Write(body)
}

// writeInternalError logs the route pattern and the error, and nothing the
// request supplied (FR-029), then answers 500 internal_error.
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
