package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// metadataBatch is how many UIDs one metadata fetch asks for.
const metadataBatch = 500

// errPasswordUnreadable means the source's password can't be opened: there
// is no key, or the stored password doesn't open with it.
var errPasswordUnreadable = errors.New("the stored password can't be opened")

// folderRow is a chosen folder with its checkpoint.
type folderRow struct {
	id          int64
	name        string
	uidValidity sql.NullInt64
	lastUID     imap.UID
}

// sync runs one sync of r's source and records how it ended.
func (c *Collector) sync(ctx context.Context, r *runner) {
	c.setSyncing(r, true)
	archived, err := c.collect(ctx, r)
	// The state leaves syncing before the success is recorded, so a reader
	// never sees the new success beside the syncing state.
	c.setSyncing(r, false)

	logger := c.deps.Logger.With("source_id", r.id)
	var failed *failure
	switch {
	case ctx.Err() != nil:
		// Stopped. That isn't a failure, so nothing is recorded.
	case errors.Is(err, errPasswordUnreadable):
		logger.Warn("password unreadable", "category", PasswordUnreadable.String())
	case errors.As(err, &failed):
		// The error may hold the server's reply, which stays out of the log.
		logger.Warn("sync failed", "category", failed.category.String())
	case err != nil:
		logger.Error("sync failed", "category", CatalogWriteFailed.String(), "error", err.Error())
	default:
		if err := c.recordSuccess(context.WithoutCancel(ctx), r.id, archived); err != nil {
			logger.Error("sync failed", "category", CatalogWriteFailed.String(), "error", err.Error())
		}
	}
}

func (c *Collector) setSyncing(r *runner, syncing bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.syncing = syncing
	r.progress = Progress{}
}

func (c *Collector) addProgress(r *runner, found, archived int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	r.progress.Found += found
	r.progress.Archived += archived
}

// collect signs in and archives every new message in each chosen folder, in
// id order. It returns how many messages archive stored.
func (c *Collector) collect(ctx context.Context, r *runner) (int, error) {
	box, sealed, err := c.loadMailbox(ctx, r.id)
	if err != nil {
		return 0, err
	}
	if c.deps.Key == nil {
		return 0, errPasswordUnreadable
	}
	if box.password, err = c.deps.Key.Open(sealed, box.host, box.userName); err != nil {
		return 0, errPasswordUnreadable
	}
	folders, err := c.chosenFolders(ctx, r.id)
	if err != nil {
		return 0, err
	}

	s, err := signIn(ctx, c.cfg, c.deps.Roots, box)
	if err != nil {
		return 0, err
	}
	var archived int
	for _, folder := range folders {
		n, err := c.collectFolder(ctx, s, r, folder)
		archived += n
		if err != nil {
			s.close()
			return archived, err
		}
	}
	// LOGOUT without CLOSE, which would expunge.
	return archived, s.logout()
}

// messageMeta is what the server reports about a message before its body.
type messageMeta struct {
	// size is RFC822.SIZE, or the literal's announced size when the server
	// reported none. It is invalid when neither is known.
	size     sql.NullInt64
	received *time.Time
}

// message is one UID a sync handles.
type message struct {
	sourceID    int64
	folderID    int64
	uidValidity uint32
	uid         imap.UID
	// retry is whether the message was waiting for a retry rather than new.
	// A retry doesn't move the checkpoint.
	retry bool
	messageMeta
}

func (m message) collectedMeta() archive.CollectedMeta {
	return archive.CollectedMeta{SourceID: m.sourceID, FolderID: m.folderID, ReceivedAt: m.received}
}

// collectFolder examines the folder read-only, handles its new messages in
// rising UID order, and then retries the messages waiting for it.
func (c *Collector) collectFolder(ctx context.Context, s *session, r *runner, folder folderRow) (int, error) {
	selected, err := s.examine(folder.name)
	if err != nil {
		return 0, err
	}

	uidValidity := selected.UIDValidity
	switch {
	case !folder.uidValidity.Valid:
		if err := c.storeUIDValidity(context.WithoutCancel(ctx), folder.id, uidValidity); err != nil {
			return 0, catalogFailure(err)
		}
		folder.lastUID = 0
	case folder.uidValidity.Int64 != int64(uidValidity):
		// The checkpoint and the problem rows count under the old numbering,
		// so the folder starts over. Archive keeps one copy of each message.
		if err := c.renumber(context.WithoutCancel(ctx), folder.id, uidValidity); err != nil {
			return 0, catalogFailure(err)
		}
		folder.lastUID = 0
	}

	var uids []imap.UID
	if selected.UIDNext == 0 || selected.UIDNext > folder.lastUID+1 {
		if uids, err = s.newUIDs(folder.lastUID); err != nil {
			return 0, err
		}
	}
	// Read before any new message is handled, so a message that fails in
	// this sync waits for the next one.
	retries, err := c.waitingUIDs(ctx, folder.id, uidValidity)
	if err != nil {
		return 0, catalogFailure(err)
	}
	c.addProgress(r, len(uids)+len(retries), 0)
	if len(uids)+len(retries) == 0 {
		return 0, nil
	}
	metas, err := s.metadata(slices.Concat(uids, retries))
	if err != nil {
		return 0, err
	}

	var archived int
	handle := func(uid imap.UID, retry bool) error {
		m := message{sourceID: r.id, folderID: folder.id, uidValidity: uidValidity, uid: uid, retry: retry, messageMeta: metas[uid]}
		stored, err := c.collectMessage(ctx, s, m)
		if stored {
			archived++
			c.addProgress(r, 0, 1)
		}
		return err
	}
	for _, uid := range uids {
		if err := handle(uid, false); err != nil {
			return archived, err
		}
	}
	for _, uid := range retries {
		if err := handle(uid, true); err != nil {
			return archived, err
		}
	}
	return archived, nil
}

// collectMessage handles one message and commits what it leaves behind. It
// reports whether archive stored the message, which counts even when an
// error follows.
func (c *Collector) collectMessage(ctx context.Context, s *session, m message) (bool, error) {
	if m.size.Valid && m.size.Int64 > archive.MaxSize {
		if err := c.settle(ctx, m, markTooLarge); err != nil {
			return false, err
		}
		c.notArchived(m, opstatus.MessageTooLarge, MessageTooLarge)
		return false, nil
	}

	outcome, announced, err := c.fetchBody(ctx, s, m)
	if err != nil {
		return false, err
	}
	if !m.size.Valid {
		m.size = announced
	}
	switch outcome {
	case fetchedStored:
		return true, c.settle(ctx, m, clearRetry)
	case fetchedEmpty:
		return false, c.waitForRetry(ctx, m)
	case fetchedOverLimit:
		if err := c.waitForRetry(ctx, m); err != nil {
			return false, err
		}
		return false, &failure{category: ServerError, err: fmt.Errorf("UID %d's body was announced over the limit", m.uid)}
	}

	// No body came. A message still in the folder waits for a retry; one
	// that is gone was deleted after the search.
	present, err := s.contains(m.uid)
	switch {
	case err != nil:
		return false, err
	case present:
		return false, c.waitForRetry(ctx, m)
	case m.retry:
		if err := c.settle(ctx, m, markRemoved); err != nil {
			return false, err
		}
		c.notArchived(m, opstatus.MessageRemoved, MessageRemoved)
		return false, nil
	default:
		return false, c.settle(ctx, m, noChange)
	}
}

// waitForRetry records that the server didn't return the message, at each
// attempt.
func (c *Collector) waitForRetry(ctx context.Context, m message) error {
	if err := c.settle(ctx, m, markWaiting); err != nil {
		return err
	}
	c.deps.Logger.Warn("message unavailable",
		"source_id", m.sourceID, "folder_id", m.folderID, "category", MessageUnavailable.String())
	return nil
}

// notArchived reports a message that will never be archived. Its record is
// committed first, so this happens once per message.
func (c *Collector) notArchived(m message, event opstatus.Category, category Category) {
	c.deps.Status.RecordFailure(opstatus.FailureEvent{Category: event, SourceID: &m.sourceID})
	c.deps.Logger.Warn("message not archived",
		"source_id", m.sourceID, "folder_id", m.folderID, "category", category.String())
}

// fetched is how a body fetch ended, when the sync goes on.
type fetched int

const (
	fetchedStored fetched = iota
	// fetchedNothing means no FETCH data or a NIL body.
	fetchedNothing
	// fetchedEmpty means an empty body, which archive refuses.
	fetchedEmpty
	// fetchedOverLimit means the literal was announced over the limit. The
	// session is closed without reading it, so the sync ends.
	fetchedOverLimit
)

// fetchBody fetches one message's body and streams it to archive. It also
// returns the literal's announced size. An error ends the sync.
func (c *Collector) fetchBody(ctx context.Context, s *session, m message) (fetched, sql.NullInt64, error) {
	outcome := fetchedNothing
	var announced sql.NullInt64
	var failed error
	err := s.do(func() error {
		fetch := s.client.Fetch(imap.UIDSetNum(m.uid), &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		})
		for message := fetch.Next(); message != nil; message = fetch.Next() {
			for item := message.Next(); item != nil; item = message.Next() {
				section, ok := item.(imapclient.FetchItemDataBodySection)
				if !ok || section.Literal == nil || outcome != fetchedNothing {
					continue
				}
				announced = sql.NullInt64{Int64: section.Literal.Size(), Valid: true}
				body := &literalBody{literal: section.Literal}
				if section.Literal.Size() > archive.MaxSize {
					s.abort(body)
					outcome = fetchedOverLimit
					return nil
				}
				result, err := c.deps.Archive.StoreCollected(ctx, body, m.collectedMeta())
				switch result.(type) {
				case archive.Stored:
					outcome = fetchedStored
				case archive.Refused:
					outcome = fetchedEmpty
				default:
					failed = s.storeFailure(m.uid, result, err)
					s.abort(body)
					return nil
				}
			}
		}
		return fetch.Close()
	})
	if failed != nil {
		return 0, announced, failed
	}
	if err != nil {
		return 0, announced, err
	}
	return outcome, announced, nil
}

// storeFailure gives a body that archive didn't store its sync category. It
// runs before the session closes, which would add an error of its own.
func (s *session) storeFailure(uid imap.UID, result archive.Result, err error) error {
	if err != nil {
		return catalogFailure(err)
	}
	switch result := result.(type) {
	case archive.Failed:
		return &failure{category: captureCategory(result.Category), err: errors.New("archive didn't store the message")}
	case archive.Dropped:
		return classify(s.ctx, s.conn, fmt.Errorf("UID %d's body ended early", uid))
	default:
		return &failure{category: ServerError, err: fmt.Errorf("archive returned %T for UID %d", result, uid)}
	}
}

// literalBody reads a body literal for archive. The client reports a
// connection that ends partway as the literal's end, so a literal shorter
// than announced reads as io.ErrUnexpectedEOF.
type literalBody struct {
	literal imap.LiteralReader
	read    int64
	// err is the literal's first error. Once the literal has returned one,
	// the client's reader runs again, so the literal is never read after.
	err error
}

func (b *literalBody) Read(p []byte) (int, error) {
	if b.err != nil {
		return 0, b.err
	}
	n, err := b.literal.Read(p)
	b.read += int64(n)
	if err != nil {
		if errors.Is(err, io.EOF) && b.read < b.literal.Size() {
			err = io.ErrUnexpectedEOF
		}
		b.err = err
	}
	return n, err
}

// abort closes the session in the middle of a body. The client's reader
// waits until the literal returns an error, so a literal that hasn't is read
// once more after the connection closes, which fails after any buffered
// bytes instead of reading the rest. The session can't be used after.
func (s *session) abort(body *literalBody) {
	if body.err == nil {
		_ = s.conn.Close()
		_, _ = io.Copy(io.Discard, body)
	}
	s.close()
}

// examine opens a folder read-only.
func (s *session) examine(name string) (*imap.SelectData, error) {
	var selected *imap.SelectData
	err := s.do(func() error {
		var err error
		selected, err = s.client.Select(name, &imap.SelectOptions{ReadOnly: true}).Wait()
		return err
	})
	return selected, err
}

// contains reports whether the folder still holds uid.
func (s *session) contains(uid imap.UID) (bool, error) {
	criteria := &imap.SearchCriteria{UID: []imap.UIDSet{imap.UIDSetNum(uid)}}
	var data *imap.SearchData
	err := s.do(func() error {
		var err error
		data, err = s.client.UIDSearch(criteria, nil).Wait()
		return err
	})
	if err != nil {
		return false, err
	}
	if data.All == nil {
		return false, nil
	}
	set, ok := data.All.(imap.UIDSet)
	if !ok {
		return false, &failure{category: ServerError, err: errors.New("UID SEARCH answered with sequence numbers")}
	}
	found, ok := set.Nums()
	if !ok {
		return false, &failure{category: ServerError, err: errors.New("UID SEARCH answered with an open range")}
	}
	return slices.Contains(found, uid), nil
}

// newUIDs searches for UIDs above lastUID. A search from lastUID+1 to the
// highest UID also matches the highest one when nothing is newer, so the
// result is filtered.
func (s *session) newUIDs(lastUID imap.UID) ([]imap.UID, error) {
	criteria := &imap.SearchCriteria{UID: []imap.UIDSet{{{Start: lastUID + 1, Stop: 0}}}}
	var data *imap.SearchData
	err := s.do(func() error {
		var err error
		data, err = s.client.UIDSearch(criteria, nil).Wait()
		return err
	})
	if err != nil {
		return nil, err
	}
	if data.All == nil {
		return nil, nil
	}
	set, ok := data.All.(imap.UIDSet)
	if !ok {
		return nil, &failure{category: ServerError, err: errors.New("UID SEARCH answered with sequence numbers")}
	}
	all, ok := set.Nums()
	if !ok {
		return nil, &failure{category: ServerError, err: errors.New("UID SEARCH answered with an open range")}
	}
	uids := slices.DeleteFunc(all, func(uid imap.UID) bool { return uid <= lastUID })
	slices.Sort(uids)
	return slices.Compact(uids), nil
}

// metadata fetches each UID's RFC822.SIZE and INTERNALDATE in batches. A UID
// the server returned nothing for has no entry.
func (s *session) metadata(uids []imap.UID) (map[imap.UID]messageMeta, error) {
	metas := make(map[imap.UID]messageMeta, len(uids))
	for batch := range slices.Chunk(uids, metadataBatch) {
		err := s.do(func() error {
			fetch := s.client.Fetch(imap.UIDSetNum(batch...), &imap.FetchOptions{
				UID:          true,
				RFC822Size:   true,
				InternalDate: true,
			})
			for message := fetch.Next(); message != nil; message = fetch.Next() {
				var uid imap.UID
				var meta messageMeta
				for item := message.Next(); item != nil; item = message.Next() {
					switch item := item.(type) {
					case imapclient.FetchItemDataUID:
						uid = item.UID
					case imapclient.FetchItemDataRFC822Size:
						meta.size = sql.NullInt64{Int64: item.Size, Valid: true}
					case imapclient.FetchItemDataInternalDate:
						if !item.Time.IsZero() {
							at := item.Time
							meta.received = &at
						}
					}
				}
				if uid != 0 {
					metas[uid] = meta
				}
			}
			return fetch.Close()
		})
		if err != nil {
			return nil, err
		}
	}
	return metas, nil
}

// captureCategory maps archive's failure category to the sync category.
func captureCategory(category opstatus.Category) Category {
	switch category {
	case opstatus.StorageFull:
		return StorageFull
	case opstatus.StorageWriteFailed:
		return StorageWriteFailed
	default:
		return CatalogWriteFailed
	}
}

func catalogFailure(err error) error {
	return &failure{category: CatalogWriteFailed, err: err}
}
