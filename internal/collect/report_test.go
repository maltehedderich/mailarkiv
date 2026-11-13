package collect

import (
	"context"
	"database/sql"
	"fmt"
	"reflect"
	"testing"
	"time"
)

// at is a time the test sets in the catalog, minutes after testNow.
func at(minutes int) time.Time { return testNow.Add(time.Duration(minutes) * time.Minute) }

// reportFixture saves sources straight to the catalog and sets their columns
// by hand, so each state is exact. Its collector never starts, so nothing
// syncs.
type reportFixture struct {
	t *testing.T
	f *fixture
}

func newReportFixture(t *testing.T) *reportFixture {
	return &reportFixture{t: t, f: newFixture(t)}
}

// add saves a source named name with folders, all chosen.
func (r *reportFixture) add(name string, folders ...string) int64 {
	r.t.Helper()
	return insertSource(r.t, r.f.deps(), SourceInput{
		Name: name, Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: name + "@example.test", Password: testPassword, Folders: folders,
	})
}

func (r *reportFixture) exec(query string, args ...any) {
	r.t.Helper()
	if _, err := r.f.db.Writer.Exec(query, args...); err != nil {
		r.t.Fatalf("%s: %v", query, err)
	}
}

// fail records a run of attempts failed syncs with category, the first at
// since.
func (r *reportFixture) fail(id int64, category Category, attempts int, since time.Time) {
	r.t.Helper()
	r.exec(`UPDATE imap_source SET failed_attempts = ?, failing_since = ?, last_failure_category = ?,
	        last_failure_at = ?, last_server_reply = '[UNAVAILABLE] MARKER-REPLY-9d2e' WHERE id = ?`,
		attempts, since.UnixMilli(), category.String(), since.Add(time.Minute).UnixMilli(), id)
}

func (r *reportFixture) folder(sourceID int64, name string) int64 {
	r.t.Helper()
	return r.f.folderIDs(r.t, sourceID)[name]
}

func (r *reportFixture) unavailable(folderID int64, since time.Time) {
	r.t.Helper()
	r.exec("UPDATE imap_folder SET unavailable_since = ? WHERE id = ?", since.UnixMilli(), folderID)
}

// problem records a message problem in folderID. size and received may be
// nil.
func (r *reportFixture) problem(folderID int64, uid int, kind string, size *int64, received *time.Time, recorded time.Time) {
	r.t.Helper()
	var receivedAt sql.NullInt64
	if received != nil {
		receivedAt = nullInt(received.UnixMilli())
	}
	r.exec(`INSERT INTO imap_message_problem (folder_id, uid_validity, uid, kind, size, received_at, recorded_at)
	        VALUES (?, 7, ?, ?, ?, ?, ?)`, folderID, uid, kind, size, receivedAt, recorded.UnixMilli())
}

// open opens a collector on the catalog at testNow without starting it.
func (r *reportFixture) open() *Collector {
	r.t.Helper()
	collector, err := open(context.Background(), r.f.deps(), testConfig)
	if err != nil {
		r.t.Fatal(err)
	}
	r.t.Cleanup(collector.Stop)
	return collector
}

func attempts(n int) *int { return &n }

// The status screen lists each source that is Retrying or needs attention
// with its sync category, otherwise one with an unavailable folder, and
// otherwise one with a message waiting for retry. Each appears once, by name.
func TestProblemSources(t *testing.T) {
	r := newReportFixture(t)

	// A sync failure comes before the source's unavailable folder.
	anna := r.add("Anna", "INBOX", "Archiv")
	r.fail(anna, TimedOut, 3, at(-30))
	r.unavailable(r.folder(anna, "Archiv"), at(-60))

	bert := r.add("Bert", "INBOX")
	r.fail(bert, SignInRejected, 1, at(-20))
	r.exec("UPDATE imap_source SET sign_in_rejected_at = ? WHERE id = ?", at(-20).UnixMilli(), bert)

	// An unavailable folder comes before a waiting message. The earliest
	// folder sets when the problem began.
	carla := r.add("Carla", "INBOX", "Projekte", "Rechnungen")
	r.unavailable(r.folder(carla, "Projekte"), at(-40))
	r.unavailable(r.folder(carla, "Rechnungen"), at(-50))
	r.problem(r.folder(carla, "INBOX"), 4, "waiting_for_retry", nil, nil, at(-90))

	// The earliest waiting message sets when the problem began. Messages not
	// archived aren't a source problem: the status lists them as events.
	dora := r.add("Dora", "INBOX", "Archiv")
	r.problem(r.folder(dora, "INBOX"), 2, "waiting_for_retry", nil, nil, at(-10))
	r.problem(r.folder(dora, "Archiv"), 3, "waiting_for_retry", nil, nil, at(-15))
	r.problem(r.folder(dora, "INBOX"), 1, "too_large", nil, nil, at(-99))

	// Paused with nothing else: not a problem, even with failed attempts.
	emil := r.add("Emil", "INBOX")
	r.fail(emil, ServerError, 2, at(-25))
	r.exec("UPDATE imap_source SET paused_at = ? WHERE id = ?", at(-5).UnixMilli(), emil)

	// Paused with an unavailable folder still has the folder problem.
	frida := r.add("Frida", "INBOX")
	r.unavailable(r.folder(frida, "INBOX"), at(-35))
	r.exec("UPDATE imap_source SET paused_at = ? WHERE id = ?", at(-5).UnixMilli(), frida)

	// Healthy, apart from messages not archived.
	gustav := r.add("Gustav", "INBOX")
	r.problem(r.folder(gustav, "INBOX"), 1, "too_large", nil, nil, at(-5))
	r.problem(r.folder(gustav, "INBOX"), 2, "removed", nil, nil, at(-5))

	// A password that doesn't open with the key needs attention from the
	// collector's start.
	hanna := r.add("Hanna", "INBOX")
	r.exec("UPDATE imap_source SET sealed_password = X'00' WHERE id = ?", hanna)

	// A removed source is gone from the status.
	ida := r.add("Ida", "INBOX")
	r.fail(ida, TimedOut, 4, at(-70))
	r.exec("UPDATE imap_source SET removed_at = ?, sealed_password = NULL WHERE id = ?", at(-1).UnixMilli(), ida)

	// A folder the source no longer collects doesn't count.
	jonas := r.add("Jonas", "INBOX", "Alt")
	alt := r.folder(jonas, "Alt")
	r.unavailable(alt, at(-80))
	r.problem(alt, 9, "waiting_for_retry", nil, nil, at(-80))
	r.exec("UPDATE imap_folder SET chosen = 0 WHERE id = ?", alt)

	collector := r.open()
	got, err := collector.ProblemSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	want := []ProblemSource{
		{ID: anna, Name: "Anna", Category: TimedOut, Since: at(-30), FailedAttempts: attempts(3)},
		{ID: bert, Name: "Bert", Category: SignInRejected, Since: at(-20), FailedAttempts: attempts(1)},
		{ID: carla, Name: "Carla", Category: FolderUnavailable, Since: at(-50)},
		{ID: dora, Name: "Dora", Category: MessageUnavailable, Since: at(-15)},
		{ID: frida, Name: "Frida", Category: FolderUnavailable, Since: at(-35)},
		{ID: hanna, Name: "Hanna", Category: PasswordUnreadable, Since: testNow},
	}
	assertProblemSources(t, got, want)
}

// A source whose next attempt is running still reports its failure, so the
// problem count doesn't drop while the problem lasts.
func TestProblemSourceWhileRetrying(t *testing.T) {
	r := newReportFixture(t)
	id := r.add("Anna", "INBOX")
	r.fail(id, ServerNotReachable, 2, at(-30))

	collector := r.open()
	sync := &fakeSync{started: make(chan time.Time), results: make(chan error)}
	collector.collectFn = sync.collect
	collector.Start()
	<-sync.started
	if state := r.f.get(t, collector, id).State; state != Syncing {
		t.Fatalf("state = %v, want syncing", state)
	}

	got, err := collector.ProblemSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertProblemSources(t, got, []ProblemSource{
		{ID: id, Name: "Anna", Category: ServerNotReachable, Since: at(-30), FailedAttempts: attempts(2)},
	})

	// A success ends the problem.
	sync.results <- nil
	waitFor(t, "the success", func() bool { return r.f.get(t, collector, id).LastSuccess != nil })
	got, err = collector.ProblemSources(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	assertProblemSources(t, got, nil)
}

func assertProblemSources(t *testing.T, got, want []ProblemSource) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("problem sources = %s, want %s", describeProblems(got), describeProblems(want))
	}
	for i := range want {
		g, w := got[i], want[i]
		if g.ID != w.ID || g.Name != w.Name || g.Category != w.Category || !g.Since.Equal(w.Since) ||
			!reflect.DeepEqual(g.FailedAttempts, w.FailedAttempts) {
			t.Errorf("problem source %d = %s, want %s", i, describeProblems(got[i:i+1]), describeProblems(want[i:i+1]))
		}
	}
}

func describeProblems(sources []ProblemSource) string {
	var s string
	for _, source := range sources {
		attempts := "nil"
		if source.FailedAttempts != nil {
			attempts = fmt.Sprint(*source.FailedAttempts)
		}
		s += fmt.Sprintf("{%d %s %s %s %s} ", source.ID, source.Name, source.Category, source.Since.Format(time.RFC3339), attempts)
	}
	return "[" + s + "]"
}

// A source counts its unavailable folders, messages waiting for retry, and
// messages not archived, in the folders it collects. Its page lists the
// messages newest first, at most 100 of each, with the total.
func TestSourceProblems(t *testing.T) {
	r := newReportFixture(t)
	id := r.add("Anna", "INBOX", "Archiv", "Alt")
	inbox, archiv, alt := r.folder(id, "INBOX"), r.folder(id, "Archiv"), r.folder(id, "Alt")
	r.unavailable(archiv, at(-60))
	size, tooLarge := int64(48211), int64(160_000_000)
	received := at(-24 * 60)
	r.problem(inbox, 5, "waiting_for_retry", &size, &received, at(-20))
	r.problem(inbox, 6, "waiting_for_retry", nil, nil, at(-10))
	r.problem(inbox, 7, "too_large", &tooLarge, &received, at(-30))
	r.problem(archiv, 3, "removed", &size, nil, at(-5))
	r.problem(alt, 1, "waiting_for_retry", nil, nil, at(-1))
	r.problem(alt, 2, "too_large", nil, nil, at(-1))
	r.exec("UPDATE imap_folder SET chosen = 0 WHERE id = ?", alt)

	other := r.add("Ben", "INBOX")
	otherInbox := r.folder(other, "INBOX")
	for uid := 1; uid <= 101; uid++ {
		r.problem(otherInbox, uid, "waiting_for_retry", nil, nil, at(uid))
	}

	collector := r.open()
	ctx := context.Background()
	sources, err := collector.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[int64]ProblemCounts{}
	for _, source := range sources {
		counts[source.ID] = source.Problems
	}
	if want := (ProblemCounts{UnavailableFolders: 1, WaitingForRetry: 2, NotArchived: 2}); counts[id] != want {
		t.Errorf("Anna's problems = %+v, want %+v", counts[id], want)
	}
	if want := (ProblemCounts{WaitingForRetry: 101}); counts[other] != want {
		t.Errorf("Ben's problems = %+v, want %+v", counts[other], want)
	}

	detail, err := collector.Detail(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if want := (ProblemCounts{UnavailableFolders: 1, WaitingForRetry: 2, NotArchived: 2}); detail.Problems != want {
		t.Errorf("the page's problems = %+v, want %+v", detail.Problems, want)
	}
	assertProblemList(t, "waiting for retry", detail.Messages.WaitingForRetry, 2, []ProblemMessage{
		{Folder: "INBOX", RecordedAt: at(-10), Category: MessageUnavailable},
		{Folder: "INBOX", Size: &size, ReceivedAt: &received, RecordedAt: at(-20), Category: MessageUnavailable},
	})
	assertProblemList(t, "not archived", detail.Messages.NotArchived, 2, []ProblemMessage{
		{Folder: "Archiv", Size: &size, RecordedAt: at(-5), Category: MessageRemoved},
		{Folder: "INBOX", Size: &tooLarge, ReceivedAt: &received, RecordedAt: at(-30), Category: MessageTooLarge},
	})

	detail, err = collector.Detail(ctx, other)
	if err != nil {
		t.Fatal(err)
	}
	waiting := detail.Messages.WaitingForRetry
	if waiting.Total != 101 || len(waiting.Items) != 100 {
		t.Fatalf("Ben's waiting messages: total %d with %d items, want 101 with 100", waiting.Total, len(waiting.Items))
	}
	if first, last := waiting.Items[0].RecordedAt, waiting.Items[99].RecordedAt; !first.Equal(at(101)) || !last.Equal(at(2)) {
		t.Errorf("Ben's items run from %s to %s, want the newest 100, from %s to %s", first, last, at(101), at(2))
	}
	if notArchived := detail.Messages.NotArchived; notArchived.Total != 0 || notArchived.Items == nil || len(notArchived.Items) != 0 {
		t.Errorf("Ben's messages not archived = %+v, want an empty list", notArchived)
	}

	if _, err := collector.Detail(ctx, 999); err != ErrNotFound {
		t.Errorf("Detail of an unknown source = %v, want ErrNotFound", err)
	}
}

func assertProblemList(t *testing.T, what string, got ProblemList, total int, want []ProblemMessage) {
	t.Helper()
	if got.Total != total || len(got.Items) != len(want) {
		t.Fatalf("%s: total %d with %+v, want %d with %+v", what, got.Total, got.Items, total, want)
	}
	for i := range want {
		g, w := got.Items[i], want[i]
		if g.Folder != w.Folder || g.Category != w.Category || !g.RecordedAt.Equal(w.RecordedAt) ||
			!reflect.DeepEqual(g.Size, w.Size) || !equalTimes(g.ReceivedAt, w.ReceivedAt) {
			t.Errorf("%s item %d = %+v, want %+v", what, i, g, w)
		}
	}
}

func equalTimes(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == b
	}
	return a.Equal(*b)
}
