package collect

import (
	"context"
	"errors"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// assertCheckFailed checks that Add's error is a failed check with category
// and the server's reply, which is empty when there is none.
func assertCheckFailed(t *testing.T, err error, category Category, reply string) {
	t.Helper()
	var failed *CheckFailedError
	if !errors.As(err, &failed) {
		t.Fatalf("Add: %v, want a failed check", err)
	}
	if failed.Category != category {
		t.Errorf("the check failed with %v, want %v", failed.Category, category)
	}
	if failed.Reply != reply {
		t.Errorf("the check kept the reply %q, want %q", failed.Reply, reply)
	}
}

func (f *fixture) sourceCount(t *testing.T) int {
	t.Helper()
	var n int
	if err := f.db.Reader.QueryRow("SELECT COUNT(*) FROM imap_source").Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// A connection that isn't secure fails before LOGIN, both when a source is
// added and when it syncs.
func TestSecureConnection(t *testing.T) {
	cases := []struct {
		name    string
		options imaptest.Options
		// reply is the server's answer to STARTTLS, if it sent one.
		reply string
	}{
		{"a certificate from another CA over TLS", imaptest.Options{Security: imaptest.ImplicitTLS, Certificate: imaptest.OtherCA}, ""},
		{"a certificate from another CA over STARTTLS", imaptest.Options{Security: imaptest.StartTLS, Certificate: imaptest.OtherCA}, ""},
		{"an expired certificate over TLS", imaptest.Options{Security: imaptest.ImplicitTLS, Certificate: imaptest.Expired}, ""},
		{"an expired certificate over STARTTLS", imaptest.Options{Security: imaptest.StartTLS, Certificate: imaptest.Expired}, ""},
		{"a certificate for another name over TLS", imaptest.Options{Security: imaptest.ImplicitTLS, Certificate: imaptest.OtherName}, ""},
		{"a certificate for another name over STARTTLS", imaptest.Options{Security: imaptest.StartTLS, Certificate: imaptest.OtherName}, ""},
		{"only TLS 1.1 over TLS", imaptest.Options{Security: imaptest.ImplicitTLS, OnlyTLS11: true}, ""},
		{"only TLS 1.1 over STARTTLS", imaptest.Options{Security: imaptest.StartTLS, OnlyTLS11: true}, ""},
		{"no STARTTLS offered", imaptest.Options{Security: imaptest.StartTLS, NoStartTLS: true}, "STARTTLS not supported"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			options := tt.options
			options.User, options.Password = testUser, testPassword
			double, err := imaptest.New(options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := double.Close(); err != nil {
					t.Errorf("close the double: %v", err)
				}
			})
			security := "tls"
			if options.Security == imaptest.StartTLS {
				security = "starttls"
			}
			input := sourceFor(t, double, security, "INBOX")
			f := newFixture(t, double)

			t.Run("Add", func(t *testing.T) {
				collector := f.start(t)
				_, err := collector.Add(context.Background(), input)
				assertCheckFailed(t, err, SecureConnectionFailed, tt.reply)
				if n := f.sourceCount(t); n != 0 {
					t.Errorf("%d sources were saved, want none", n)
				}
				collector.Stop()
			})

			t.Run("a sync", func(t *testing.T) {
				id := insertSource(t, f.deps(), input)
				f.start(t)
				if category := f.waitForSyncFailure(t, 0); category != SecureConnectionFailed.String() {
					t.Errorf("the sync failed with %q, want secure_connection_failed", category)
				}
				if row := f.sourceRow(t, id); row.LastFailureCategory != nullString(SecureConnectionFailed.String()) {
					t.Errorf("last_failure_category = %v, want secure_connection_failed", row.LastFailureCategory)
				}
			})

			if n := double.Logins(); n != 0 {
				t.Errorf("the double saw %d LOGIN commands, want none", n)
			}
		})
	}
}
