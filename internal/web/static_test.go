package web

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

const (
	appPage   = "<!doctype html><title>app</title>"
	appBundle = "console.log('bundle');"
)

func staticFiles() fstest.MapFS {
	return fstest.MapFS{
		"app.html":                  {Data: []byte(appPage)},
		"_app/immutable/start.js":   {Data: []byte(appBundle)},
		"_app/immutable/style.css":  {Data: []byte("body{}")},
		"download-failed/page.html": {Data: []byte("<h1>failed</h1>")},
	}
}

// The expected results come from the static row of the access table in
// contracts/http-api.md: GET and HEAD only, known files as they are, app.html
// for an unknown path, and 404 for a missing /_app/ file.
func TestStaticGET(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		status      int
		body        string
		contentType string
	}{
		{"known file", "/_app/immutable/start.js", http.StatusOK, appBundle, "text/javascript"},
		{"known stylesheet", "/_app/immutable/style.css", http.StatusOK, "body{}", "text/css"},
		{"root", "/", http.StatusOK, appPage, "text/html"},
		{"unknown path", "/sign-in", http.StatusOK, appPage, "text/html"},
		{"unknown nested path", "/status/anything", http.StatusOK, appPage, "text/html"},
		{"directory outside /_app/", "/download-failed", http.StatusOK, appPage, "text/html"},
		{"missing /_app/ file", "/_app/immutable/gone.js", http.StatusNotFound, "", ""},
		{"directory inside /_app/", "/_app/immutable", http.StatusNotFound, "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, body := requestStatic(t, http.MethodGet, tt.path)

			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
			if tt.status != http.StatusOK {
				if strings.Contains(body, appPage) {
					t.Fatalf("body holds the application page: %q", body)
				}
				return
			}
			if body != tt.body {
				t.Errorf("body = %q, want %q", body, tt.body)
			}
			if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, tt.contentType) {
				t.Errorf("Content-Type = %q, want prefix %q", contentType, tt.contentType)
			}
		})
	}
}

func TestStaticHEAD(t *testing.T) {
	tests := []struct {
		name          string
		path          string
		status        int
		contentLength int
	}{
		{"known file", "/_app/immutable/start.js", http.StatusOK, len(appBundle)},
		{"unknown path", "/sign-in", http.StatusOK, len(appPage)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, _ := requestStatic(t, http.MethodHead, tt.path)

			if response.StatusCode != tt.status {
				t.Fatalf("status = %d, want %d", response.StatusCode, tt.status)
			}
			want := strconv.Itoa(tt.contentLength)
			if got := response.Header.Get("Content-Length"); got != want {
				t.Errorf("Content-Length = %q, want %q", got, want)
			}
		})
	}

	t.Run("missing /_app/ file", func(t *testing.T) {
		response, _ := requestStatic(t, http.MethodHead, "/_app/immutable/gone.js")

		if response.StatusCode != http.StatusNotFound {
			t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusNotFound)
		}
	})
}

func TestStaticRefusesOtherMethods(t *testing.T) {
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		for _, path := range []string{"/", "/app.html", "/_app/immutable/start.js", "/sign-in"} {
			t.Run(method+" "+path, func(t *testing.T) {
				response, body := requestStatic(t, method, path)

				if response.StatusCode != http.StatusMethodNotAllowed {
					t.Fatalf("status = %d, want %d", response.StatusCode, http.StatusMethodNotAllowed)
				}
				if allow := response.Header.Get("Allow"); allow != "GET, HEAD" {
					t.Errorf("Allow = %q, want %q", allow, "GET, HEAD")
				}
				if strings.Contains(body, appPage) || strings.Contains(body, appBundle) {
					t.Errorf("a refused request got a file: %q", body)
				}
			})
		}
	}
}

// requestStatic goes through a real listener, because net/http's handling of
// HEAD bodies and content headers lives in the server, not in the handler.
func requestStatic(t *testing.T, method, path string) (*http.Response, string) {
	t.Helper()
	server := httptest.NewServer(Static(staticFiles()))
	t.Cleanup(server.Close)

	request, err := http.NewRequest(method, server.URL+path, nil)
	if err != nil {
		t.Fatal(err)
	}
	response, err := server.Client().Do(request)
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
