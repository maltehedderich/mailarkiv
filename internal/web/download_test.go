package web

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"testing"
)

// FR-023, risk 10, and the contract's download table: the server hashes the
// stored file before it sends any header, so a damaged copy is never saved as
// the message, and the interface learns why from the error code.
func TestDownloadVerifies(t *testing.T) {
	type wanted struct {
		status   int
		category string
	}
	const (
		missing = "stored_original_missing"
		damaged = "stored_original_damaged"
	)
	tests := []struct {
		name   string
		change func(t *testing.T, h *harness, hash string)
		want   wanted
	}{
		{
			name: "a missing file",
			change: func(t *testing.T, h *harness, hash string) {
				if err := os.Remove(h.blobFile(hash)); err != nil {
					t.Fatal(err)
				}
			},
			want: wanted{http.StatusNotFound, missing},
		},
		{
			name: "a file with one byte added",
			change: func(t *testing.T, h *harness, hash string) {
				file, err := os.OpenFile(h.blobFile(hash), os.O_APPEND|os.O_WRONLY, 0)
				if err != nil {
					t.Fatal(err)
				}
				defer file.Close()
				if _, err := file.Write([]byte("x")); err != nil {
					t.Fatal(err)
				}
			},
			want: wanted{http.StatusInternalServerError, damaged},
		},
		{
			name: "a file with one byte changed and the same size",
			change: func(t *testing.T, h *harness, hash string) {
				body, err := os.ReadFile(h.blobFile(hash))
				if err != nil {
					t.Fatal(err)
				}
				body[len(body)-1] ^= 0xff
				h.writeBlob(t, hash, body)
			},
			want: wanted{http.StatusInternalServerError, damaged},
		},
		{
			name: "an unreadable file",
			change: func(t *testing.T, h *harness, hash string) {
				h.makeBlobUnreadable(t, hash)
			},
			want: wanted{http.StatusInternalServerError, damaged},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t, Routes())
			h.createAccount(t, "admin@example.test")
			session := h.signIn(t, "admin@example.test")
			content := mail(tt.name)
			response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
			}
			id := messageID(t, body)
			hash := hashOf(content)
			tt.change(t, h, hash)

			response, body = h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/original", id), session)

			assertFailedDownload(t, response, body, tt.want.status, tt.want.category)
			failures := h.status.Snapshot().Failures
			if len(failures) != 1 {
				t.Fatalf("failures = %+v, want 1", failures)
			}
			event := failures[0]
			if event.Category.String() != tt.want.category {
				t.Errorf("category = %s, want %s", event.Category, tt.want.category)
			}
			if event.MessageID == nil || *event.MessageID != id {
				t.Errorf("message ID = %v, want %d", event.MessageID, id)
			}
			if event.ContentHash == nil || *event.ContentHash != hash {
				t.Errorf("content hash = %v, want %s", event.ContentHash, hash)
			}
			if event.Restored {
				t.Error("restored = true, and no import replaced the file")
			}
		})
	}
}

// Contract: a matching file gives 200 with the exact bytes and no failure event.
func TestDownloadOfAMatchingFile(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	content := mail("matching")
	response, body := h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(content), session)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, body)
	}

	response, body = h.do(t, http.MethodGet, "/api/messages/"+strconv.FormatInt(messageID(t, body), 10)+"/original", session)

	if response.StatusCode != http.StatusOK || body != string(content) {
		t.Fatalf("download = %d %q, want 200 with the exact bytes", response.StatusCode, body)
	}
	if failures := h.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

// Contract: an unknown ID answers not_found, with no failure event, because
// nothing went wrong in the archive.
func TestDownloadUnknownMessage(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	for _, id := range []string{"1", "0", "-1", "abc", "99999999999999999999"} {
		t.Run(id, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/messages/"+id+"/original", session)

			assertFailedDownload(t, response, body, http.StatusNotFound, "not_found")
			if failures := h.status.Snapshot().Failures; len(failures) != 0 {
				t.Errorf("failures = %+v, want none", failures)
			}
		})
	}
}

// Contract: a catalog that can't be read answers 500 internal_error as JSON, with
// no failure event. The guard reads sessions from the same catalog, so this test
// opens the route to everyone to reach the handler.
func TestDownloadWhenTheCatalogCantBeRead(t *testing.T) {
	routes := Routes()
	for i := range routes {
		if routes[i].Path == "/api/messages/{id}/original" {
			routes[i].Access = Public
		}
	}
	h := newHarness(t, routes)
	if err := h.db.Close(); err != nil {
		t.Fatal(err)
	}

	response, body := h.do(t, http.MethodGet, "/api/messages/1/original", nil)

	assertJSONResponse(t, response, body, http.StatusInternalServerError, `{"error":"internal_error"}`)
	if failures := h.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failures = %+v, want none", failures)
	}
}

// assertFailedDownload checks the error answer the interface reads in place of
// a download. It must have no Content-Disposition, so nothing is saved.
func assertFailedDownload(t *testing.T, response *http.Response, body string, status int, code string) {
	t.Helper()
	assertJSONResponse(t, response, body, status, `{"error":"`+code+`"}`)
	if got := response.Header.Get("Content-Disposition"); got != "" {
		t.Errorf("Content-Disposition = %q, want none, so nothing is saved", got)
	}
}
