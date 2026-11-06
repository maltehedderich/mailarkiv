package web

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
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

type harness struct {
	server *httptest.Server
	log    *lockedBuffer
}

func newHarness(t *testing.T, routes []Route) *harness {
	t.Helper()
	log := &lockedBuffer{}
	deps := Deps{
		Files:  staticFiles(),
		Logger: slog.New(slog.NewJSONHandler(log, nil)),
	}
	server := httptest.NewServer(newHandler(deps, routes))
	t.Cleanup(server.Close)
	return &harness{server: server, log: log}
}

func (h *harness) do(t *testing.T, method, path string, change func(*http.Request)) (*http.Response, string) {
	t.Helper()
	request, err := http.NewRequest(method, h.server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if change != nil {
		change(request)
	}
	// Redirects stay visible: every response has to carry the headers.
	client := &http.Client{
		Timeout: 10 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	response, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatal(err)
	}
	return response, string(body)
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
	// FR-029: nothing the request supplied reaches the log.
	for _, value := range []string{"PATHVALUE", "QUERYVALUE", "COOKIEVALUE", "HEADERVALUE", "AGENTVALUE"} {
		if strings.Contains(log, value) {
			t.Errorf("log holds the request value %s: %s", value, log)
		}
	}
}

// The checkpoint for this phase: the real route table over static files, and no
// /api/ route that opens without a session.
func TestNewServesTheRouteTable(t *testing.T) {
	server := httptest.NewServer(New(Deps{Files: staticFiles(), Logger: slog.New(slog.DiscardHandler)}))
	t.Cleanup(server.Close)
	h := &harness{server: server}

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
