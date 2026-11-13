package collect

import (
	"bytes"
	"context"
	"errors"
	"net"
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

const newPassword = "MARKER-PW-9d2e"

// A rename alone, or a port or security equal to the stored one, doesn't
// sign in or sync.
func TestRenameDoesNotSignIn(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, m.fixture, m.collector, input)
	waiting := m.get(t, m.collector, id)
	logins := double.Logins()

	edited, err := m.collector.Edit(ctx, id, EditInput{
		Name:          new("  Anna privat "),
		MailboxChange: MailboxChange{Port: new(input.Port), Security: new("tls")},
	})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Name != "Anna privat" {
		t.Errorf("name = %q, want %q", edited.Name, "Anna privat")
	}
	if edited.State != Waiting || edited.NextSyncAt == nil || !edited.NextSyncAt.Equal(*waiting.NextSyncAt) {
		t.Errorf("Edit answered %v, next sync %v, want waiting until %v", edited.State, edited.NextSyncAt, waiting.NextSyncAt)
	}
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands after a rename, want 0", n)
	}
	if got := m.get(t, m.collector, id).Name; got != "Anna privat" {
		t.Errorf("stored name = %q, want %q", got, "Anna privat")
	}
}

// A new port and security sign in to the server they name before anything
// is saved, and later syncs go there.
func TestEditSignsInWithNewValues(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	moved := newDouble(t, imaptest.StartTLS)
	m := newManaged(t, double, moved)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	_, port, err := net.SplitHostPort(moved.Addr())
	if err != nil {
		t.Fatal(err)
	}
	newPort, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	logins := double.Logins()

	m.clock.Advance(10 * time.Second)
	edited, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Port: &newPort, Security: new("starttls")}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.Port != int(newPort) || edited.Security != StartTLS {
		t.Errorf("Edit answered port %d, security %v, want %d, starttls", edited.Port, edited.Security, newPort)
	}
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForSuccess(t, m.collector, id, m.clock)
	if n := moved.Logins(); n != 2 {
		t.Errorf("%d LOGIN commands on the new server, want 2: the check and the sync", n)
	}
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands on the old server, want 0", n)
	}
}

// A failed check answers its category, and nothing in the request is saved.
func TestEditCheckFailed(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, m.fixture, m.collector, input)
	before := m.get(t, m.collector, id)
	sealed := m.sealedPassword(t, id)

	_, port, err := net.SplitHostPort(closedPort(t))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name     string
		change   MailboxChange
		category Category
		reply    string
	}{
		{"wrong password", MailboxChange{Password: new("MARKER-PW-wrong")}, SignInRejected, "[AUTHENTICATIONFAILED] Authentication failed"},
		{"closed port", MailboxChange{Port: &closed}, ServerNotReachable, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := m.collector.Edit(ctx, id, EditInput{
				Name:          new("Neu"),
				MailboxChange: tt.change,
				Folders:       &[]string{"INBOX", "Archiv"},
			})
			assertCheckFailed(t, err, tt.category, tt.reply)
			after := m.get(t, m.collector, id)
			if after.Name != before.Name || after.Port != before.Port || after.Security != before.Security ||
				!slices.Equal(folderNames(after.Folders), folderNames(before.Folders)) {
				t.Errorf("source = %+v, want unchanged %+v", after, before)
			}
			if got := m.sealedPassword(t, id); !bytes.Equal(got, sealed) {
				t.Error("the stored password changed")
			}
		})
	}

	// The stored password still signs in.
	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForSuccess(t, m.collector, id, m.clock)
}

// An empty or missing password keeps the stored one.
func TestEditKeepsThePassword(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	input := sourceFor(t, double, "tls", "INBOX")
	id := addSource(t, m.fixture, m.collector, input)
	sealed := m.sealedPassword(t, id)

	for _, password := range []*string{nil, new("")} {
		if _, err := m.collector.Edit(ctx, id, EditInput{Name: new("Anna"), MailboxChange: MailboxChange{Password: password}}); err != nil {
			t.Fatalf("Edit: %v", err)
		}
		if got := m.sealedPassword(t, id); !bytes.Equal(got, sealed) {
			t.Errorf("password %v changed the stored one", password)
		}
	}

	// An unchanged port with an empty password changes nothing that signs
	// in.
	logins := double.Logins()
	if _, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Port: new(input.Port), Password: new("")}}); err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if n := double.Logins() - logins; n != 0 {
		t.Errorf("%d LOGIN commands for an unchanged port, want 0", n)
	}
}

// Saving a password clears a rejected sign-in and syncs at once, with the
// new password.
func TestSavedPasswordClearsRejection(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))

	double.SetPassword(newPassword)
	m.clock.Advance(10 * time.Second)
	failures := len(m.logEntries(t, "sync failed"))
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	if category := m.waitForSyncFailure(t, failures); category != SignInRejected.String() {
		t.Fatalf("the sync failed with %q, want sign_in_rejected", category)
	}
	waitFor(t, "needs attention", func() bool { return m.get(t, m.collector, id).State == NeedsAttention })

	m.clock.Advance(10 * time.Second)
	logins := double.Logins()
	edited, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Password: new(newPassword)}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.State != Syncing {
		t.Errorf("Edit answered %v, want syncing", edited.State)
	}
	if row := m.sourceRow(t, id); row.SignInRejectedAt.Valid {
		t.Error("sign_in_rejected_at is still set")
	}
	waitForSuccess(t, m.collector, id, m.clock)
	if n := double.Logins() - logins; n != 2 {
		t.Errorf("%d LOGIN commands, want 2: the check and the sync", n)
	}
}

// Saving a password clears the unreadable mark and syncs at once.
func TestSavedPasswordClearsUnreadable(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	if _, err := m.db.Writer.Exec("UPDATE imap_source SET sealed_password = X'00' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	m.restart(t)
	source := m.get(t, m.collector, id)
	if source.State != NeedsAttention || source.Failure == nil || source.Failure.Category != PasswordUnreadable {
		t.Fatalf("source = %+v, want needs_attention for password_unreadable", source)
	}

	// Without a password, nothing can sign in to check a new port.
	_, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Port: new(int64(143))}})
	var invalid *InvalidFieldsError
	if !errors.As(err, &invalid) || !slices.Equal(invalid.Fields, []string{"password"}) {
		t.Errorf("Edit of the port = %v, want invalid password", err)
	}

	m.clock.Advance(10 * time.Second)
	edited, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Password: new(testPassword)}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.State != Syncing || edited.Failure != nil {
		t.Errorf("Edit answered %v with failure %+v, want syncing", edited.State, edited.Failure)
	}
	waitForSuccess(t, m.collector, id, m.clock)
}

// A password saved while paused stays paused until Resume.
func TestSavedPasswordWhilePaused(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	pausedAt := m.clock.Now()
	if _, err := m.collector.Pause(ctx, id); err != nil {
		t.Fatal(err)
	}
	double.SetPassword(newPassword)
	logins := double.Logins()

	edited, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Password: new(newPassword)}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	assertPaused(t, edited, pausedAt)
	m.passPeriods(t, 1)
	if n := double.Logins() - logins; n != 1 {
		t.Errorf("%d LOGIN commands, want 1: the check", n)
	}

	m.clock.Advance(10 * time.Second)
	if _, err := m.collector.Resume(ctx, id); err != nil {
		t.Fatal(err)
	}
	waitForSuccess(t, m.collector, id, m.clock)
}

// Host and user name can't change, and nothing else in the request is
// saved.
func TestEditRefusesHostAndUserName(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))

	for _, tt := range []struct {
		name string
		in   EditInput
		want []string
	}{
		{"host", EditInput{Name: new("Neu"), Host: new("imap.example.test")}, []string{"host"}},
		{"user name", EditInput{Name: new("Neu"), UserName: new(testUser)}, []string{"user_name"}},
		{"every rule", EditInput{
			Name:          new(" "),
			Host:          new("imap.example.test"),
			UserName:      new("ben@example.test"),
			MailboxChange: MailboxChange{Port: new(int64(0)), Security: new("ssl"), Password: new(string(make([]byte, 1025)))},
			Folders:       &[]string{},
		}, []string{"name", "host", "port", "security", "user_name", "password", "folders"}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := collector.Edit(ctx, id, tt.in)
			var invalid *InvalidFieldsError
			if !errors.As(err, &invalid) || !slices.Equal(invalid.Fields, tt.want) {
				t.Errorf("Edit = %v, want invalid %v", err, tt.want)
			}
			if got := f.get(t, collector, id).Name; got != "Anna" {
				t.Errorf("name = %q, want Anna", got)
			}
		})
	}
}

// Without the key, a rename works, and a change that signs in is refused.
func TestEditWithoutKey(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	deps := f.deps()
	id := insertSource(t, deps, SourceInput{
		Name: "Anna", Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: testUser, Password: testPassword, Folders: []string{"INBOX"},
	})
	deps.Key = nil
	collector, err := open(ctx, deps, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	collector.Start()
	t.Cleanup(collector.Stop)

	if _, err := collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Password: new(newPassword)}}); !errors.Is(err, ErrNoKey) {
		t.Errorf("Edit of the password = %v, want ErrNoKey", err)
	}
	if _, err := collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Port: new(int64(143))}}); !errors.Is(err, ErrNoKey) {
		t.Errorf("Edit of the port = %v, want ErrNoKey", err)
	}
	edited, err := collector.Edit(ctx, id, EditInput{Name: new("Anna privat")})
	if err != nil {
		t.Fatalf("rename without the key: %v", err)
	}
	if edited.Name != "Anna privat" {
		t.Errorf("name = %q, want Anna privat", edited.Name)
	}
}

// A password changed during a sync leaves that sync running. The next
// sign-in uses the new one.
func TestPasswordChangedDuringSync(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	m := newManaged(t, double)
	id := addSource(t, m.fixture, m.collector, sourceFor(t, double, "tls", "INBOX"))
	body := countBody("Held while the password changes")
	uid, err := double.AddMessage("INBOX", body, testNow)
	if err != nil {
		t.Fatal(err)
	}
	hold := double.HoldBody("INBOX", uid)
	m.clock.Advance(10 * time.Second)
	logins := double.Logins()
	if _, err := m.collector.SyncNow(ctx, id); err != nil {
		t.Fatal(err)
	}
	<-hold.Reached()

	// The mailbox's password changes, and so does the source's.
	double.SetPassword(newPassword)
	edited, err := m.collector.Edit(ctx, id, EditInput{MailboxChange: MailboxChange{Password: new(newPassword)}})
	if err != nil {
		t.Fatalf("Edit: %v", err)
	}
	if edited.State != Syncing {
		t.Errorf("Edit answered %v, want syncing: the held sync goes on", edited.State)
	}
	hold.Release()

	// The held sync, the check, and the next sync.
	waitFor(t, "the next sign-in", func() bool { return double.Logins()-logins == 3 })
	waitForSuccess(t, m.collector, id, m.clock)
	if got := m.collectionEntries(t, body); len(got) != 1 {
		t.Errorf("the held message has %d entries, want 1", len(got))
	}
	if row := m.sourceRow(t, id); row.FailedAttempts != 0 || row.SignInRejectedAt.Valid {
		t.Errorf("row = %+v, want no failure", row)
	}
	if n := double.BodyFetches("INBOX", uid); n != 1 {
		t.Errorf("the held message was fetched %d times, want 1", n)
	}
}
