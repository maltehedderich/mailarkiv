package collect

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// State is what a source is doing.
type State struct{ name string }

var (
	Waiting = State{"waiting"}
	Syncing = State{"syncing"}
)

// String returns the name the API shares.
func (s State) String() string { return s.name }

// Source is a source as the sources list shows it.
type Source struct {
	ID       int64
	Name     string
	Host     string
	Port     int
	Security Security
	UserName string
	State    State
	// Folders are the chosen folders, in the order the source syncs them.
	Folders []Folder
	// Progress is set while a sync runs.
	Progress    *Progress
	LastSuccess *Success
	// Collected counts the distinct messages with an entry from this source.
	Collected int
}

type Folder struct {
	Name             string
	UnavailableSince *time.Time
	RenumberedAt     *time.Time
}

// Progress counts the running sync's messages so far.
type Progress struct {
	Archived int
	Found    int
}

// Success is the latest sync that succeeded.
type Success struct {
	At       time.Time
	Archived int
}

// HasKey reports whether serve has the key, without which no source can be
// added or synced.
func (c *Collector) HasKey() bool { return c.deps.Key != nil }

// Add checks the input, signs in to the mailbox, and only then saves the
// source with its folders and starts its first sync. It returns an
// *InvalidFieldsError, ErrNoKey, or a failed sign-in, and saves nothing then.
// Signing in has the check limit.
func (c *Collector) Add(ctx context.Context, in SourceInput) (Source, error) {
	source, err := parseInput(in)
	if err != nil {
		return Source{}, err
	}
	if c.deps.Key == nil {
		return Source{}, ErrNoKey
	}

	check, cancel := context.WithTimeout(ctx, c.cfg.check)
	defer cancel()
	session, err := signIn(check, c.cfg, c.deps.Roots, source.mailbox())
	if err != nil {
		return Source{}, err
	}
	// Sign-in worked, which is all the check needs to know.
	_ = session.logout()

	sealed := c.deps.Key.Seal(source.password, source.host, source.userName)
	var id int64
	err = sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
		inserted, err := tx.ExecContext(ctx,
			`INSERT INTO imap_source (name, host, port, security, user_name, user_key, sealed_password, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			source.name, source.host, source.port, source.security.String(), source.userName, source.userKey,
			sealed, c.deps.Now().UnixMilli())
		if err != nil {
			return err
		}
		if id, err = inserted.LastInsertId(); err != nil {
			return err
		}
		for _, folder := range source.folders {
			if _, err := tx.ExecContext(ctx,
				"INSERT INTO imap_folder (source_id, name, chosen) VALUES (?, ?, 1)", id, folder); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return Source{}, fmt.Errorf("save the source: %w", err)
	}
	c.addRunner(id)

	folders := make([]Folder, len(source.folders))
	for i, name := range source.folders {
		folders[i] = Folder{Name: name}
	}
	// The first sync starts at once, so the answer says so even if the
	// runner hasn't picked it up yet.
	return Source{
		ID:       id,
		Name:     source.name,
		Host:     source.host,
		Port:     source.port,
		Security: source.security,
		UserName: source.userName,
		State:    Syncing,
		Folders:  folders,
		Progress: &Progress{},
	}, nil
}

// List returns every source that wasn't removed, by name, then ID.
func (c *Collector) List(ctx context.Context) ([]Source, error) {
	return c.load(ctx, "", nil)
}

// Get returns one source, or ErrNotFound.
func (c *Collector) Get(ctx context.Context, id int64) (Source, error) {
	sources, err := c.load(ctx, "AND s.id = ?", []any{id})
	if err != nil {
		return Source{}, err
	}
	if len(sources) == 0 {
		return Source{}, ErrNotFound
	}
	return sources[0], nil
}

// load reads the sources that match filter, a condition on imap_source s.
func (c *Collector) load(ctx context.Context, filter string, args []any) ([]Source, error) {
	sources := []Source{}
	byID := map[int64]*Source{}
	err := sqlitedb.InTx(ctx, c.deps.DB.Reader, func(tx *sql.Tx) error {
		rows, err := tx.QueryContext(ctx, `
SELECT s.id, s.name, s.host, s.port, s.security, s.user_name, s.last_success_at, s.last_success_archived
FROM imap_source s
WHERE s.removed_at IS NULL `+filter+`
ORDER BY s.name, s.id`, args...)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var source Source
			var security string
			var successAt, successArchived sql.NullInt64
			if err := rows.Scan(&source.ID, &source.Name, &source.Host, &source.Port, &security, &source.UserName,
				&successAt, &successArchived); err != nil {
				return err
			}
			if source.Security, err = parseSecurity(security); err != nil {
				return err
			}
			if successAt.Valid {
				source.LastSuccess = &Success{At: time.UnixMilli(successAt.Int64).UTC(), Archived: int(successArchived.Int64)}
			}
			source.Folders = []Folder{}
			sources = append(sources, source)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		for i := range sources {
			byID[sources[i].ID] = &sources[i]
		}
		return loadFolders(ctx, tx, filter, args, byID)
	})
	if err != nil {
		return nil, fmt.Errorf("read sources: %w", err)
	}

	ids := make([]int64, len(sources))
	for i, source := range sources {
		ids[i] = source.ID
	}
	counts, err := c.deps.Archive.CollectedCounts(ctx, ids)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	for i := range sources {
		sources[i].Collected = counts[sources[i].ID]
		sources[i].State = Waiting
		if r, ok := c.runners[sources[i].ID]; ok && r.syncing {
			progress := r.progress
			sources[i].State = Syncing
			sources[i].Progress = &progress
		}
	}
	return sources, nil
}

func loadFolders(ctx context.Context, tx *sql.Tx, filter string, args []any, byID map[int64]*Source) error {
	rows, err := tx.QueryContext(ctx, `
SELECT f.source_id, f.name, f.unavailable_since, f.renumbered_at
FROM imap_folder f
JOIN imap_source s ON s.id = f.source_id
WHERE f.chosen = 1 AND s.removed_at IS NULL `+filter+`
ORDER BY f.id`, args...)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var sourceID int64
		var folder Folder
		var unavailableSince, renumberedAt sql.NullInt64
		if err := rows.Scan(&sourceID, &folder.Name, &unavailableSince, &renumberedAt); err != nil {
			return err
		}
		folder.UnavailableSince = optionalTime(unavailableSince)
		folder.RenumberedAt = optionalTime(renumberedAt)
		if source, ok := byID[sourceID]; ok {
			source.Folders = append(source.Folders, folder)
		}
	}
	return rows.Err()
}

func parseSecurity(name string) (Security, error) {
	for _, security := range []Security{TLS, StartTLS} {
		if security.String() == name {
			return security, nil
		}
	}
	return Security{}, fmt.Errorf("unknown security %q", name)
}

func optionalTime(ms sql.NullInt64) *time.Time {
	if !ms.Valid {
		return nil
	}
	t := time.UnixMilli(ms.Int64).UTC()
	return &t
}
