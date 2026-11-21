package search

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/fault"
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

// session is one running search process as searches and indexing see it.
type session struct {
	client *client
	// opened closes once searches have a generation, or know there is none.
	opened     chan struct{}
	openedOnce sync.Once
	// searching is held for reading from a search's choice of generation
	// until its request is written, and for writing while searches move to
	// another generation. So no search reaches an index after the request
	// that closes it.
	searching sync.RWMutex

	mu       sync.Mutex
	searchID int64
	// maintaining holds a channel for each generation with a maintenance
	// run, closed when the run ends.
	maintaining map[int64]chan struct{}
	runs        sync.WaitGroup
}

func newSession(c *client) *session {
	return &session{client: c, opened: make(chan struct{}), maintaining: make(map[int64]chan struct{})}
}

// setSearch sends searches to generation id, or answers them Unavailable
// when id is 0.
func (s *session) setSearch(id int64) {
	s.searching.Lock()
	defer s.searching.Unlock()
	s.mu.Lock()
	s.searchID = id
	s.mu.Unlock()
	s.openedOnce.Do(func() { close(s.opened) })
}

func (s *session) search() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.searchID
}

// startMaintenance registers a run for generation id, unless one runs. done
// ends it.
func (s *session) startMaintenance(id int64) (done func(), started bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, running := s.maintaining[id]; running {
		return nil, false
	}
	ended := make(chan struct{})
	s.maintaining[id] = ended
	s.runs.Add(1)
	return func() {
		s.mu.Lock()
		delete(s.maintaining, id)
		s.mu.Unlock()
		close(ended)
		s.runs.Done()
	}, true
}

// waitForMaintenance waits until no maintenance run for generation id runs.
func (s *session) waitForMaintenance(ctx context.Context, id int64) {
	s.mu.Lock()
	ended, running := s.maintaining[id]
	s.mu.Unlock()
	if !running {
		return
	}
	select {
	case <-ended:
	case <-ctx.Done():
	}
}

// backoff is a wait of 1 s that doubles up to 60 s.
type backoff struct{ last time.Duration }

func (b *backoff) next() time.Duration {
	b.last = min(max(2*b.last, time.Second), maxRestartWait)
	return b.last
}

func (b *backoff) reset() { b.last = 0 }

// feed is one open generation that receives batches.
type feed struct {
	gen *generation
	// sent is the processed_seq of the last message sent, committed or not.
	sent             int64
	uncommitted      int
	firstUncommitted time.Time
	// Commits since the last maintenance run, and that run's time.
	commits        int
	lastMaintained time.Time
}

func (m *Module) newFeed(g *generation) *feed {
	return &feed{gen: g, sent: g.cursor, lastMaintained: m.config.Now()}
}

// index keeps the indexes in step with processed messages while the session
// runs. After a failure it waits, then starts again from the stored
// cursors: the search process discards what wasn't committed.
func (m *Module) index(ctx context.Context, s *session) {
	defer s.runs.Wait()
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
	feeds, err := m.openGenerations(ctx, s)
	if err != nil {
		return err
	}
	tick := time.NewTicker(m.diskCheckEvery)
	defer tick.Stop()
	// waitFor waits for a reason to look again. A tick reads free space and
	// starts maintenance that is due by time.
	waitFor := func(processed <-chan struct{}) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-processed:
		case <-m.wake:
		case <-tick.C:
			m.checkDisk()
			for _, f := range feeds {
				m.maybeMaintain(ctx, s, f)
			}
		}
		return nil
	}
	turn := 0
	for {
		if feeds, err = m.openRebuild(ctx, s, feeds); err != nil {
			return err
		}
		highest, err := m.config.Archive.HighestProcessedSeq(ctx)
		if err != nil {
			return err
		}
		for _, f := range feeds {
			if f.gen.role == roleBuilding && f.gen.cursor >= highest {
				if feeds, err = m.swap(ctx, s, feeds, f); err != nil {
					return err
				}
				break
			}
		}
		m.setProgress(progressOf(feeds, highest))

		f := m.nextFeed(feeds, highest, &turn)
		if f == nil {
			if err := waitFor(m.config.Processor.Processed()); err != nil {
				return err
			}
			continue
		}
		if m.checkDisk() {
			if err := waitFor(m.config.Processor.Processed()); err != nil {
				return err
			}
			continue
		}

		b, err := m.nextBatch(ctx, f.sent)
		if err != nil {
			return err
		}
		if highest, err = m.config.Archive.HighestProcessedSeq(ctx); err != nil {
			return err
		}
		b.commit = b.lastSeq >= highest || f.uncommitted+len(b.messages) >= m.commitMessages ||
			(f.uncommitted > 0 && time.Since(f.firstUncommitted) >= commitAfter)
		if err := m.send(ctx, s.client, f.gen.id, b); err != nil {
			m.countRebuildFailure(ctx, s, f, err)
			return err
		}
		if f.gen.rebuildID != nil {
			m.mu.Lock()
			m.rebuildFailures = 0
			m.mu.Unlock()
		}
		f.sent = b.lastSeq
		if b.commit {
			if err := m.commit(ctx, f); err != nil {
				return err
			}
			wait.reset()
			m.maybeMaintain(ctx, s, f)
		} else {
			if f.uncommitted == 0 {
				f.firstUncommitted = time.Now()
			}
			f.uncommitted += len(b.messages)
		}
		if m.batchSent != nil {
			m.batchSent(sentBatch{generation: f.gen.id, messages: b.messages, documents: len(b.documents), commit: b.commit})
		}
	}
}

// commit saves f's cursor once the search process has committed its batches.
func (m *Module) commit(ctx context.Context, f *feed) error {
	if err := fault.At("after-index-commit"); err != nil {
		return err
	}
	if err := m.saveCursor(ctx, f.gen, f.sent); err != nil {
		return err
	}
	point := "after-cursor-commit"
	if f.gen.rebuildID != nil {
		point = "rebuild-after-cursor-commit"
	}
	if err := fault.At(point); err != nil {
		return err
	}
	f.uncommitted = 0
	f.commits++
	m.config.Status.RecordSuccess(opstatus.IndexWriteFailed)
	return nil
}

// nextFeed picks the next feed with messages to send, taking turns. While
// the index needs a rebuild, only a building generation receives batches.
func (m *Module) nextFeed(feeds []*feed, highest int64, turn *int) *feed {
	stopped := m.mayBeIncomplete()
	for range feeds {
		*turn = (*turn + 1) % len(feeds)
		f := feeds[*turn]
		if f.sent < highest && (f.gen.role == roleBuilding || !stopped) {
			return f
		}
	}
	return nil
}

// openGenerations opens every generation in the search process and returns
// their feeds. A generation whose directory is gone starts a new build. An
// active index the search process can't read marks Needs rebuild, and a
// rebuild whose index can't be created fails.
func (m *Module) openGenerations(ctx context.Context, s *session) ([]*feed, error) {
	generations, err := m.loadGenerations(ctx)
	if err != nil {
		return nil, err
	}
	var feeds []*feed
	for i := 0; i < len(generations); i++ {
		g := &generations[i]
		err := m.open(ctx, s, g)
		switch {
		case err == nil:
			feeds = append(feeds, m.newFeed(g))
		case isAnswer(err, "index_missing"):
			created, err := m.replaceGenerations(ctx)
			if err != nil {
				return nil, err
			}
			generations = []generation{created}
			i = -1
			feeds = nil
		case errors.Is(err, errStopped), ctx.Err() != nil:
			return nil, err
		case g.role == roleActive && isAnswer(err, "index_damaged"):
			m.markNeedsRebuild(ctx, opstatus.CauseIndexDamaged)
		case g.rebuildID != nil:
			m.failRebuild(ctx, s, g)
		default:
			if answer, ok := errors.AsType[*answerError](err); ok && answer.code == "write_failed" {
				m.recordWriteFailed(g.id, nil, answer.cause)
			}
			return nil, fmt.Errorf("open index %d: %w", g.id, err)
		}
	}
	// Searches go to the active index, or else to the one being built.
	var search int64
	for _, f := range feeds {
		if f.gen.role == roleActive || search == 0 {
			search = f.gen.id
		}
	}
	s.setSearch(search)
	return feeds, nil
}

// open opens g in the search process. A building generation is created
// unless it has a cursor, whose index must still be there.
func (m *Module) open(ctx context.Context, s *session, g *generation) error {
	return s.client.call(ctx, opstatus.OperationOpen, func(id int64) any {
		return openRequest{ID: id, Op: "open", Generation: g.id, Create: g.role == roleBuilding && g.cursor == 0}
	}, nil)
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
	err := c.callSent(ctx, opstatus.OperationIndex, func(id int64) any {
		return indexRequest{ID: id, Op: "index", Generation: generationID, Documents: b.documents, Commit: b.commit}
	}, nil, func() { _ = fault.At("after-index-send") })
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
