package web

import (
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

func text(s string) *string { return &s }

// Contract: GET /api/processing-problems answers as GET /api/messages does,
// with only the rows processed with problems or that couldn't be read, in the
// list's order.
func TestListProcessingProblems(t *testing.T) {
	h := newHarness(t, Routes())
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")

	read := mail("Gelesen")
	broken := mail("Kaputt")
	partly := mail("Teilweise")
	waiting := mail("Wartet")
	readID := h.importMail(t, session, read, "gelesen.eml")
	brokenID := h.importMail(t, session, broken, "kaputt.eml")
	partlyID := h.importMail(t, session, partly, "teilweise.eml")
	h.importMail(t, session, waiting, "wartet.eml")

	h.recordProcessed(t, readID, archive.ProcessingResult{
		State: archive.Processed, ListDate: testNow.Add(time.Hour),
		Summary: &archive.Summary{SenderAddress: text("sender@example.test"), Subject: text("Gelesen")},
	})
	h.recordProcessed(t, brokenID, unreadable())
	h.recordProcessed(t, partlyID, archive.ProcessingResult{
		State:    archive.ProcessedWithProblems,
		ListDate: time.Date(2026, 11, 2, 8, 14, 55, 0, time.UTC),
		Summary: &archive.Summary{
			SenderName: text("Anna Schmidt"), SenderAddress: text("anna@example.com"), MoreSenders: 2,
			Subject: text("Teilweise"), DateHeader: text("Mon, 02 Nov 2026 09:14:55 +0100"), HasAttachments: true,
		},
		Problems: []archive.Problem{{Position: "2", DeclaredType: text("application/pdf"), Kind: "invalid_encoding"}},
	})

	// The unreadable message keeps its first archive time, which is newer
	// than the other's Date header.
	firstPage := `{"items":[` +
		summaryLessRow("unreadable", brokenID, broken, testTime, 1, importEntry(testTime, "admin@example.test", `"kaputt.eml"`)) + "," +
		fmt.Sprintf(`{"id":%d,"content_hash":%q,"size":%d,"processing":"processed_with_problems",`+
			`"list_date":"2026-11-02T08:14:55.000Z","list_date_is_archive_time":false,`+
			`"sender":{"name":"Anna Schmidt","address":"anna@example.com","more":2},"subject":"Teilweise",`+
			`"has_attachments":true,"observation_count":1,"latest":%s}`,
			partlyID, hashOf(partly), len(partly), importEntry(testTime, "admin@example.test", `"teilweise.eml"`)) +
		`],"page":1,"page_size":50,"total":2}`

	for _, tt := range []struct {
		name   string
		query  string
		status int
		want   string
	}{
		{"no page", "", http.StatusOK, firstPage},
		{"page 1", "?page=1", http.StatusOK, firstPage},
		{"page past the end", "?page=2", http.StatusOK, `{"items":[],"page":2,"page_size":50,"total":2}`},
		{"word", "?page=abc", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"page 0", "?page=0", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"two pages", "?page=1&page=2", http.StatusBadRequest, `{"error":"bad_page"}`},
		{"too large for a number", "?page=99999999999999999999", http.StatusBadRequest, `{"error":"bad_page"}`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			response, body := h.do(t, http.MethodGet, "/api/processing-problems"+tt.query, session)
			assertJSONResponse(t, response, body, tt.status, tt.want)
		})
	}

}
