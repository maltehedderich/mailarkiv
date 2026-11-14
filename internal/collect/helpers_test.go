package collect

import (
	"bytes"
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/imaptest"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// The mailbox every double serves. The password is a marker, so a leak of it
// is easy to find.
const (
	testUser     = "anna@example.test"
	testPassword = "MARKER-PW-4f1c"
)

// testNow is where the fake clock starts.
var testNow = time.Date(2026, 11, 6, 15, 50, 52, 123_000_000, time.UTC)

// testConfig keeps each limit short, so a stall fails within two seconds
// and a healthy step never comes near one.
var testConfig = shortLimits(2*time.Second, time.Second)

// shortLimits is the production config with the connect and idle limits
// given and a check limit of 5 seconds.
func shortLimits(connect, idle time.Duration) config {
	cfg := productionConfig()
	cfg.connect, cfg.idle, cfg.check = connect, idle, 5*time.Second
	return cfg
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.String()
}

// fakeClock stands in for time. Deadlines on connections use real time.
// Its timers fire only when Advance passes them.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*fakeTimer
}

type fakeTimer struct {
	at    time.Time
	fired chan time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	c.timers = slices.DeleteFunc(c.timers, func(timer *fakeTimer) bool {
		if timer.at.After(c.now) {
			return false
		}
		timer.fired <- c.now
		return true
	})
}

// After is the timer option for the fake clock.
func (c *fakeClock) After(d time.Duration) (<-chan time.Time, func() bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	timer := &fakeTimer{at: c.now.Add(d), fired: make(chan time.Time, 1)}
	if d <= 0 {
		timer.fired <- c.now
		return timer.fired, func() bool { return false }
	}
	c.timers = append(c.timers, timer)
	return timer.fired, func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		before := len(c.timers)
		c.timers = slices.DeleteFunc(c.timers, func(other *fakeTimer) bool { return other == timer })
		return len(c.timers) < before
	}
}

// waitForTimers waits until n timers are set, so an Advance after it fires
// them.
func (c *fakeClock) waitForTimers(t *testing.T, n int) {
	t.Helper()
	waitFor(t, strconv.Itoa(n)+" timers", func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		return len(c.timers) == n
	})
}

// fixture is a data directory with a real catalog and archive, and a key
// outside it.
type fixture struct {
	dataDir string
	db      *sqlitedb.DB
	archive *archive.Archive
	status  *opstatus.Status
	key     *passwordkey.Key
	log     *lockedBuffer
	clock   *fakeClock
	roots   *x509.CertPool
}

// newFixture trusts the CA of each double.
func newFixture(t *testing.T, doubles ...*imaptest.Server) *fixture {
	t.Helper()
	f := &fixture{log: &lockedBuffer{}, clock: &fakeClock{now: testNow}, roots: x509.NewCertPool()}
	for _, double := range doubles {
		if !f.roots.AppendCertsFromPEM(double.CAPEM()) {
			t.Fatal("the double's CA isn't PEM")
		}
	}

	f.dataDir = filepath.Join(t.TempDir(), "data")
	if err := os.Mkdir(f.dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitedb.Open(filepath.Join(f.dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	f.db = db

	logger := slog.New(slog.NewJSONHandler(f.log, nil))
	f.status = opstatus.New(logger, f.clock.Now)
	f.archive, err = archive.Open(f.dataDir, db, f.status, logger, f.clock.Now)
	if err != nil {
		t.Fatal(err)
	}

	keyPath := filepath.Join(t.TempDir(), "key")
	if err := passwordkey.Create(keyPath); err != nil {
		t.Fatal(err)
	}
	f.key, err = passwordkey.Load(keyPath)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *fixture) deps() Deps {
	return Deps{
		DB:      f.db,
		Archive: f.archive,
		Status:  f.status,
		Key:     f.key,
		Logger:  slog.New(slog.NewJSONHandler(f.log, nil)),
		Now:     f.clock.Now,
		Roots:   f.roots,
	}
}

// start opens a collector on the fixture's catalog with testConfig and
// starts it. It stops when the test ends.
func (f *fixture) start(t *testing.T) *Collector {
	t.Helper()
	return f.startWith(t, testConfig)
}

// startWith is start with another config.
func (f *fixture) startWith(t *testing.T, cfg config) *Collector {
	t.Helper()
	collector, err := open(context.Background(), f.deps(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	collector.Start()
	t.Cleanup(collector.Stop)
	return collector
}

// insertSource saves a source straight to the catalog, as Add does after a
// sign-in that worked, and returns its ID. A collector opened after it
// syncs it at start.
func insertSource(t *testing.T, deps Deps, input SourceInput) int64 {
	t.Helper()
	source, err := parseInput(input)
	if err != nil {
		t.Fatal(err)
	}
	id, err := (&Collector{deps: deps}).save(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

// sourceRow is a source's failure and success columns.
type sourceRow struct {
	FailedAttempts      int64
	FailingSince        sql.NullInt64
	LastFailureCategory sql.NullString
	LastFailureAt       sql.NullInt64
	LastServerReply     sql.NullString
	SignInRejectedAt    sql.NullInt64
	LastSuccessAt       sql.NullInt64
	LastSuccessArchived sql.NullInt64
}

func (f *fixture) sourceRow(t *testing.T, id int64) sourceRow {
	t.Helper()
	var row sourceRow
	if err := f.db.Reader.QueryRow(`
SELECT failed_attempts, failing_since, last_failure_category, last_failure_at, last_server_reply,
       sign_in_rejected_at, last_success_at, last_success_archived
FROM imap_source WHERE id = ?`, id).Scan(&row.FailedAttempts, &row.FailingSince, &row.LastFailureCategory,
		&row.LastFailureAt, &row.LastServerReply, &row.SignInRejectedAt, &row.LastSuccessAt, &row.LastSuccessArchived); err != nil {
		t.Fatal(err)
	}
	return row
}

func (f *fixture) get(t *testing.T, collector *Collector, id int64) Source {
	t.Helper()
	source, err := collector.Get(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return source
}

func nullString(s string) sql.NullString { return sql.NullString{String: s, Valid: true} }

func newDouble(t *testing.T, security imaptest.Security) *imaptest.Server {
	t.Helper()
	return newDoubleFor(t, security, testUser)
}

// newDoubleFor serves user's mailbox, with testPassword.
func newDoubleFor(t *testing.T, security imaptest.Security, user string) *imaptest.Server {
	t.Helper()
	double, err := imaptest.New(imaptest.Options{Security: security, User: user, Password: testPassword})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := double.Close(); err != nil {
			t.Errorf("close the double: %v", err)
		}
	})
	return double
}

// sourceFor is a new source for the double's mailbox.
func sourceFor(t *testing.T, double *imaptest.Server, security string, folders ...string) SourceInput {
	t.Helper()
	host, port, err := net.SplitHostPort(double.Addr())
	if err != nil {
		t.Fatal(err)
	}
	number, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	return SourceInput{
		Name:     "Anna",
		Host:     host,
		Port:     number,
		Security: security,
		UserName: testUser,
		Password: testPassword,
		Folders:  folders,
	}
}

// waitFor polls condition until it holds, and fails the test after ten
// seconds.
func waitFor(t *testing.T, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !condition() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// waitForSuccess waits until the source's last success is at the clock's
// current time, and returns the source.
func waitForSuccess(t *testing.T, collector *Collector, id int64, clock *fakeClock) Source {
	t.Helper()
	var source Source
	waitFor(t, "a successful sync", func() bool {
		var err error
		source, err = collector.Get(context.Background(), id)
		if err != nil {
			t.Fatal(err)
		}
		return source.LastSuccess != nil && source.LastSuccess.At.Equal(clock.Now()) && source.State == Waiting
	})
	return source
}

// logins counts the LOGIN commands the double has seen.
func logins(double *imaptest.Server) int {
	var n int
	for _, command := range double.Commands() {
		if command.Name == "LOGIN" {
			n++
		}
	}
	return n
}

// folderIDs maps each folder name of the source to its ID.
func (f *fixture) folderIDs(t *testing.T, sourceID int64) map[string]int64 {
	t.Helper()
	rows, err := f.db.Reader.Query("SELECT name, id FROM imap_folder WHERE source_id = ?", sourceID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	ids := map[string]int64{}
	for rows.Next() {
		var name string
		var id int64
		if err := rows.Scan(&name, &id); err != nil {
			t.Fatal(err)
		}
		ids[name] = id
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return ids
}

// blobs lists the stored originals.
func (f *fixture) blobs(t *testing.T) []string {
	t.Helper()
	var found []string
	err := filepath.WalkDir(filepath.Join(f.dataDir, "blobs"), func(path string, entry fs.DirEntry, err error) error {
		if err == nil && !entry.IsDir() {
			found = append(found, path)
		}
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return found
}

// syncAgain stops collector and starts a new one a minute later, which
// syncs every source at start.
func (f *fixture) syncAgain(t *testing.T, collector *Collector) *Collector {
	t.Helper()
	collector.Stop()
	f.clock.Advance(time.Minute)
	return f.start(t)
}

// logEntries decodes the log lines whose message is msg.
func (f *fixture) logEntries(t *testing.T, msg string) []map[string]any {
	t.Helper()
	var found []map[string]any
	for line := range strings.Lines(f.log.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		if entry["msg"] == msg {
			found = append(found, entry)
		}
	}
	return found
}

// waitForSyncFailure waits until the log has more than before "sync failed"
// lines, and returns the newest one's category.
func (f *fixture) waitForSyncFailure(t *testing.T, before int) string {
	t.Helper()
	var entries []map[string]any
	waitFor(t, "a failed sync", func() bool {
		entries = f.logEntries(t, "sync failed")
		return len(entries) > before
	})
	category, _ := entries[len(entries)-1]["category"].(string)
	return category
}

// assertLogged checks that the log has want lines with message msg, each
// with the fields in fields.
func (f *fixture) assertLogged(t *testing.T, msg string, want int, fields map[string]any) {
	t.Helper()
	entries := f.logEntries(t, msg)
	if len(entries) != want {
		t.Fatalf("the log has %d %q lines, want %d: %s", len(entries), msg, want, f.log.String())
	}
	for _, entry := range entries {
		for key, value := range fields {
			if entry[key] != value {
				t.Errorf("%q line has %s = %v, want %v", msg, key, entry[key], value)
			}
		}
	}
}

// checkpoint reads a folder's UIDVALIDITY and last UID.
func (f *fixture) checkpoint(t *testing.T, folderID int64) (uidValidity sql.NullInt64, lastUID int64) {
	t.Helper()
	if err := f.db.Reader.QueryRow(
		"SELECT uid_validity, last_uid FROM imap_folder WHERE id = ?", folderID).Scan(&uidValidity, &lastUID); err != nil {
		t.Fatal(err)
	}
	return uidValidity, lastUID
}

// problem is one imap_message_problem row.
type problem struct {
	UIDValidity int64
	UID         int64
	Kind        string
	Size        sql.NullInt64
	ReceivedAt  sql.NullInt64
	RecordedAt  int64
}

// problems lists a folder's problem rows by numbering and UID.
func (f *fixture) problems(t *testing.T, folderID int64) []problem {
	t.Helper()
	rows, err := f.db.Reader.Query(`
SELECT uid_validity, uid, kind, size, received_at, recorded_at FROM imap_message_problem
WHERE folder_id = ? ORDER BY uid_validity, uid`, folderID)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var found []problem
	for rows.Next() {
		var p problem
		if err := rows.Scan(&p.UIDValidity, &p.UID, &p.Kind, &p.Size, &p.ReceivedAt, &p.RecordedAt); err != nil {
			t.Fatal(err)
		}
		found = append(found, p)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return found
}

// listed returns the content hashes of every listed message, sorted.
func (f *fixture) listed(t *testing.T) []string {
	t.Helper()
	page, err := f.archive.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total > len(page.Items) {
		t.Fatalf("the archive lists %d messages, more than one page", page.Total)
	}
	var hashes []string
	for _, item := range page.Items {
		hashes = append(hashes, item.ContentHash)
	}
	slices.Sort(hashes)
	return hashes
}

// hashesOf returns the content hashes of bodies, sorted.
func hashesOf(bodies ...[]byte) []string {
	var hashes []string
	for _, body := range bodies {
		hashes = append(hashes, hashOf(body))
	}
	slices.Sort(hashes)
	return hashes
}

// tempFiles lists what tmp/ holds.
func (f *fixture) tempFiles(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(f.dataDir, "tmp"))
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// fetchCommands counts the UID FETCH commands the double has seen.
func fetchCommands(double *imaptest.Server) int {
	var n int
	for _, command := range double.Commands() {
		if command.Name == "UID FETCH" {
			n++
		}
	}
	return n
}

func nullInt(n int64) sql.NullInt64 { return sql.NullInt64{Int64: n, Valid: true} }
