package collect

import (
	"bytes"
	"context"
	"errors"
	"net"
	"reflect"
	"slices"
	"strconv"
	"testing"

	"github.com/emersion/go-imap/v2"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

func checkInputFor(t *testing.T, double *imaptest.Server, security string) CheckInput {
	t.Helper()
	input := sourceFor(t, double, security)
	return CheckInput{Host: input.Host, Port: input.Port, Security: input.Security, UserName: input.UserName, Password: input.Password}
}

// listingDouble lists Archiv, Entwürfe, INBOX, and Zeta by name, then three
// entries no folder backs: one \Noselect, one \NonExistent, and Aaa/Projekt
// with neither.
func listingDouble(t *testing.T, listStatus bool) *imaptest.Server {
	t.Helper()
	double, err := imaptest.New(imaptest.Options{
		Security: imaptest.ImplicitTLS, User: testUser, Password: testPassword, ListStatus: listStatus,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { double.Close() })
	addFolders(t, double, "Archiv", "Entwürfe", "Zeta")
	for folder, n := range map[string]int{"INBOX": 2, "Archiv": 1, "Zeta": 3} {
		for i := range n {
			if _, err := double.AddMessage(folder, countBody(folder+" "+strconv.Itoa(i)), testNow); err != nil {
				t.Fatal(err)
			}
		}
	}
	double.AddListEntry("Archiv/Alt", imap.MailboxAttrNoSelect)
	double.AddListEntry("Weg", imap.MailboxAttrNonExistent)
	double.AddListEntry("Aaa/Projekt")
	return double
}

func count(n int) *int { return &n }

func assertFolders(t *testing.T, got, want []ServerFolder) {
	t.Helper()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("folders = %s, want %s", describeFolders(got), describeFolders(want))
	}
}

func describeFolders(folders []ServerFolder) string {
	var b bytes.Buffer
	for _, folder := range folders {
		b.WriteString(strconv.Quote(folder.Name))
		if folder.Messages == nil {
			b.WriteString(":null ")
		} else {
			b.WriteString(":" + strconv.Itoa(*folder.Messages) + " ")
		}
	}
	return b.String()
}

// statusCommands lists the folders the double answered STATUS for.
func statusCommands(double *imaptest.Server) []string {
	var folders []string
	for _, command := range double.Commands() {
		if command.Name == "STATUS" {
			folders = append(folders, command.Mailbox)
		}
	}
	return folders
}

// A check lists INBOX first, then the server's order, without folders that
// can't be opened, and with each folder's message count where the server
// gives one. It reads counts from LIST when the server offers LIST-STATUS,
// and with STATUS otherwise.
func TestCheckListsFolders(t *testing.T) {
	t.Run("LIST-STATUS", func(t *testing.T) {
		double := listingDouble(t, true)
		f := newFixture(t, double)
		collector := f.start(t)

		folders, err := collector.Check(context.Background(), checkInputFor(t, double, "tls"))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		assertFolders(t, folders, []ServerFolder{
			{"INBOX", count(2)}, {"Archiv", count(1)}, {"Entwürfe", count(0)}, {"Zeta", count(3)}, {"Aaa/Projekt", nil},
		})
		if got := statusCommands(double); len(got) != 0 {
			t.Errorf("STATUS for %q, want none: LIST gave the counts", got)
		}
	})

	t.Run("STATUS", func(t *testing.T) {
		double := listingDouble(t, false)
		double.SetHook(func(command imaptest.Command) error {
			if command.Name == "STATUS" && command.Mailbox == "Zeta" {
				return imaptest.No("", "Zeta is busy")
			}
			return nil
		})
		f := newFixture(t, double)
		collector := f.start(t)

		folders, err := collector.Check(context.Background(), checkInputFor(t, double, "tls"))
		if err != nil {
			t.Fatalf("Check: %v", err)
		}
		assertFolders(t, folders, []ServerFolder{
			{"INBOX", count(2)}, {"Archiv", count(1)}, {"Entwürfe", count(0)}, {"Zeta", nil}, {"Aaa/Projekt", nil},
		})
		got := statusCommands(double)
		slices.Sort(got)
		if want := []string{"Aaa/Projekt", "Archiv", "Entwürfe", "INBOX", "Zeta"}; !slices.Equal(got, want) {
			t.Errorf("STATUS for %q, want %q", got, want)
		}
	})
}

// A check opens no folder and saves nothing.
func TestCheckChangesNothing(t *testing.T) {
	double := listingDouble(t, false)
	f := newFixture(t, double)
	collector := f.start(t)
	if _, err := collector.Check(context.Background(), checkInputFor(t, double, "tls")); err != nil {
		t.Fatal(err)
	}
	for _, command := range double.Commands() {
		if command.Name != "LOGIN" && command.Name != "LIST" && command.Name != "STATUS" {
			t.Errorf("the check sent %+v", command)
		}
	}
	if n := f.sourceCount(t); n != 0 {
		t.Errorf("%d sources, want 0", n)
	}
}

// A check refuses a mailbox an active source already has, before it
// connects, and answers the input rules, the key, and a failed sign-in as
// Add does.
func TestCheckRefusals(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	collector := f.start(t)
	existing := insertSource(t, f.deps(), SourceInput{
		Name: "Anna", Host: "imap.example.test", Port: 993, Security: "tls",
		UserName: testUser, Password: testPassword, Folders: []string{"INBOX"},
	})

	// The port is closed, so a check that connected would fail.
	_, port, err := net.SplitHostPort(closedPort(t))
	if err != nil {
		t.Fatal(err)
	}
	closed, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	_, err = collector.Check(ctx, CheckInput{
		Host: "IMAP.example.test", Port: closed, Security: "tls", UserName: "ANNA@example.test", Password: testPassword,
	})
	var exists *SourceExistsError
	if !errors.As(err, &exists) || exists.ID != existing {
		t.Errorf("Check of a duplicate = %v, want source_exists with ID %d", err, existing)
	}

	_, err = collector.Check(ctx, CheckInput{})
	var invalid *InvalidFieldsError
	if want := []string{"host", "port", "security", "user_name", "password"}; !errors.As(err, &invalid) || !slices.Equal(invalid.Fields, want) {
		t.Errorf("Check of nothing = %v, want invalid %v", err, want)
	}

	wrong := checkInputFor(t, double, "tls")
	wrong.Password = "MARKER-PW-wrong"
	_, err = collector.Check(ctx, wrong)
	assertCheckFailed(t, err, SignInRejected, "[AUTHENTICATIONFAILED] Authentication failed")

	deps := f.deps()
	deps.Key = nil
	keyless, err := open(ctx, deps, testConfig)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := keyless.Check(ctx, checkInputFor(t, double, "tls")); !errors.Is(err, ErrNoKey) {
		t.Errorf("Check without the key = %v, want ErrNoKey", err)
	}
}

// A check for a stored source uses the stored port, security, and password
// for each field the request leaves out, and saves nothing.
func TestCheckSource(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	moved := newDouble(t, imaptest.StartTLS)
	addFolders(t, double, "Archiv")
	addFolders(t, moved, "Nur hier")
	f := newFixture(t, double, moved)
	collector := f.start(t)
	id := addSource(t, f, collector, sourceFor(t, double, "tls", "INBOX"))
	sealed := f.sealedPassword(t, id)
	before := f.get(t, collector, id)

	folders, err := collector.CheckSource(ctx, id, MailboxChange{})
	if err != nil {
		t.Fatalf("CheckSource with stored values: %v", err)
	}
	assertFolders(t, folders, []ServerFolder{{"INBOX", count(0)}, {"Archiv", count(0)}})

	_, err = collector.CheckSource(ctx, id, MailboxChange{Password: new("MARKER-PW-wrong")})
	assertCheckFailed(t, err, SignInRejected, "[AUTHENTICATIONFAILED] Authentication failed")

	_, port, err := net.SplitHostPort(moved.Addr())
	if err != nil {
		t.Fatal(err)
	}
	newPort, err := strconv.ParseInt(port, 10, 64)
	if err != nil {
		t.Fatal(err)
	}
	folders, err = collector.CheckSource(ctx, id, MailboxChange{Port: &newPort, Security: new("starttls"), Password: new("")})
	if err != nil {
		t.Fatalf("CheckSource with a new port and security: %v", err)
	}
	assertFolders(t, folders, []ServerFolder{{"INBOX", count(0)}, {"Nur hier", count(0)}})
	if n := moved.Logins(); n != 1 {
		t.Errorf("%d LOGIN commands on the new server, want 1", n)
	}

	_, err = collector.CheckSource(ctx, id, MailboxChange{Port: new(int64(0))})
	var invalid *InvalidFieldsError
	if !errors.As(err, &invalid) || !slices.Equal(invalid.Fields, []string{"port"}) {
		t.Errorf("CheckSource with port 0 = %v, want invalid port", err)
	}

	after := f.get(t, collector, id)
	if after.Port != before.Port || after.Security != before.Security {
		t.Errorf("source = %+v, want unchanged %+v", after, before)
	}
	if got := f.sealedPassword(t, id); !bytes.Equal(got, sealed) {
		t.Error("the stored password changed")
	}
}

// When the stored password doesn't open, a check for the source needs one.
func TestCheckSourceWithUnreadablePassword(t *testing.T) {
	ctx := context.Background()
	double := newDouble(t, imaptest.ImplicitTLS)
	f := newFixture(t, double)
	id := insertSource(t, f.deps(), sourceFor(t, double, "tls", "INBOX"))
	if _, err := f.db.Writer.Exec("UPDATE imap_source SET sealed_password = X'00' WHERE id = ?", id); err != nil {
		t.Fatal(err)
	}
	collector := f.start(t)

	_, err := collector.CheckSource(ctx, id, MailboxChange{})
	var invalid *InvalidFieldsError
	if !errors.As(err, &invalid) || !slices.Equal(invalid.Fields, []string{"password"}) {
		t.Errorf("CheckSource = %v, want invalid password", err)
	}
	if _, err := collector.CheckSource(ctx, id, MailboxChange{Password: new(testPassword)}); err != nil {
		t.Errorf("CheckSource with a password: %v", err)
	}
	if n := double.Logins(); n != 1 {
		t.Errorf("%d LOGIN commands, want 1", n)
	}
}
