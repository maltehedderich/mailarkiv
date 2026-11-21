package web

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

// longName is a 600-character attachment name.
var longName = strings.Repeat("a", 596) + ".pdf"

// reportContent is the PDF attachment's decoded bytes, 46 of them.
const reportContent = "%PDF-1.4\n% synthetic contract for tests\n%%EOF\n"

// richMail has every part kind: the text and HTML bodies inside a related
// part with an embedded logo, a PDF, and an attached message.
func richMail() []byte {
	return []byte(strings.Join([]string{
		"From: Anna Schmidt <anna@example.com>",
		"To: team@example.com, Bob <bob@example.com>",
		"Cc: Carla Braun <carla@example.com>",
		"Subject: =?utf-8?q?Quartalsbericht_Q3_f=C3=BCr_alle?=",
		"Date: Mon, 2 Nov 2026 09:14:55 +0100",
		"MIME-Version: 1.0",
		`Content-Type: multipart/mixed; boundary="mix"`,
		"",
		"--mix",
		`Content-Type: multipart/related; boundary="rel"`,
		"",
		"--rel",
		`Content-Type: multipart/alternative; boundary="alt"`,
		"",
		"--alt",
		"Content-Type: text/plain; charset=utf-8",
		"",
		"Der Bericht.",
		"--alt",
		"Content-Type: text/html; charset=utf-8",
		"",
		`<p>Der Bericht.</p><img src="cid:logo@example.com"><img src="https://images.example.org/t.png">`,
		"--alt--",
		"--rel",
		"Content-Type: image/png",
		"Content-ID: <logo@example.com>",
		"Content-Transfer-Encoding: base64",
		"",
		// "\x89PNG\r\n\x1a\n" and "synthetic logo": 22 bytes.
		"iVBORw0KGgpzeW50aGV0aWMgbG9nbw==",
		"--rel--",
		"--mix",
		"Content-Type: application/pdf",
		`Content-Disposition: attachment; filename="` + longName + `"`,
		"Content-Transfer-Encoding: base64",
		"",
		base64.StdEncoding.EncodeToString([]byte(reportContent)),
		"--mix",
		"Content-Type: message/rfc822",
		"",
		// 21 + 16 + 2 + 11 = 50 bytes.
		"From: x@example.com",
		"Subject: Inner",
		"",
		"Inner body.",
		"--mix--",
		"",
	}, "\r\n"))
}

// brokenMail has a From that isn't an address list and an unknown
// character set.
func brokenMail() []byte {
	return []byte("From: this is not an address\r\nSubject: Kaputt\r\nDate: sometime\r\nMIME-Version: 1.0\r\n" +
		"Content-Type: text/plain; charset=x-unknown\r\n\r\nText\r\n")
}

func processedResult(state archive.ProcessingState, listDate time.Time, summary *archive.Summary, problems ...archive.Problem) archive.ProcessingResult {
	return archive.ProcessingResult{State: state, ListDate: listDate, Summary: summary, Problems: problems}
}

func TestContent(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{})
	importedAt := formatTime(testNow)

	waiting := mail("Wartet")
	waitingID := h.importMail(t, session, waiting, "waiting.eml")

	unread := mail("Unlesbar")
	unreadID := h.importMail(t, session, unread, "unread.eml")
	h.recordProcessed(t, unreadID, unreadable())

	rich := richMail()
	richID := h.importMail(t, session, rich, "rich.eml")
	headerDate := time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC)
	h.recordProcessed(t, richID, processedResult(archive.Processed, headerDate, &archive.Summary{
		SenderName: text("Anna Schmidt"), SenderAddress: text("anna@example.com"),
		Subject: text("Quartalsbericht Q3 für alle"), DateHeader: text("Mon, 2 Nov 2026 09:14:55 +0100"),
		HasAttachments: true,
	}))

	broken := brokenMail()
	brokenID := h.importMail(t, session, broken, "broken.eml")
	// Recorded with the whole message first, to show the answer orders them.
	h.recordProcessed(t, brokenID, processedResult(archive.ProcessedWithProblems, testNow,
		&archive.Summary{Subject: text("Kaputt"), DateHeader: text("sometime"), DateReason: text("unreadable")},
		archive.Problem{Position: "", Kind: "malformed_header"},
		archive.Problem{Position: "1", DeclaredType: text("text/plain"), Kind: "unknown_charset"},
	))

	notRead := func(id int64, content []byte, state, problems string) string {
		return fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,"processing":%q,"problems":%s,`+
			`"list_date":%q,"date_reason":null,"date_header":null,"subject":null,"from":null,"to":null,`+
			`"cc":null,"bcc":null,"encrypted":null,"html":null,"text":null,"attachments":[]}`,
			id, hashOf(content), len(content), state, problems, importedAt)
	}
	tests := []struct {
		name string
		id   int64
		want string
	}{
		{"not processed", waitingID, notRead(waitingID, waiting, "not_processed", "[]")},
		{"unreadable", unreadID, notRead(unreadID, unread, "unreadable",
			`[{"position":null,"declared_type":null,"kind":"took_too_long"}]`)},
		{"processed", richID, fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,"processing":"processed","problems":[],`+
			`"list_date":"2026-11-02T08:14:55.000Z","date_reason":null,"date_header":"Mon, 2 Nov 2026 09:14:55 +0100",`+
			`"subject":"Quartalsbericht Q3 für alle",`+
			`"from":[{"name":"Anna Schmidt","address":"anna@example.com"}],`+
			`"to":[{"name":null,"address":"team@example.com"},{"name":"Bob","address":"bob@example.com"}],`+
			`"cc":[{"name":"Carla Braun","address":"carla@example.com"}],"bcc":[],"encrypted":false,`+
			`"html":{"truncated":false,"remote_images":true,"embedded_images_left_out":false},`+
			`"text":{"truncated":false},"attachments":[`+
			`{"position":"1.2","name":null,"declared_type":"image/png","size":22,"damaged":false,"kind":"embedded_image"},`+
			`{"position":"2","name":%q,"declared_type":"application/pdf","size":46,"damaged":false,"kind":"file"},`+
			`{"position":"3","name":null,"declared_type":"message/rfc822","size":50,"damaged":false,"kind":"message"}]}`,
			richID, hashOf(rich), len(rich), longName)},
		{"processed with problems", brokenID, fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,`+
			`"processing":"processed_with_problems","problems":[`+
			`{"position":"1","declared_type":"text/plain","kind":"unknown_charset"},`+
			`{"position":null,"declared_type":null,"kind":"malformed_header"}],`+
			`"list_date":%q,"date_reason":"unreadable","date_header":"sometime","subject":"Kaputt",`+
			`"from":[{"name":"this is not an address","address":null}],"to":[],"cc":[],"bcc":[],"encrypted":false,`+
			`"html":null,"text":{"truncated":false},"attachments":[]}`,
			brokenID, hashOf(broken), len(broken), importedAt)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/content", tt.id), session)
			assertJSONResponse(t, response, body, http.StatusOK, tt.want)
		})
	}

	for _, unknown := range []string{"999", "abc"} {
		t.Run("unknown message "+unknown, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/messages/"+unknown+"/content", session)
			assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
		})
	}
}

func TestContentWithoutTheOriginal(t *testing.T) {
	tests := []struct {
		name     string
		change   func(t *testing.T, h *harness, hash string)
		status   int
		category string
	}{
		{"deleted", func(t *testing.T, h *harness, hash string) {
			if err := os.Remove(h.blobFile(hash)); err != nil {
				t.Fatal(err)
			}
		}, http.StatusNotFound, "stored_original_missing"},
		{"changed", func(t *testing.T, h *harness, hash string) {
			body, err := os.ReadFile(h.blobFile(hash))
			if err != nil {
				t.Fatal(err)
			}
			body[len(body)-1] ^= 0xff
			h.writeBlob(t, hash, body)
		}, http.StatusInternalServerError, "stored_original_damaged"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h, session := newViewHarness(t, harnessOptions{})
			content := mail("Original " + tt.name)
			id := h.importMail(t, session, content, "original.eml")
			h.recordProcessed(t, id, processedResult(archive.Processed, testNow, &archive.Summary{Subject: text("Original")}))
			tt.change(t, h, hashOf(content))

			response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/content", id), session)
			assertJSONResponse(t, response, body, tt.status, `{"error":"`+tt.category+`"}`)
			failures := h.status.Snapshot().Failures
			if len(failures) != 1 || failures[0].Category.String() != tt.category ||
				failures[0].MessageID == nil || *failures[0].MessageID != id {
				t.Errorf("failures %+v, want one %s for message %d", failures, tt.category, id)
			}
		})
	}
}

// Both interactive workers hang. Requests that hold them end at the limit,
// and one that finds neither free ends after the wait.
func TestContentWhenWorkersAreBusy(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{views: "hang", viewLimit: 2 * time.Second, viewWait: 300 * time.Millisecond})
	content := mail("Haengt")
	id := h.importMail(t, session, content, "hang.eml")
	h.recordProcessed(t, id, processedResult(archive.Processed, testNow, &archive.Summary{Subject: text("Haengt")}))
	path := fmt.Sprintf("/api/messages/%d/content", id)

	type answer struct {
		response *http.Response
		body     string
	}
	held := make([]answer, 2)
	var wg sync.WaitGroup
	for i := range held {
		wg.Go(func() {
			request, err := http.NewRequest(http.MethodGet, h.server.URL+path, nil)
			if err != nil {
				t.Error(err)
				return
			}
			session(request)
			response, err := testClient.Do(request)
			if err != nil {
				t.Error(err)
				return
			}
			defer response.Body.Close()
			var body bytes.Buffer
			_, _ = body.ReadFrom(response.Body)
			held[i] = answer{response, body.String()}
		})
	}
	waitFor(t, "both workers holding a request", func() bool {
		entries, err := os.ReadDir(h.markers)
		return err == nil && len(entries) == 2
	})

	response, body := h.do(t, http.MethodGet, path, session)
	assertJSONResponse(t, response, body, http.StatusServiceUnavailable, `{"error":"busy"}`)

	wg.Wait()
	for _, a := range held {
		if a.response == nil {
			t.Fatal("a held request got no answer")
		}
		assertJSONResponse(t, a.response, a.body, http.StatusInternalServerError, `{"error":"could_not_read"}`)
	}
	if got := strings.Count(h.log.String(), `"msg":"processing_worker_stopped"`); got != 2 {
		t.Errorf("%d processing_worker_stopped events, want 2: %s", got, h.log.String())
	}
}

func TestTextBody(t *testing.T) {
	h, session := newViewHarness(t, harnessOptions{})
	short := h.importMail(t, session, withAttachment(`filename="a.pdf"`, []byte("x")), "short.eml")
	long := strings.Repeat("ä", 1_000_000) + "b"
	longID := h.importMail(t, session, []byte("From: anna@example.test\r\nMIME-Version: 1.0\r\n"+
		"Content-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n--mix\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n"+
		long+"\r\n--mix--\r\n"), "long.eml")
	htmlOnly := h.importMail(t, session, htmlMail("<p>Nur HTML</p>"), "html.eml")

	t.Run("a body", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/text", short), session)
		if response.StatusCode != http.StatusOK || body != "Anbei." {
			t.Fatalf("status = %d %q, want 200 Anbei.", response.StatusCode, body)
		}
		assertOne(t, response, "Content-Type", "text/plain; charset=utf-8")
		assertOne(t, response, "Content-Security-Policy", downloadPolicy)
		assertOne(t, response, "X-Content-Type-Options", "nosniff")
	})

	t.Run("cut at 2,000,000 bytes", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/text", longID), session)
		if response.StatusCode != http.StatusOK || body != strings.Repeat("ä", 1_000_000) {
			t.Fatalf("status = %d with %d bytes, want 200 with the 2,000,000 bytes of ä", response.StatusCode, len(body))
		}
	})

	t.Run("no text body", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, fmt.Sprintf("/api/messages/%d/text", htmlOnly), session)
		assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"no_body"}`)
	})

	t.Run("unknown message", func(t *testing.T) {
		response, body := h.do(t, http.MethodGet, "/api/messages/999/text", session)
		assertJSONResponse(t, response, body, http.StatusNotFound, `{"error":"not_found"}`)
	})
}
