package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
)

const (
	maxBatchMessages = 500
	// maxBatchBytes keeps a batch's frame well under the 64 MiB frame limit.
	maxBatchBytes = 32 << 20
	// Without an empty queue, a commit still comes after this many
	// messages, or this long after the first uncommitted one.
	commitEvery = 10_000
	commitAfter = 20 * time.Second
)

// session is one running search process as searches see it.
type session struct {
	client *client
	// opened closes once an index is open for searches.
	opened chan struct{}

	mu       sync.Mutex
	searchID int64
}

func newSession(c *client) *session {
	return &session{client: c, opened: make(chan struct{})}
}

// setSearch sends searches to generation id.
func (s *session) setSearch(id int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	first := s.searchID == 0
	s.searchID = id
	if first {
		close(s.opened)
	}
}

func (s *session) search() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searchID
}

// backoff is a wait of 1 s that doubles up to 60 s.
type backoff struct{ last time.Duration }

func (b *backoff) next() time.Duration {
	b.last = min(max(2*b.last, time.Second), maxRestartWait)
	return b.last
}

func (b *backoff) reset() { b.last = 0 }

// index keeps the index in step with processed messages while the session
// runs. After a failure it waits, then starts again from the stored cursor:
// the search process discards what wasn't committed.
func (m *Module) index(ctx context.Context, s *session) {
	var wait backoff
	for {
		err := m.indexUntilFailure(ctx, s, &wait)
		switch {
		case ctx.Err() != nil, errors.Is(err, errStopped):
			return
		case errors.Is(err, ErrDidNotAnswer), isAnswer(err, "write_failed"):
			// Already recorded as a failure event.
		default:
			m.config.Logger.Error("indexing", "error", err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-m.retryAfter(wait.next()):
		}
	}
}

func (m *Module) indexUntilFailure(ctx context.Context, s *session, wait *backoff) error {
	target, err := m.openGenerations(ctx, s)
	if err != nil {
		return err
	}
	sent := target.cursor
	uncommitted := 0
	var firstUncommitted time.Time
	for {
		highest, err := m.config.Archive.HighestProcessedSeq(ctx)
		if err != nil {
			return err
		}
		if target.role == roleBuilding && target.cursor >= highest {
			if err := m.activate(ctx, target); err != nil {
				return err
			}
			s.setSearch(target.id)
		}
		m.setIndexing(target, highest)
		if sent >= highest {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-m.config.Processor.Processed():
			}
			continue
		}

		b, err := m.nextBatch(ctx, sent)
		if err != nil {
			return err
		}
		if highest, err = m.config.Archive.HighestProcessedSeq(ctx); err != nil {
			return err
		}
		b.commit = b.lastSeq >= highest || uncommitted+len(b.messages) >= commitEvery ||
			(uncommitted > 0 && time.Since(firstUncommitted) >= commitAfter)
		if err := m.send(ctx, s.client, target.id, b); err != nil {
			return err
		}
		sent = b.lastSeq
		if b.commit {
			if err := m.saveCursor(ctx, target, sent); err != nil {
				return err
			}
			uncommitted = 0
			wait.reset()
		} else {
			if uncommitted == 0 {
				firstUncommitted = time.Now()
			}
			uncommitted += len(b.messages)
		}
		if m.batchSent != nil {
			m.batchSent(sentBatch{generation: target.id, messages: b.messages, documents: len(b.documents), commit: b.commit})
		}
	}
}

// openGenerations opens every generation in the search process and returns
// the one that receives batches: the building one, or else the active one.
// An active index that is gone starts a new build.
func (m *Module) openGenerations(ctx context.Context, s *session) (*generation, error) {
	generations, err := m.loadGenerations(ctx)
	if err != nil {
		return nil, err
	}
	var target, search *generation
	for i := 0; i < len(generations); i++ {
		g := &generations[i]
		err := s.client.call(ctx, opstatus.OperationOpen, func(id int64) any {
			return openRequest{ID: id, Op: "open", Generation: g.id, Create: g.role == roleBuilding}
		}, nil)
		if g.role == roleActive && isAnswer(err, "index_missing") {
			created, err := m.replaceGenerations(ctx)
			if err != nil {
				return nil, err
			}
			generations = []generation{created}
			i = -1
			target, search = nil, nil
			continue
		}
		if err != nil {
			if answer, ok := errors.AsType[*answerError](err); ok && answer.code == "write_failed" {
				m.recordWriteFailed(g.id, nil, answer.cause)
			}
			return nil, fmt.Errorf("open index %d: %w", g.id, err)
		}
		if g.role == roleBuilding {
			target = g
		} else {
			search = g
		}
	}
	if target == nil {
		target = search
	}
	if search == nil {
		search = target
	}
	s.setSearch(search.id)
	return target, nil
}

type openRequest struct {
	ID         int64  `json:"id"`
	Op         string `json:"op"`
	Generation int64  `json:"generation"`
	Create     bool   `json:"create"`
}

type indexRequest struct {
	ID         int64             `json:"id"`
	Op         string            `json:"op"`
	Generation int64             `json:"generation"`
	Documents  []json.RawMessage `json:"documents"`
	Commit     bool              `json:"commit"`
}

// batch is the messages after one cursor that one index request carries.
// An unreadable message is in messages without a document.
type batch struct {
	messages  []int64
	documents []json.RawMessage
	lastSeq   int64
	commit    bool
}

// sentBatch is what tests see of a batch once its answer is handled.
type sentBatch struct {
	generation int64
	messages   []int64
	documents  int
	commit     bool
}

// nextBatch reads the messages processed after sent, up to the batch
// bounds. It always takes at least one message.
func (m *Module) nextBatch(ctx context.Context, sent int64) (*batch, error) {
	processed, err := m.config.Archive.ProcessedAfter(ctx, sent, m.batchMessages)
	if err != nil {
		return nil, err
	}
	b := &batch{documents: []json.RawMessage{}}
	size := 0
	for _, message := range processed {
		var encoded json.RawMessage
		if message.State != archive.Unreadable {
			doc, err := m.document(ctx, message)
			if err != nil {
				return nil, err
			}
			if encoded, err = json.Marshal(doc); err != nil {
				return nil, err
			}
		}
		if len(b.messages) > 0 && size+len(encoded) > m.batchBytes {
			break
		}
		b.messages = append(b.messages, message.ID)
		b.lastSeq = message.Seq
		if encoded != nil {
			b.documents = append(b.documents, encoded)
			size += len(encoded)
		}
	}
	return b, nil
}

// send sends one batch and waits for its answer. A write_failed answer is
// recorded with the batch's first and last message.
func (m *Module) send(ctx context.Context, c *client, generationID int64, b *batch) error {
	err := c.call(ctx, opstatus.OperationIndex, func(id int64) any {
		return indexRequest{ID: id, Op: "index", Generation: generationID, Documents: b.documents, Commit: b.commit}
	}, nil)
	if answer, ok := errors.AsType[*answerError](err); ok && answer.code == "write_failed" {
		m.recordWriteFailed(generationID, b.messages, answer.cause)
	}
	return err
}

func (m *Module) recordWriteFailed(generationID int64, messages []int64, cause string) {
	detail := &opstatus.Detail{GenerationID: &generationID, Cause: writeCause(cause)}
	if len(messages) > 0 {
		first, last := messages[0], messages[len(messages)-1]
		detail.FirstMessageID, detail.LastMessageID = &first, &last
	}
	m.config.Status.RecordFailure(opstatus.FailureEvent{Category: opstatus.IndexWriteFailed, Detail: detail})
}

func writeCause(cause string) opstatus.Cause {
	switch cause {
	case "no_space":
		return opstatus.CauseNoSpace
	case "permission_denied":
		return opstatus.CausePermissionDenied
	}
	return opstatus.CauseOther
}

// document is one message's index document. Empty lists are left out: the
// search process reads a missing list as empty and refuses null.
type document struct {
	ID              int64     `json:"id"`
	ListDate        int64     `json:"list_date"`
	FirstArchivedAt int64     `json:"first_archived_at"`
	HasAttachments  bool      `json:"has_attachments"`
	Subject         *string   `json:"subject,omitempty"`
	From            []address `json:"from,omitempty"`
	To              []address `json:"to,omitempty"`
	AttachmentNames []string  `json:"attachment_names,omitempty"`
	Text            []string  `json:"text,omitempty"`
}

type address struct {
	Name    string `json:"name,omitempty"`
	Address string `json:"address"`
}

// document reads message's original in a worker. When the original is
// missing or damaged, or no worker finishes it, the document holds only
// what the summary has.
func (m *Module) document(ctx context.Context, message archive.ProcessedMessage) (document, error) {
	d := document{
		ID:              message.ID,
		ListDate:        message.ListDate.UnixMilli(),
		FirstArchivedAt: message.FirstArchivedAt.UnixMilli(),
	}
	summary := message.Summary
	if summary != nil {
		d.HasAttachments = summary.HasAttachments
	}
	content, err := m.config.Processor.Read(ctx, message.ID, true)
	_, workerStopped := errors.AsType[*processing.WorkerStoppedError](err)
	switch {
	case err == nil:
		d.Subject = content.Subject
		d.From = addressesOf(content.From)
		d.To = addressesOf(content.Recipients)
		d.AttachmentNames = content.AttachmentNames
		d.Text = content.Text
		return d, nil
	case workerStopped, errors.Is(err, archive.ErrOriginalMissing), errors.Is(err, archive.ErrOriginalDamaged):
		// Processing logged each worker that stopped, and the archive
		// recorded the original's failure.
	default:
		return document{}, err
	}
	if summary != nil {
		d.Subject = summary.Subject
		if summary.SenderName != nil || summary.SenderAddress != nil {
			d.From = []address{{Name: deref(summary.SenderName), Address: deref(summary.SenderAddress)}}
		}
	}
	return d, nil
}

func addressesOf(list []processing.Address) []address {
	var out []address
	for _, a := range list {
		out = append(out, address{Name: a.Name, Address: a.Address})
	}
	return out
}

func deref(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
