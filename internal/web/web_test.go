package web

import (
	"bytes"
	"context"
	"crypto/x509"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/collect"
	"github.com/maltehedderich/mailarkiv/internal/identity"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// lockedBuffer holds log output. The handler writes on the server's goroutine,
// and the test reads on its own.
type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// testNow is where the fake clock starts. Sessions last 12 hours, so none
// expires unless a test moves the clock.
var testNow = time.Date(2026, 11, 6, 15, 50, 52, 123_000_000, time.UTC)

// fakeClock stands in for time, the one nondeterministic collaborator here.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// harness is the handler over a real listener, with real modules, SQLite, and
// files behind it.
type harness struct {
	server    *httptest.Server
	log       *lockedBuffer
	dataDir   string
	db        *sqlitedb.DB
	identity  *identity.Identity
	archive   *archive.Archive
	status    *opstatus.Status
	clock     *fakeClock
	collector *collect.Collector
}

// harnessOptions choose how the harness collects mail. The zero value has a
// collector with no key that never starts, so the source routes exist and
// nothing syncs.
type harnessOptions struct {
	// noCollector leaves Deps.Collect nil, as serve does without
	// --preview-collection.
	noCollector bool
	// key gives the collector a key in a file outside the data directory.
	key bool
	// roots are the CAs the collector trusts, for a double's certificate.
	roots *x509.CertPool
	// start starts the collector. It stops when the test ends.
	start bool
}

func newHarness(t *testing.T, routes []Route) *harness {
	t.Helper()
	return newHarnessWith(t, routes, harnessOptions{})
}

func newHarnessWith(t *testing.T, routes []Route, options harnessOptions) *harness {
	t.Helper()
	h := &harness{log: &lockedBuffer{}}
	deps := h.deps(t)
	if !options.noCollector {
		deps.Collect = h.openCollector(t, options)
	}
	h.server = httptest.NewServer(newHandler(deps, routes))
	t.Cleanup(h.server.Close)
	return h
}

func (h *harness) openCollector(t *testing.T, options harnessOptions) *collect.Collector {
	t.Helper()
	var key *passwordkey.Key
	if options.key {
		path := filepath.Join(t.TempDir(), "key")
		if err := passwordkey.Create(path); err != nil {
			t.Fatal(err)
		}
		var err error
		if key, err = passwordkey.Load(path); err != nil {
			t.Fatal(err)
		}
	}
	collector, err := collect.Open(context.Background(), collect.Deps{
		DB:      h.db,
		Archive: h.archive,
		Key:     key,
		Logger:  slog.New(slog.NewJSONHandler(h.log, nil)),
		Now:     h.clock.Now,
		Roots:   options.roots,
	})
	if err != nil {
		t.Fatal(err)
	}
	if options.start {
		collector.Start()
		t.Cleanup(collector.Stop)
	}
	h.collector = collector
	return collector
}

// deps opens a catalog in a new data directory and builds the modules on it.
func (h *harness) deps(t *testing.T) Deps {
	t.Helper()
	logger := slog.New(slog.NewJSONHandler(h.log, nil))
	h.clock = &fakeClock{now: testNow}
	now := h.clock.Now

	h.dataDir = filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(h.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.Open(filepath.Join(h.dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	h.db = db
	h.identity = identity.New(db, now)

	status := opstatus.New(logger, now)
	files, err := archive.Open(h.dataDir, db, status, logger, now)
	if err != nil {
		t.Fatal(err)
	}
	h.archive = files
	h.status = status
	return Deps{
		Files:    staticFiles(),
		Logger:   logger,
		Identity: h.identity,
		Archive:  files,
		Status:   status,
		Labels:   collect.NewLabeler(db),
	}
}

func (h *harness) do(t *testing.T, method, path string, change func(*http.Request)) (*http.Response, string) {
	t.Helper()
	return h.send(t, method, path, nil, change)
}

// send makes a request with a body. A *bytes.Reader or *strings.Reader body
// sends Content-Length. Any other reader sends a chunked body.
func (h *harness) send(t *testing.T, method, path string, body io.Reader, change func(*http.Request)) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, body)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(request)
	}
	response, err := testClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	read, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(read)
}

// testClient leaves redirects visible: every response has to carry the
// headers. The timeout is long enough for a 150 MB body under the race
// detector.
var testClient = &http.Client{
	Timeout: 2 * time.Minute,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// Go tests send the cookie by hand. net/http/cookiejar withholds Secure cookies
// over plain HTTP.
func withGarbageCookie(request *http.Request) {
	request.Header.Set("Cookie", "__Host-mailarkiv_session=garbage")
}

// recordRun is a handler that only notes that it ran.
func recordRun(ran *atomic.Bool) func(*server, http.ResponseWriter, *http.Request) {
	return func(*server, http.ResponseWriter, *http.Request) { ran.Store(true) }
}

func TestRouteWithNoDeclaredRuleAnswers403(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, []Route{
		{Method: http.MethodGet, Path: "/api/undeclared", Handler: recordRun(&ran)},
		{Method: http.MethodPost, Path: "/api/undeclared", Access: Undeclared, Handler: recordRun(&ran)},
	})

	tests := []struct {
		name   string
		method string
		change func(*http.Request)
	}{
		{"GET with no session", http.MethodGet, nil},
		{"GET with a cookie", http.MethodGet, withGarbageCookie},
		{"POST with no session", http.MethodPost, nil},
		{"POST with a cookie", http.MethodPost, withGarbageCookie},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := h.do(t, tt.method, "/api/undeclared", tt.change)

			if response.StatusCode != http.StatusForbidden {
				t.Fatalf("status = %d, want 403", response.StatusCode)
			}
			if ran.Load() {
				t.Fatal("the handler ran")
			}
		})
	}
}

func TestSignedInRouteWithoutSessionAnswers401(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, []Route{
		{Method: http.MethodGet, Path: "/api/private", Access: SignedIn, Handler: recordRun(&ran)},
	})

	for _, tt := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"no cookie", nil},
		{"garbage cookie", withGarbageCookie},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/private", tt.change)

			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.StatusCode)
			}
			if body != `{"error":"no_session"}` {
				t.Fatalf("body = %q", body)
			}
			if got := response.Header.Get("Content-Type"); got != "application/json" {
				t.Errorf("Content-Type = %q, want application/json", got)
			}
			if ran.Load() {
				t.Fatal("the handler ran")
			}
		})
	}
}

func TestUnknownAPIPathAnswers401WithoutSession(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, []Route{
		{Method: http.MethodGet, Path: "/api/known", Access: Public, Handler: recordRun(&ran)},
	})

	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"unknown path", http.MethodGet, "/api/nothing-here"},
		{"unknown nested path", http.MethodGet, "/api/nothing/here/either"},
		{"the bare prefix", http.MethodGet, "/api/"},
		{"unknown path with POST", http.MethodPost, "/api/nothing-here"},
		{"unknown method on a known path", http.MethodDelete, "/api/known"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, tt.method, tt.path, nil)

			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", response.StatusCode)
			}
			if body != `{"error":"no_session"}` {
				t.Fatalf("body = %q", body)
			}
			if ran.Load() {
				t.Fatal("the route's handler ran")
			}
		})
	}
}

func TestPublicRouteRunsWithoutSession(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, []Route{
		{Method: http.MethodPost, Path: "/api/open", Access: Public, Handler: recordRun(&ran)},
	})

	response, _ := h.do(t, http.MethodPost, "/api/open", nil)

	if response.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", response.StatusCode)
	}
	if !ran.Load() {
		t.Fatal("the handler didn't run")
	}
}

// anyRedirect stands for any 3xx status in a test table.
const anyRedirect = -1

func TestEveryResponseCarriesTheHeaders(t *testing.T) {
	h := newHarness(t, append(Routes(),
		Route{Method: http.MethodGet, Path: "/api/undeclared"},
		Route{Method: http.MethodGet, Path: "/api/private", Access: SignedIn},
		Route{Method: http.MethodGet, Path: "/api/broken", Access: Public, Handler: func(s *server, w http.ResponseWriter, r *http.Request) {
			s.writeInternalError(w, r, errors.New("broken"))
		}},
		Route{Method: http.MethodGet, Path: "/api/open", Access: Public, Handler: func(_ *server, w http.ResponseWriter, _ *http.Request) {
			writeError(w, http.StatusBadRequest, "bad_page")
		}},
	))

	tests := []struct {
		name   string
		method string
		path   string
		status int
		isAPI  bool
	}{
		{"static file", http.MethodGet, "/_app/immutable/start.js", http.StatusOK, false},
		{"static HEAD", http.MethodHead, "/_app/immutable/start.js", http.StatusOK, false},
		{"application page", http.MethodGet, "/sign-in", http.StatusOK, false},
		{"missing static file", http.MethodGet, "/_app/immutable/gone.js", http.StatusNotFound, false},
		{"POST to a static path", http.MethodPost, "/sign-in", http.StatusMethodNotAllowed, false},
		// ServeMux redirects it. Which redirect status it picks isn't ours to fix.
		{"unclean path", http.MethodGet, "/a/../b", anyRedirect, false},
		{"unknown API path", http.MethodGet, "/api/nothing", http.StatusUnauthorized, true},
		{"undeclared route", http.MethodGet, "/api/undeclared", http.StatusForbidden, true},
		{"signed-in route with no session", http.MethodGet, "/api/private", http.StatusUnauthorized, true},
		{"internal error", http.MethodGet, "/api/broken", http.StatusInternalServerError, true},
		{"handler's own error", http.MethodGet, "/api/open", http.StatusBadRequest, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := h.do(t, tt.method, tt.path, nil)

			switch {
			case tt.status == anyRedirect:
				if response.StatusCode < 300 || response.StatusCode > 399 {
					t.Fatalf("status = %d, want a redirect", response.StatusCode)
				}
			case response.StatusCode != tt.status:
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
			want := map[string]string{
				"MailArkiv-Response":      "1",
				"Content-Security-Policy": "frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
				"X-Content-Type-Options":  "nosniff",
				"Referrer-Policy":         "same-origin",
			}
			if tt.isAPI {
				want["Cache-Control"] = "no-store"
			}
			for name, value := range want {
				if got := response.Header.Get(name); got != value {
					t.Errorf("%s = %q, want %q", name, got, value)
				}
			}
		})
	}
}

func TestInternalError(t *testing.T) {
	const pattern = "GET /api/broken/{id}"
	const failure = "the disk stopped answering"
	h := newHarness(t, []Route{
		{Method: http.MethodGet, Path: "/api/broken/{id}", Access: Public, Handler: func(s *server, w http.ResponseWriter, r *http.Request) {
			s.writeInternalError(w, r, errors.New(failure))
		}},
	})

	response, body := h.do(t, http.MethodGet, "/api/broken/PATHVALUE?filter=QUERYVALUE", func(request *http.Request) {
		request.Header.Set("Cookie", "__Host-mailarkiv_session=COOKIEVALUE")
		request.Header.Set("MailArkiv-File-Name", "HEADERVALUE")
		request.Header.Set("User-Agent", "AGENTVALUE")
	})

	if response.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", response.StatusCode)
	}
	if body != `{"error":"internal_error"}` {
		t.Fatalf("body = %q", body)
	}
	if got := response.Header.Get("MailArkiv-Response"); got != "1" {
		t.Errorf("MailArkiv-Response = %q, want 1", got)
	}

	log := h.log.String()
	var entry map[string]any
	if err := json.Unmarshal([]byte(log), &entry); err != nil {
		t.Fatalf("log isn't one JSON line: %q: %v", log, err)
	}
	if entry["route"] != pattern {
		t.Errorf("route = %v, want %q", entry["route"], pattern)
	}
	if entry["error"] != failure {
		t.Errorf("error = %v, want %q", entry["error"], failure)
	}
	// Nothing the request supplied reaches the log.
	for _, value := range []string{"PATHVALUE", "QUERYVALUE", "COOKIEVALUE", "HEADERVALUE", "AGENTVALUE"} {
		if strings.Contains(log, value) {
			t.Errorf("log holds the request value %s: %s", value, log)
		}
	}
}

// The checkpoint for this phase: the real route table over static files, and no
// /api/ route that opens without a session.
func TestNewServesTheRouteTable(t *testing.T) {
	h := &harness{log: &lockedBuffer{}}
	h.server = httptest.NewServer(New(h.deps(t)))
	t.Cleanup(h.server.Close)

	response, body := h.do(t, http.MethodGet, "/api/messages", nil)
	if response.StatusCode != http.StatusUnauthorized || body != `{"error":"no_session"}` {
		t.Errorf("GET /api/messages = %d %q, want 401 no_session", response.StatusCode, body)
	}

	response, body = h.do(t, http.MethodGet, "/", nil)
	if response.StatusCode != http.StatusOK || body != appPage {
		t.Errorf("GET / = %d %q, want 200 and the application page", response.StatusCode, body)
	}

	response, _ = h.do(t, http.MethodHead, "/_app/immutable/start.js", nil)
	if response.StatusCode != http.StatusOK {
		t.Errorf("HEAD on a static file = %d, want 200", response.StatusCode)
	}
}

// Contract: times are RFC 3339 in UTC with milliseconds.
func TestFormatTime(t *testing.T) {
	berlin := time.FixedZone("CET", 60*60)
	tests := []struct {
		name string
		in   time.Time
		want string
	}{
		{"from UTC", time.Date(2026, 11, 6, 15, 50, 52, 123_000_000, time.UTC), "2026-11-06T15:50:52.123Z"},
		{"from another zone", time.Date(2026, 11, 6, 16, 50, 52, 123_000_000, berlin), "2026-11-06T15:50:52.123Z"},
		{"keeps zero milliseconds", time.Date(2026, 11, 6, 15, 0, 0, 0, time.UTC), "2026-11-06T15:00:00.000Z"},
		{"drops what's below a millisecond", time.Date(2026, 11, 6, 15, 0, 0, 123_999_999, time.UTC), "2026-11-06T15:00:00.123Z"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := formatTime(tt.in); got != tt.want {
				t.Fatalf("formatTime = %q, want %q", got, tt.want)
			}
		})
	}
}

const crossOrigin = `{"error":"cross_origin"}`

// A state-changing request from another site is refused before any route
// runs, with or without a session, and stores nothing.
func TestCrossOriginRequestsAreRefused(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(mail("stored before")), session)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}
	id := messageID(t, body)
	before := h.counts(t)

	forgeries := []struct {
		name   string
		change func(*http.Request)
	}{
		{"Sec-Fetch-Site cross-site", withHeader("Sec-Fetch-Site", "cross-site")},
		{"Origin of another site and no Sec-Fetch-Site", withHeader("Origin", "http://elsewhere.test")},
	}
	var checked int
	for _, route := range Routes() {
		if route.Method == http.MethodGet {
			continue
		}
		method, path := route.Method, concretePath(route.Path, id)
		if method == "" {
			// The static files' route takes every method, and only GET and HEAD
			// are served.
			method, path = http.MethodPost, "/sign-in"
		}
		checked++
		for _, forgery := range forgeries {
			for _, signedIn := range []bool{true, false} {
				name := method + " " + path + "/" + forgery.name
				change := forgery.change
				if signedIn {
					name += "/with a session"
					change = both(session, forgery.change)
				}
				t.Run(name, func(t *testing.T) {
					var requestBody io.Reader = bytes.NewReader(mail("forged"))
					if path == "/api/session" && method == http.MethodPost {
						requestBody = strings.NewReader(`{"email":"admin@example.test","password":"correct horse battery"}`)
					}
					response, body := h.send(t, method, path, requestBody, change)

					assertJSONResponse(t, response, body, http.StatusForbidden, crossOrigin)
					if cookie := response.Header.Get("Set-Cookie"); cookie != "" {
						t.Errorf("Set-Cookie = %q, want none", cookie)
					}
				})
			}
		}
	}
	if checked == 0 {
		t.Fatal("the route table has no state-changing route")
	}

	if after := h.counts(t); after != before {
		t.Fatalf("the archive changed: %+v, then %+v", before, after)
	}
	// The forged sign-out didn't end the session.
	response, body = h.do(t, http.MethodGet, "/api/session", session)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/session after the forgeries = %d %s, want 200", response.StatusCode, body)
	}
}

// A request from this site's own pages, or from a client that isn't a
// browser, reaches its route.
func TestSameOriginRequestsReachTheRoute(t *testing.T) {
	var ran atomic.Bool
	h := newHarness(t, append(Routes(),
		Route{Method: http.MethodPost, Path: "/api/open", Access: Public, Handler: recordRun(&ran)},
	))

	for _, tt := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"Sec-Fetch-Site same-origin", withHeader("Sec-Fetch-Site", "same-origin")},
		{"no Sec-Fetch-Site and no Origin", nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ran.Store(false)
			response, body := h.do(t, http.MethodPost, "/api/open", tt.change)

			if response.StatusCode != http.StatusOK || !ran.Load() {
				t.Fatalf("status = %d %q, ran = %v; want the handler to run", response.StatusCode, body, ran.Load())
			}
		})
	}

	t.Run("a same-origin POST to a static path isn't served", func(t *testing.T) {
		response, body := h.do(t, http.MethodPost, "/sign-in", withHeader("Sec-Fetch-Site", "same-origin"))
		if response.StatusCode != http.StatusMethodNotAllowed || strings.Contains(body, appPage) {
			t.Fatalf("status = %d %q, want 405 without the application page", response.StatusCode, body)
		}
	})
}
