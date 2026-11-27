package web

import (
	"fmt"
	"net/http"
	"os"
	"strings"
	"testing"
)

// The policies the contract gives the view's answers.
const (
	previewPolicy = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data:; " +
		"frame-ancestors 'self'; base-uri 'none'; form-action 'none'"
	previewImagesPolicy = "sandbox; default-src 'none'; style-src 'unsafe-inline'; img-src data: https:; " +
		"frame-ancestors 'self'; base-uri 'none'; form-action 'none'; upgrade-insecure-requests"
	downloadPolicy = "sandbox; default-src 'none'; frame-ancestors 'none'"
)

// newViewHarness is the real route table with interactive workers in the
// options' mode, 1 when unset, and a signed-in session.
func newViewHarness(t *testing.T, options harnessOptions) (*harness, func(*http.Request)) {
	t.Helper()
	if options.views == "" {
		options.views = "1"
	}
	h := newHarnessWith(t, Routes(), options)
	h.createAccount(t, "admin@example.test")
	return h, h.signIn(t, "admin@example.test")
}

// htmlMail is a message whose only body is html.
func htmlMail(html string) []byte {
	return []byte("From: anna@example.test\r\nTo: admin@example.test\r\nSubject: HTML\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n\r\n" + html + "\r\n")
}

// assertOne checks that the response carries header once, with want.
func assertOne(t *testing.T, response *http.Response, header, want string) {
	t.Helper()
	if got := response.Header.Values(header); len(got) != 1 || got[0] != want {
		t.Errorf("%s = %q, want exactly %q", header, got, want)
	}
}

func assertPreviewHeaders(t *testing.T, response *http.Response, policy string) {
	t.Helper()
	assertOne(t, response, "Content-Security-Policy", policy)
	assertOne(t, response, "Referrer-Policy", "no-referrer")
	assertOne(t, response, "X-DNS-Prefetch-Control", "off")
	assertOne(t, response, "X-Content-Type-Options", "nosniff")
	assertOne(t, response, "MailArkiv-Response", "1")
}

func TestPreviewHeaders(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{})
	id := h.importMail(t, session, htmlMail(`<p><a href="https://leak.example.test/">Link</a></p>`), "html.eml")
	plain := h.importMail(t, session, mail("Nur Text"), "plain.eml")
	path := fmt.Sprintf("/api/messages/%d/html", id)

	t.Run("without images", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, session)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", response.StatusCode, body)
		}
		assertOne(t, response, "Content-Type", "text/html; charset=utf-8")
		assertPreviewHeaders(t, response, previewPolicy)
		if !strings.Contains(body, "<a>Link</a>") || strings.Contains(body, "leak.example.test") {
			t.Errorf("body isn't the rewritten HTML: %s", body)
		}
	})

	t.Run("with images", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path+"?images=1", session)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", response.StatusCode, body)
		}
		assertPreviewHeaders(t, response, previewImagesPolicy)
	})

	for _, query := range []string{"?images=0", "?x=1", "?images=1&images=1", "?images=1&x=1", "?images", "?IMAGES=1"} {
		t.Run("query "+query, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, path+query, session)
			assertJSONResponse(t, response, body, http.StatusBadRequest, `{"error":"bad_query"}`)
			assertPreviewHeaders(t, response, previewPolicy)
		})
	}

	t.Run("no HTML body", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/html", plain), session)
		assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"no_body"}`)
		assertPreviewHeaders(t, response, previewPolicy)
	})

	for _, unknown := range []string{"999", "abc", "-1"} {
		t.Run("unknown message "+unknown, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/messages/"+unknown+"/html?images=1", session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
			assertPreviewHeaders(t, response, previewImagesPolicy)
		})
	}

	t.Run("a missing original", func(t *testing.T) {
		gone := h.importMail(t, session, htmlMail("<p>Weg</p>"), "gone.eml")
		if err := os.Remove(h.blobFile(hashOf(htmlMail("<p>Weg</p>")))); err != nil {
			t.Fatal(err)
		}
		response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/html", gone), session)
		assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"stored_original_missing"}`)
		assertPreviewHeaders(t, response, previewPolicy)
	})

	t.Run("no session, opened as a page", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, both(
			withHeader("Sec-Fetch-Dest", "document"), withHeader("Sec-Fetch-Mode", "navigate"),
			withHeader("Referer", h.server.URL+"/messages/5"),
		))
		if response.StatusCode != http.StatusSeeOther {
			t.Fatalf("status = %d %q, want 303", response.StatusCode, body)
		}
		if got, want := response.Header.Get("Location"), "/sign-in?ended=1&next=%2Fmessages%2F5"; got != want {
			t.Errorf("Location = %q, want %q", got, want)
		}
		if strings.Contains(body, "Link") {
			t.Errorf("the redirect carries the message: %q", body)
		}
	})

	for _, tt := range []struct {
		name   string
		change func(*http.Request)
	}{
		{"in the frame", both(withHeader("Sec-Fetch-Dest", "iframe"), withHeader("Sec-Fetch-Mode", "navigate"))},
		{"fetched", both(withHeader("Sec-Fetch-Dest", "empty"), withHeader("Sec-Fetch-Mode", "cors"))},
		{"without fetch headers", nil},
		{"garbage token in the frame", both(withGarbageCookie, withHeader("Sec-Fetch-Dest", "iframe"))},
	} {
		t.Run("no session, "+tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, path+"?images=1", tt.change)
			if response.StatusCode != http.StatusUnauthorized || body != "" {
				t.Fatalf("status = %d %q, want an empty 401", response.StatusCode, body)
			}
			assertPreviewHeaders(t, response, previewImagesPolicy)
		})
	}
}
