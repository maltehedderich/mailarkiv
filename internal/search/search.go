package search

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// MaxPage is the deepest page a search may ask for. The search process
// refuses offsets past it.
const MaxPage = 2000

// searchDeadline is when the search process stops a search. The call's own
// limit is longer, so the process normally answers timed_out first.
const searchDeadline = 10 * time.Second

// Query is one search. Empty text, nil times, and a false HasAttachments are
// no filter. Every word and filter must match.
type Query struct {
	Words string
	// From matches senders, and To matches To, Cc, and Bcc: a complete
	// address, a domain, or words.
	From       string
	To         string
	Subject    string
	Attachment string
	// ListDateFrom keeps messages listed at or after it.
	ListDateFrom *time.Time
	// ListDateBefore keeps messages listed before it.
	ListDateBefore *time.Time
	HasAttachments bool
	// Page counts from 1, with archive.PageSize rows on each.
	Page int
}

// Result is one page of a search's rows, in list order.
type Result struct {
	Rows  []archive.Message
	Total int
	// NotIncluded counts the messages the search couldn't see: those not
	// processed yet, and those processed but not yet in the index.
	NotIncluded int
	// FullList is true when the words had no letters or digits, so the rows
	// are the whole list.
	FullList bool
	// MayBeIncomplete is true while the index is marked Needs rebuild.
	MayBeIncomplete bool
}

// UnavailableError means the search process isn't running.
type UnavailableError struct {
	// Cause is the category of the latest stop or failed start.
	Cause opstatus.Category
	// NextStartAt is when the next start is due, if one is waiting.
	NextStartAt *time.Time
}

func (e *UnavailableError) Error() string { return "search is unavailable: " + e.Cause.String() }

// ErrTookTooLong means the search process stopped the search at its
// deadline.
var ErrTookTooLong = errors.New("the search took too long")

type searchRequest struct {
	ID         int64  `json:"id"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
	Words      string `json:"words"`
	From       string `json:"from"`
	To         string `json:"to"`
	Subject    string `json:"subject"`
	Attachment string `json:"attachment"`
	// Unix milliseconds, or null.
	ListDateFrom   *int64 `json:"list_date_from"`
	ListDateBefore *int64 `json:"list_date_before"`
	HasAttachments bool   `json:"has_attachments"`
	Offset         int    `json:"offset"`
	Limit          int    `json:"limit"`
	DeadlineMS     int64  `json:"deadline_ms"`
}

// unixMilli is t in Unix milliseconds, or nil.
func unixMilli(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	ms := t.UnixMilli()
	return &ms
}

type searchAnswer struct {
	Total   int     `json:"total"`
	IDs     []int64 `json:"ids"`
	NoTerms bool    `json:"no_terms"`
}

// Search finds the messages that match q. It searches the active index, or
// the one being built when none is active or the active one can't be read.
func (m *Module) Search(ctx context.Context, q Query) (Result, error) {
	if q.Page < 1 || q.Page > MaxPage {
		return Result{}, fmt.Errorf("search page %d: pages run from 1 to %d", q.Page, MaxPage)
	}
	s, err := m.openSession(ctx)
	if err != nil {
		return Result{}, err
	}
	s.searching.RLock()
	written := sync.OnceFunc(s.searching.RUnlock)
	defer written()
	generationID := s.search()
	if generationID == 0 {
		return Result{}, &UnavailableError{Cause: opstatus.IndexNeedsRebuild}
	}
	incomplete := m.mayBeIncomplete()
	var answer searchAnswer
	err = s.client.callSent(ctx, opstatus.OperationSearch, func(id int64) any {
		return searchRequest{
			ID: id, Op: "search", Generation: generationID,
			Words: q.Words, From: q.From, To: q.To, Subject: q.Subject, Attachment: q.Attachment,
			ListDateFrom: unixMilli(q.ListDateFrom), ListDateBefore: unixMilli(q.ListDateBefore),
			HasAttachments: q.HasAttachments,
			Offset:         (q.Page - 1) * archive.PageSize, Limit: archive.PageSize,
			DeadlineMS: searchDeadline.Milliseconds(),
		}
	}, &answer, written)
	switch {
	case err == nil:
	case isAnswer(err, "timed_out"):
		return Result{}, ErrTookTooLong
	case errors.Is(err, errStopped):
		return Result{}, &UnavailableError{Cause: opstatus.SearchProcessStopped}
	default:
		return Result{}, fmt.Errorf("search: %w", err)
	}

	if answer.NoTerms {
		page, err := m.config.Archive.List(ctx, q.Page)
		if err != nil {
			return Result{}, err
		}
		return Result{Rows: page.Items, Total: page.Total, FullList: true, MayBeIncomplete: incomplete}, nil
	}
	rows, err := m.config.Archive.Rows(ctx, answer.IDs)
	if err != nil {
		return Result{}, err
	}
	notIncluded, err := m.notIncluded(ctx, generationID)
	if err != nil {
		return Result{}, err
	}
	return Result{Rows: rows, Total: answer.Total, NotIncluded: notIncluded, MayBeIncomplete: incomplete}, nil
}

// openSession returns the running session once an index is open for
// searches. A process that is starting gets as long as a search call.
func (m *Module) openSession(ctx context.Context) (*session, error) {
	m.mu.Lock()
	s := m.session
	unavailable := m.unavailableLocked()
	m.mu.Unlock()
	if s == nil {
		return nil, unavailable
	}
	limit := time.NewTimer(callLimit(opstatus.OperationSearch))
	defer limit.Stop()
	select {
	case <-s.opened:
		return s, nil
	case <-s.client.done:
		return nil, &UnavailableError{Cause: opstatus.SearchProcessStopped}
	case <-limit.C:
		return nil, ErrDidNotAnswer
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// notIncluded counts the messages not processed yet and those processed
// after generation id's cursor.
func (m *Module) notIncluded(ctx context.Context, id int64) (int, error) {
	notProcessed, err := m.config.Archive.CountNotProcessed(ctx)
	if err != nil {
		return 0, err
	}
	highest, err := m.config.Archive.HighestProcessedSeq(ctx)
	if err != nil {
		return 0, err
	}
	cursor, err := m.storedCursor(ctx, id)
	if err != nil {
		return 0, err
	}
	return notProcessed + int(max(highest-cursor, 0)), nil
}
