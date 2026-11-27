package processing

import (
	"context"
	"errors"
	"log/slog"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/fault"
	"github.com/maltehedderich/mailarkiv/internal/mailparse"
)

// batchSize is how many messages one pass takes from the catalog.
const batchSize = 100

// retryWait is how long processing waits after the catalog failed.
const retryWait = time.Second

// Processor reads each message once, newest capture first, and records what
// it found.
type Processor struct {
	archive   *archive.Archive
	pool      *Pool
	logger    *slog.Logger
	processed chan struct{}
	// skipped holds messages whose original was missing or damaged, so each
	// records its failure event once. A restored original clears it, and so
	// does a restart. Only Run's goroutine touches it.
	skipped map[int64]bool
}

// New returns a processor that reads originals through background.
func New(a *archive.Archive, background *Pool, logger *slog.Logger) *Processor {
	return &Processor{
		archive:   a,
		pool:      background,
		logger:    logger,
		processed: make(chan struct{}, 1),
		skipped:   make(map[int64]bool),
	}
}

// Processed fires after a result is recorded, so the indexer can wake.
func (p *Processor) Processed() <-chan struct{} { return p.processed }

// Run processes until ctx ends. It wakes when a capture adds a message or
// restores an original.
func (p *Processor) Run(ctx context.Context) {
	for {
		select {
		case <-p.archive.RestoreSignal():
			clear(p.skipped)
		default:
		}
		taken, err := p.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			p.logger.Error("processing", "error", err.Error())
			select {
			case <-ctx.Done():
				return
			case <-time.After(retryWait):
			}
			continue
		}
		if taken > 0 {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-p.archive.CaptureSignal():
		case <-p.archive.RestoreSignal():
			clear(p.skipped)
		}
	}
}

// runOnce processes one batch and returns how many messages it took.
func (p *Processor) runOnce(ctx context.Context) (int, error) {
	ids, err := p.archive.NotProcessed(ctx, batchSize, p.skipped)
	if err != nil {
		return 0, err
	}
	for _, id := range ids {
		if err := p.process(ctx, id); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (p *Processor) process(ctx context.Context, id int64) error {
	answer, firstArchivedAt, err := p.readOriginal(ctx, id, false)
	if category, ok := skipCategory(err); ok {
		p.skipped[id] = true
		p.logger.Warn("processing skipped", "message_id", id, "category", category)
		return nil
	}
	var result archive.ProcessingResult
	if stopped, ok := errors.AsType[*WorkerStoppedError](err); ok {
		result = stoppedResult(stopped, firstArchivedAt)
	} else if err != nil {
		return err
	} else {
		result = resultOf(answer.Message, firstArchivedAt)
	}
	if _, err := p.archive.RecordProcessed(ctx, id, result); err != nil {
		return err
	}
	_ = fault.At("after-processing-commit")
	select {
	case p.processed <- struct{}{}:
	default:
	}
	return nil
}

// skipCategory names the failure event OpenVerified recorded for an
// original processing can't read.
func skipCategory(err error) (string, bool) {
	switch {
	case errors.Is(err, archive.ErrOriginalMissing):
		return "stored_original_missing", true
	case errors.Is(err, archive.ErrOriginalDamaged):
		return "stored_original_damaged", true
	}
	return "", false
}

// stoppedResult is the result for a message no worker could finish: it took
// too long, or broke two workers in a row. The closed list of kinds has no
// better fit for the second than the message's structure.
func stoppedResult(stopped *WorkerStoppedError, firstArchivedAt time.Time) archive.ProcessingResult {
	kind := mailparse.MalformedStructure
	if stopped.TookTooLong {
		kind = mailparse.TookTooLong
	}
	return archive.ProcessingResult{
		State:    archive.Unreadable,
		ListDate: firstArchivedAt,
		Problems: []archive.Problem{{Position: "", Kind: string(kind)}},
	}
}

func resultOf(m mailparse.Message, firstArchivedAt time.Time) archive.ProcessingResult {
	var problems []archive.Problem
	for _, problem := range m.Problems {
		problems = append(problems, archive.Problem{
			Position: problem.Position, DeclaredType: nonEmpty(problem.DeclaredType), Kind: string(problem.Kind),
		})
	}
	if m.Unreadable {
		return archive.ProcessingResult{State: archive.Unreadable, ListDate: firstArchivedAt, Problems: problems}
	}
	listDate, reason := mailparse.ListDate(m.Date, firstArchivedAt)
	s := m.Summary()
	state := archive.Processed
	if len(problems) > 0 {
		state = archive.ProcessedWithProblems
	}
	return archive.ProcessingResult{
		State:    state,
		ListDate: listDate,
		Summary: &archive.Summary{
			SenderName:     s.SenderName,
			SenderAddress:  s.SenderAddress,
			MoreSenders:    s.MoreSenders,
			Subject:        s.Subject,
			DateHeader:     s.DateHeader,
			DateReason:     nonEmpty(string(reason)),
			HasAttachments: s.HasAttachments,
		},
		Problems: problems,
	}
}

func nonEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
