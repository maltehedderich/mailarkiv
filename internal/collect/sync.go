package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
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

// collectFolder examines the folder read-only and stores its new messages in
// rising UID order, moving the checkpoint after each.
func (c *Collector) collectFolder(ctx context.Context, s *session, r *runner, folder folderRow) (int, error) {
	var selected *imap.SelectData
	err := s.do(func() error {
		var err error
		selected, err = s.client.Select(folder.name, &imap.SelectOptions{ReadOnly: true}).Wait()
		return err
	})
	if err != nil {
		return 0, err
	}

	switch {
	case !folder.uidValidity.Valid:
		if err := c.storeUIDValidity(context.WithoutCancel(ctx), folder.id, selected.UIDValidity); err != nil {
			return 0, catalogFailure(err)
		}
		folder.lastUID = 0
	case folder.uidValidity.Int64 != int64(selected.UIDValidity):
		// The checkpoint counts under the old numbering. Continuing under the
		// new one could skip messages.
		return 0, &failure{category: ServerError, err: fmt.Errorf("folder %d has a new UIDVALIDITY", folder.id)}
	}
	if selected.UIDNext != 0 && selected.UIDNext <= folder.lastUID+1 {
		return 0, nil
	}

	uids, err := s.newUIDs(folder.lastUID)
	if err != nil {
		return 0, err
	}
	c.addProgress(r, len(uids), 0)
	received, err := s.receivedTimes(uids)
	if err != nil {
		return 0, err
	}

	var archived int
	for _, uid := range uids {
		meta := archive.CollectedMeta{SourceID: r.id, FolderID: folder.id}
		if at, ok := received[uid]; ok {
			meta.ReceivedAt = &at
		}
		if err := c.collectMessage(ctx, s, uid, meta); err != nil {
			return archived, err
		}
		archived++
		c.addProgress(r, 0, 1)
		if err := c.moveCheckpoint(context.WithoutCancel(ctx), folder.id, uid); err != nil {
			return archived, catalogFailure(err)
		}
	}
	return archived, nil
}

// collectMessage fetches one body and streams it to archive. Any result but
// Stored ends the sync, with the checkpoint before this message.
func (c *Collector) collectMessage(ctx context.Context, s *session, uid imap.UID, meta archive.CollectedMeta) error {
	var result archive.Result
	var storeErr error
	err := s.do(func() error {
		fetch := s.client.Fetch(imap.UIDSetNum(uid), &imap.FetchOptions{
			UID:         true,
			BodySection: []*imap.FetchItemBodySection{{Peek: true}},
		})
		for message := fetch.Next(); message != nil; message = fetch.Next() {
			for item := message.Next(); item != nil; item = message.Next() {
				section, ok := item.(imapclient.FetchItemDataBodySection)
				if !ok || section.Literal == nil || result != nil || storeErr != nil {
					continue
				}
				result, storeErr = c.deps.Archive.StoreCollected(ctx, section.Literal, meta)
			}
		}
		return fetch.Close()
	})
	if storeErr != nil {
		return catalogFailure(storeErr)
	}
	if err != nil {
		return err
	}

	switch result := result.(type) {
	case archive.Stored:
		return nil
	case archive.Failed:
		return &failure{category: captureCategory(result.Category), err: errors.New("archive didn't store the message")}
	case nil:
		return &failure{category: ServerError, err: fmt.Errorf("UID %d returned no body", uid)}
	default:
		return &failure{category: ServerError, err: fmt.Errorf("archive returned %T for UID %d", result, uid)}
	}
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

// receivedTimes fetches each UID's INTERNALDATE in batches. A UID the server
// reported no time for has no entry.
func (s *session) receivedTimes(uids []imap.UID) (map[imap.UID]time.Time, error) {
	times := make(map[imap.UID]time.Time, len(uids))
	for batch := range slices.Chunk(uids, metadataBatch) {
		var messages []*imapclient.FetchMessageBuffer
		err := s.do(func() error {
			var err error
			messages, err = s.client.Fetch(imap.UIDSetNum(batch...), &imap.FetchOptions{
				UID:          true,
				RFC822Size:   true,
				InternalDate: true,
			}).Collect()
			return err
		})
		if err != nil {
			return nil, err
		}
		for _, message := range messages {
			if !message.InternalDate.IsZero() {
				times[message.UID] = message.InternalDate
			}
		}
	}
	return times, nil
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
