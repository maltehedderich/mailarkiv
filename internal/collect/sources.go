package collect

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// State is what a source is doing.
type State struct{ name string }

var (
	Waiting  = State{"waiting"}
	Syncing  = State{"syncing"}
	Retrying = State{"retrying"}
	// NeedsAttention waits for an administrator, such as after a rejected
	// sign-in.
	NeedsAttention = State{"needs_attention"}
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
	// NextSyncAt is set while the source is Waiting or Retrying.
	NextSyncAt *time.Time
	// Failure is set while the source is Retrying or needs attention.
	Failure *Failure
}

// Failure is the run of failed syncs a source is in.
type Failure struct {
	Category Category
	// Since is the first failure of the run.
	Since          time.Time
	FailedAttempts *int
	LastAt         *time.Time
	// Reply is the last failure's NO or BAD code and text, if the server
	// sent one. The log never holds it.
	Reply *string
}

// CheckFailedError is a sign-in that failed while a source was checked.
// Reply is the server's NO or BAD code and text, or empty.
type CheckFailedError struct {
	Category Category
	Reply    string
}

func (e *CheckFailedError) Error() string { return "the sign-in failed: " + e.Category.String() }

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
// *InvalidFieldsError, ErrNoKey, or a *CheckFailedError, and saves nothing then.
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
	var failed *failure
	if errors.As(err, &failed) {
		return Source{}, &CheckFailedError{Category: failed.category, Reply: failed.reply}
	}
	if err != nil {
		return Source{}, err
	}
	// Sign-in worked, which is all the check needs to know.
	_ = session.logout()

	id, err := c.save(ctx, source)
	if err != nil {
		return Source{}, err
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

// save inserts the source with its sealed password and its folders.
func (c *Collector) save(ctx context.Context, source newSource) (int64, error) {
	sealed := c.deps.Key.Seal(source.password, source.host, source.userName)
	var id int64
	err := sqlitedb.InTx(ctx, c.deps.DB.Writer, func(tx *sql.Tx) error {
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
		return 0, fmt.Errorf("save the source: %w", err)
	}
	return id, nil
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
	rows := map[int64]*stateRow{}
	err := sqlitedb.InTx(ctx, c.deps.DB.Reader, func(tx *sql.Tx) error {
		query, err := tx.QueryContext(ctx, `
SELECT s.id, s.name, s.host, s.port, s.security, s.user_name, s.last_success_at, s.last_success_archived,
       s.sign_in_rejected_at, s.failed_attempts, s.failing_since, s.last_failure_category, s.last_failure_at,
       s.last_server_reply
FROM imap_source s
WHERE s.removed_at IS NULL `+filter+`
ORDER BY s.name, s.id`, args...)
		if err != nil {
			return err
		}
		defer query.Close()
		for query.Next() {
			var source Source
			var row stateRow
			var security string
			var successAt, successArchived sql.NullInt64
			if err := query.Scan(&source.ID, &source.Name, &source.Host, &source.Port, &security, &source.UserName,
				&successAt, &successArchived, &row.signInRejectedAt, &row.failedAttempts, &row.failingSince,
				&row.lastFailureCategory, &row.lastFailureAt, &row.lastServerReply); err != nil {
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
			rows[source.ID] = &row
		}
		if err := query.Err(); err != nil {
			return err
		}
		byID := map[int64]*Source{}
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
		source := &sources[i]
		source.Collected = counts[source.ID]
		if err := c.setState(source, rows[source.ID]); err != nil {
			return nil, fmt.Errorf("read source %d: %w", source.ID, err)
		}
	}
	return sources, nil
}

// stateRow is what a source's state comes from in its row.
type stateRow struct {
	signInRejectedAt    sql.NullInt64
	failedAttempts      int
	failingSince        sql.NullInt64
	lastFailureCategory sql.NullString
	lastFailureAt       sql.NullInt64
	lastServerReply     sql.NullString
}

// setState sets the source's state from its row and its runner, taking the
// first that applies: Syncing, Needs attention, Retrying, Waiting. The
// caller holds mu.
func (c *Collector) setState(source *Source, row *stateRow) error {
	r, running := c.runners[source.ID]
	switch {
	case running && r.syncing:
		progress := r.progress
		source.State = Syncing
		source.Progress = &progress
		return nil
	case row.signInRejectedAt.Valid:
		source.State = NeedsAttention
	case row.failedAttempts > 0:
		source.State = Retrying
	default:
		source.State = Waiting
	}
	if running && source.State != NeedsAttention {
		due := r.due
		source.NextSyncAt = &due
	}
	if row.failedAttempts == 0 {
		return nil
	}
	category, err := parseSyncCategory(row.lastFailureCategory.String)
	if err != nil {
		return err
	}
	attempts := row.failedAttempts
	source.Failure = &Failure{
		Category:       category,
		Since:          time.UnixMilli(row.failingSince.Int64).UTC(),
		FailedAttempts: &attempts,
		LastAt:         optionalTime(row.lastFailureAt),
	}
	if row.lastServerReply.Valid {
		reply := row.lastServerReply.String
		source.Failure.Reply = &reply
	}
	return nil
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
