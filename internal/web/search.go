package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"
	"unicode/utf8"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/search"
)

const (
	maxSearchBody = 64 << 10
	// maxSearchText is the longest words or filter value, in characters.
	maxSearchText = 1000
)

// searchRequest holds each field undecoded, so a field of the wrong type
// breaks only its own rule. Every field is required.
type searchRequest struct {
	Words          json.RawMessage `json:"words"`
	From           json.RawMessage `json:"from"`
	To             json.RawMessage `json:"to"`
	Subject        json.RawMessage `json:"subject"`
	Attachment     json.RawMessage `json:"attachment"`
	ListDateFrom   json.RawMessage `json:"list_date_from"`
	ListDateBefore json.RawMessage `json:"list_date_before"`
	HasAttachments json.RawMessage `json:"has_attachments"`
	Page           json.RawMessage `json:"page"`
}

// parse checks every field and returns the search, or the names of the
// fields that break their rules, in body order.
func (request searchRequest) parse() (search.Query, []string) {
	var query search.Query
	var invalid []string
	text := func(name string, raw json.RawMessage, into *string) {
		value, ok := searchText(raw)
		if !ok {
			invalid = append(invalid, name)
		}
		*into = value
	}
	instant := func(name string, raw json.RawMessage, into **time.Time) {
		value, ok := instantOrNull(raw)
		if !ok {
			invalid = append(invalid, name)
		}
		*into = value
	}
	text("words", request.Words, &query.Words)
	text("from", request.From, &query.From)
	text("to", request.To, &query.To)
	text("subject", request.Subject, &query.Subject)
	text("attachment", request.Attachment, &query.Attachment)
	instant("list_date_from", request.ListDateFrom, &query.ListDateFrom)
	instant("list_date_before", request.ListDateBefore, &query.ListDateBefore)
	hasAttachments, ok := strictDecode[bool](request.HasAttachments)
	if !ok {
		invalid = append(invalid, "has_attachments")
	}
	query.HasAttachments = hasAttachments
	page, ok := strictDecode[int](request.Page)
	if !ok || page < 1 {
		invalid = append(invalid, "page")
	}
	query.Page = page
	return query, invalid
}

// searchMessages answers POST /api/search. The words and filters travel only
// in the body, and neither they nor the body are logged.
func searchMessages(s *server, w http.ResponseWriter, r *http.Request) {
	request, ok := decodeBody[searchRequest](w, r, maxSearchBody)
	if !ok {
		return
	}
	query, invalid := request.parse()
	if len(invalid) > 0 {
		writeInvalidFields(w, invalid)
		return
	}
	if query.Page > search.MaxPage {
		writeError(w, http.StatusBadRequest, "page_too_deep")
		return
	}

	result, err := s.search.Search(r.Context(), query)
	if unavailable, ok := errors.AsType[*search.UnavailableError](err); ok {
		var next *string
		if unavailable.NextStartAt != nil {
			formatted := formatTime(*unavailable.NextStartAt)
			next = &formatted
		}
		writeJSON(w, http.StatusServiceUnavailable, struct {
			Error       string  `json:"error"`
			Cause       string  `json:"cause"`
			NextStartAt *string `json:"next_start_at"`
		}{"search_unavailable", unavailable.Cause.String(), next})
		return
	}
	switch {
	case errors.Is(err, search.ErrTookTooLong):
		writeError(w, http.StatusUnprocessableEntity, "search_took_too_long")
		return
	case errors.Is(err, search.ErrDidNotAnswer):
		writeError(w, http.StatusServiceUnavailable, "search_did_not_answer")
		return
	case err != nil:
		s.writeInternalError(w, r, err)
		return
	}
	items, err := s.encodeMessages(r.Context(), result.Rows)
	if err != nil {
		s.writeInternalError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, struct {
		Items           []messageJSON `json:"items"`
		Page            int           `json:"page"`
		PageSize        int           `json:"page_size"`
		Total           int           `json:"total"`
		NotIncluded     int           `json:"not_included"`
		FullList        bool          `json:"full_list"`
		MayBeIncomplete bool          `json:"may_be_incomplete"`
	}{items, query.Page, archive.PageSize, result.Total, result.NotIncluded, result.FullList, false})
}

// strictDecode decodes a field that must be present and of type T. The JSON
// decoder would replace bytes that aren't UTF-8, so they are refused first.
func strictDecode[T any](raw json.RawMessage) (T, bool) {
	var value T
	if raw == nil || !utf8.Valid(raw) || string(raw) == "null" || json.Unmarshal(raw, &value) != nil {
		var zero T
		return zero, false
	}
	return value, true
}

// searchText decodes words or a filter: a string of at most 1,000
// characters, where empty means no filter.
func searchText(raw json.RawMessage) (string, bool) {
	value, ok := strictDecode[string](raw)
	return value, ok && utf8.RuneCountInString(value) <= maxSearchText
}

// instantOrNull decodes null, or an RFC 3339 time with a zone.
func instantOrNull(raw json.RawMessage) (*time.Time, bool) {
	if string(raw) == "null" {
		return nil, true
	}
	value, ok := strictDecode[string](raw)
	if !ok {
		return nil, false
	}
	instant, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil, false
	}
	return &instant, true
}
