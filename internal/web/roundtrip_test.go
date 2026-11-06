package web

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"testing"
)

// SC-002's samples. The server must store and return each one byte for byte,
// whether or not it is well-formed mail (FR-011, FR-022).
var roundTripSamples = []struct {
	name    string
	content []byte
}{
	{"valid mail", []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: Valid\r\nDate: Fri, 06 Nov 2026 15:00:00 +0000\r\nMessage-ID: <valid@example.test>\r\nMIME-Version: 1.0\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello.\r\n")},
	{"broken MIME", []byte("From: sender@example.test\r\nSubject: Broken\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"never-closed\"\r\n\r\n--never-closed\r\nContent-Type: text/plain\r\nContent-Transfer-Encoding: base64\r\n\r\n!!not base64!!\r\n")},
	{"non-UTF-8 text", []byte("From: sender@example.test\r\nSubject: Caf\xe9\r\nContent-Type: text/plain; charset=iso-8859-1\r\n\r\nGr\xfc\xdfe aus K\xf6ln \xff\xfe\r\n")},
	{"no Message-ID", []byte("From: sender@example.test\r\nTo: admin@example.test\r\nSubject: No ID\r\n\r\nBody.\r\n")},
	{"mixed line endings", []byte("From: sender@example.test\nSubject: Mixed\r\nX-Odd: one\r\r\n\nline one\rline two\r\nline three\n")},
	{"NUL bytes", []byte("From: sender@example.test\r\nSubject: NUL\r\n\r\nbefore\x00after\x00\x00end\r\n")},
	{"no final line break", []byte("From: sender@example.test\r\nSubject: Unfinished\r\n\r\nlast line")},
}

type listedPage struct {
	Items []struct {
		ID          int64  `json:"id"`
		ContentHash string `json:"content_hash"`
	} `json:"items"`
	Total int `json:"total"`
}

type storedResponse struct {
	Outcome string `json:"outcome"`
	Message struct {
		ID          int64  `json:"id"`
		ContentHash string `json:"content_hash"`
		Size        int64  `json:"size"`
	} `json:"message"`
}

func TestImportedBytesRoundTrip(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	h.createAccount(t, "second@example.test")
	admin := h.signIn(t, "admin@example.test")
	second := h.signIn(t, "second@example.test")

	ids := map[string]int64{}
	for _, sample := range roundTripSamples {
		t.Run(sample.name, func(t *testing.T) {
			stored := importForRoundTrip(t, h, admin, bytes.NewReader(sample.content), int64(len(sample.content)))
			ids[sample.name] = stored.Message.ID
			assertDownload(t, h, admin, stored.Message.ID, hashOf(sample.content), int64(len(sample.content)))
		})
	}

	// The limit itself, from a reader, so the test never holds 150 MB.
	t.Run("exactly 150,000,000 bytes", func(t *testing.T) {
		input := sha256.New()
		body := io.TeeReader(newGenerated(maxImport), input)
		stored := importForRoundTrip(t, h, admin, body, maxImport)
		inputHash := hex.EncodeToString(input.Sum(nil))
		if stored.Message.ContentHash != inputHash {
			t.Fatalf("content_hash = %s, want %s", stored.Message.ContentHash, inputHash)
		}
		assertDownload(t, h, admin, stored.Message.ID, inputHash, maxImport)
	})

	// Scenario 7, FR-008: every account sees the same archive.
	t.Run("a second account lists and downloads the same message", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, "/api/messages", second)
		if response.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/messages = %d %s", response.StatusCode, body)
		}
		var page listedPage
		if err := json.Unmarshal([]byte(body), &page); err != nil {
			t.Fatal(err)
		}
		if page.Total != len(roundTripSamples)+1 {
			t.Fatalf("total = %d, want %d", page.Total, len(roundTripSamples)+1)
		}
		valid := roundTripSamples[0]
		listed := false
		for _, item := range page.Items {
			listed = listed || (item.ID == ids[valid.name] && item.ContentHash == hashOf(valid.content))
		}
		if !listed {
			t.Fatalf("the list doesn't hold %s: %s", valid.name, body)
		}
		assertDownload(t, h, second, ids[valid.name], hashOf(valid.content), int64(len(valid.content)))
	})
}

func importForRoundTrip(t *testing.T, h *harness, session func(*http.Request), body io.Reader, size int64) storedResponse {
	t.Helper()
	response, read := h.send(t, http.MethodPost, "/api/imports", body, both(session, withFileName("sample.eml"), func(request *http.Request) {
		request.ContentLength = size
	}))
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("import = %d %s, want 201", response.StatusCode, read)
	}
	var stored storedResponse
	if err := json.Unmarshal([]byte(read), &stored); err != nil {
		t.Fatal(err)
	}
	if stored.Outcome != "archived" || stored.Message.Size != size {
		t.Fatalf("import = %s, want archived with size %d", read, size)
	}
	return stored
}

// assertDownload hashes the download as it arrives, and checks FR-022's
// headers.
func assertDownload(t *testing.T, h *harness, session func(*http.Request), id int64, wantHash string, wantSize int64) {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, h.server.URL+"/api/messages/"+strconv.FormatInt(id, 10)+"/original", nil)
	if err != nil {
		t.Fatal(err)
	}
	session(request)
	response, err := testClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()

	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(response.Body)
		t.Fatalf("download = %d %s, want 200", response.StatusCode, body)
	}
	want := map[string]string{
		"Content-Type":        "application/octet-stream",
		"Content-Disposition": `attachment; filename="` + wantHash + `.eml"`,
		"Content-Length":      strconv.FormatInt(wantSize, 10),
		"MailArkiv-Response":  "1",
	}
	for name, value := range want {
		if got := response.Header.Get(name); got != value {
			t.Errorf("%s = %q, want %q", name, got, value)
		}
	}
	downloaded := sha256.New()
	n, err := io.Copy(downloaded, response.Body)
	if err != nil {
		t.Fatal(err)
	}
	if n != wantSize {
		t.Errorf("downloaded %d bytes, want %d", n, wantSize)
	}
	if got := hex.EncodeToString(downloaded.Sum(nil)); got != wantHash {
		t.Fatalf("download SHA-256 = %s, want %s", got, wantHash)
	}
}
