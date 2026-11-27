package search

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// MaxPage is the deepest page a search may ask for. The search process
// refuses offsets past it.
const MaxPage = 2000

// Query is one search.
type Query struct {
	Words string
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
	Offset     int    `json:"offset"`
	Limit      int    `json:"limit"`
}

type searchAnswer struct {
	Total   int     `json:"total"`
	IDs     []int64 `json:"ids"`
	NoTerms bool    `json:"no_terms"`
}

// Search finds the messages that match q. It searches the active index, or
// the one being built when none is active yet.
func (m *Module) Search(ctx context.Context, q Query) (Result, error) {
	if q.Page < 1 || q.Page > MaxPage {
		return Result{}, fmt.Errorf("search page %d: pages run from 1 to %d", q.Page, MaxPage)
	}
	s, err := m.openSession(ctx)
	if err != nil {
		return Result{}, err
	}
	generationID := s.search()
	var answer searchAnswer
	err = s.client.call(ctx, opstatus.OperationSearch, func(id int64) any {
		return searchRequest{
			ID: id, Op: "search", Generation: generationID, Words: q.Words,
			Offset: (q.Page - 1) * archive.PageSize, Limit: archive.PageSize,
		}
	}, &answer)
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
		return Result{Rows: page.Items, Total: page.Total, FullList: true}, nil
	}
	rows, err := m.config.Archive.Rows(ctx, answer.IDs)
	if err != nil {
		return Result{}, err
	}
	notIncluded, err := m.notIncluded(ctx, generationID)
	if err != nil {
		return Result{}, err
	}
	return Result{Rows: rows, Total: answer.Total, NotIncluded: notIncluded}, nil
}

// openSession returns the running session once an index is open for
// searches. A process that is starting gets as long as a search call.
func (m *Module) openSession(ctx context.Context) (*session, error) {
	m.mu.Lock()
	s := m.session
	unavailable := &UnavailableError{Cause: m.lastFailure}
	if m.nextStartAt != nil {
		next := *m.nextStartAt
		unavailable.NextStartAt = &next
	}
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
