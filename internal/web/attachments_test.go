package web

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"testing"
)

// withAttachment is a message with a text body as part 1 and, as part 2, an
// attachment with the encoded file name parameter and content.
func withAttachment(filenameParameter string, content []byte) []byte {
	encoded := base64.StdEncoding.EncodeToString(content)
	var b strings.Builder
	b.WriteString("From: anna@example.test\r\nTo: admin@example.test\r\nSubject: Anhang\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n")
	b.WriteString("--mix\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nAnbei.\r\n")
	b.WriteString("--mix\r\nContent-Type: application/pdf\r\nContent-Disposition: attachment; " + filenameParameter + "\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n--mix--\r\n")
	return []byte(b.String())
}

func TestAttachmentHeaders(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{})
	content := []byte("%PDF-1.4\n% synthetic test report\n%%EOF\n")
	id := h.importMail(t, session, withAttachment("filename*=utf-8''Pr%C3%BCfbericht.pdf", content), "report.eml")
	path := fmt.Sprintf("/api/messages/%d/attachments/2", id)

	t.Run("success", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, session)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d %s, want 200", response.StatusCode, body)
		}
		disposition := mime.FormatMediaType("attachment", map[string]string{"filename": "Prüfbericht.pdf"})
		if !strings.Contains(disposition, "filename*=") {
			t.Fatalf("the expected disposition %q has no filename*", disposition)
		}
		assertOne(t, response, "Content-Type", "application/octet-stream")
		assertOne(t, response, "Content-Disposition", disposition)
		assertOne(t, response, "Content-Security-Policy", downloadPolicy)
		assertOne(t, response, "Content-Length", strconv.Itoa(len(content)))
		assertOne(t, response, "X-Content-Type-Options", "nosniff")
		if body != string(content) {
			t.Errorf("body %q, want %q", body, content)
		}
	})

	for _, position := range []string{"1", "3", "2.1", "0", "abc", "2%2F..%2F1"} {
		t.Run("no attachment at "+position, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/attachments/%s", id, position), session)
			assertFailedDownload(t, response, body, http.StatusNotFound, "not_found")
		})
	}

	t.Run("unknown message", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, "/api/messages/999/attachments/2", session)
		assertFailedDownload(t, response, body, http.StatusNotFound, "not_found")
	})

	t.Run("no session, opened as a page", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, both(
			withHeader("Sec-Fetch-Mode", "navigate"), withHeader("Referer", h.server.URL+"/messages/1"),
		))
		if response.StatusCode != http.StatusSeeOther || response.Header.Get("Location") != "/sign-in?ended=1&next=%2Fmessages%2F1" {
			t.Fatalf("status = %d to %q, want 303 to sign-in", response.StatusCode, response.Header.Get("Location"))
		}
		if response.Header.Get("Content-Disposition") != "" || strings.Contains(body, "PDF") {
			t.Errorf("the redirect carries the attachment: %q", body)
		}
	})

	t.Run("no session, fetched", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, path, withHeader("Sec-Fetch-Mode", "cors"))
		assertJSONResponse(t, response, body, http.StatusUnauthorized, noSession)
	})
}

// A worker that stops after the headers leaves the transfer broken, so the
// client can't mistake the bytes for the whole file.
func TestAttachmentWorkerDiesAfterTheHeaders(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{views: "cut"})
	content := bytes.Repeat([]byte("0123456789"), 100_000)
	id := h.importMail(t, session, withAttachment(`filename="gross.bin"`, content), "big.eml")

	request, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/messages/%d/attachments/2", h.server.URL, id), nil)
	if err != nil {
		t.Fatal(err)
	}
	session(request)
	response, err := testClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK || response.Header.Get("Content-Length") != strconv.Itoa(len(content)) {
		t.Fatalf("status = %d with length %q, want 200 and %d", response.StatusCode, response.Header.Get("Content-Length"), len(content))
	}
	got, err := io.ReadAll(response.Body)
	if !errors.Is(err, io.ErrUnexpectedEOF) {
		t.Errorf("reading the body gave %v after %d bytes, want an unexpected end", err, len(got))
	}
	if len(got) >= len(content) {
		t.Errorf("%d bytes arrived, want fewer than %d", len(got), len(content))
	}
}
