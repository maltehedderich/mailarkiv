package collect

import (
	"bytes"
	"context"
	"crypto/x509"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
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

// testConfig keeps each limit short, so a stall fails in well under a second
// and a healthy step never comes near one.
var testConfig = config{
	connect: 2 * time.Second,
	idle:    time.Second,
	check:   5 * time.Second,
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
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
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
	collector, err := open(context.Background(), f.deps(), testConfig)
	if err != nil {
		t.Fatal(err)
	}
	collector.Start()
	t.Cleanup(collector.Stop)
	return collector
}

func newDouble(t *testing.T, security imaptest.Security) *imaptest.Server {
	t.Helper()
	double, err := imaptest.New(imaptest.Options{Security: security, User: testUser, Password: testPassword})
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
