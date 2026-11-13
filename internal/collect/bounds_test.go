package collect

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

func TestLimitValues(t *testing.T) {
	got := productionConfig()
	if got.commandBytes != 16<<20 {
		t.Errorf("command bound = %d bytes, want 16 MiB", got.commandBytes)
	}
	if got.bodyBytes != 150_000_000+1<<20 {
		t.Errorf("body fetch bound = %d bytes, want 150,000,000 plus 1 MiB", got.bodyBytes)
	}
}

// sizedBody is a message of size bytes.
func sizedBody(subject string, size int) []byte {
	head := "From: ben@example.test\r\nSubject: " + subject + "\r\n\r\n"
	return append([]byte(head), bytes.Repeat([]byte("z"), size-len(head))...)
}

func TestReplyBounds(t *testing.T) {
	t.Run("a folder list past the command bound", func(t *testing.T) {
		double := newDouble(t, imaptest.ImplicitTLS)
		mail := threeInInbox()
		addMessages(t, double, mail)
		double.FloodList("EXAMINE", 128<<10)
		f := newFixture(t, double)
		cfg := testConfig
		cfg.commandBytes = 64 << 10
		collector := f.startWith(t, cfg)

		id, category := addFailing(t, f, collector, sourceFor(t, double, "tls", "INBOX"))

		if category != ServerError.String() {
			t.Errorf("the sync failed with %q, want server_error", category)
		}
		if got := f.listed(t); len(got) != 0 {
			t.Errorf("listed %v, want nothing", got)
		}
		if uidValidity, lastUID := f.checkpoint(t, f.folderIDs(t, id)["INBOX"]); uidValidity.Valid || lastUID != 0 {
			t.Errorf("checkpoint = %v %d, want none", uidValidity, lastUID)
		}
	})

	t.Run("a body past the body bound", func(t *testing.T) {
		double := newDouble(t, imaptest.StartTLS)
		// The first is over the command bound and under the body bound, the
		// second over both.
		mail := []fixtureMessage{
			{folder: "INBOX", body: sizedBody("Under", 100<<10), received: time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC)},
			{folder: "INBOX", body: sizedBody("Over", 256<<10), received: time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)},
		}
		addMessages(t, double, mail)
		f := newFixture(t, double)
		cfg := testConfig
		cfg.commandBytes = 64 << 10
		cfg.bodyBytes = 192 << 10
		collector := f.startWith(t, cfg)

		id, category := addFailing(t, f, collector, sourceFor(t, double, "starttls", "INBOX"))

		if category != ServerError.String() {
			t.Errorf("the sync failed with %q, want server_error", category)
		}
		if got, want := f.listed(t), hashesOf(mail[0].body); !slices.Equal(got, want) {
			t.Errorf("listed %v, want the first message alone", got)
		}
		if temp := f.tempFiles(t); len(temp) != 0 {
			t.Errorf("tmp/ holds %v", temp)
		}
		if _, lastUID := f.checkpoint(t, f.folderIDs(t, id)["INBOX"]); lastUID != int64(mail[0].uid) {
			t.Errorf("last_uid = %d, want %d", lastUID, mail[0].uid)
		}
	})

	// The client reads what the server sends between commands at once, so
	// those bytes count toward the next command, which then goes past the
	// bound. Without them the same command stays under it.
	for _, between := range []bool{false, true} {
		name := "a reply under the bound"
		if between {
			name = "bytes between commands"
		}
		t.Run(name, func(t *testing.T) {
			cfg := testConfig
			cfg.commandBytes = 1000
			// Once the test has seen the first NOOP's answer, the server
			// sends filler and an EXPUNGE, whose arrival the client reports.
			sendFiller := make(chan bool, 1)
			addr := script(t, func(conn net.Conn, lines *bufio.Reader) {
				io.WriteString(conn, "* OK [CAPABILITY IMAP4rev1] ready\r\n")
				tag := readTag(t, lines)
				io.WriteString(conn, tag+" OK NOOP completed\r\n")
				if <-sendFiller {
					io.WriteString(conn, "* OK "+strings.Repeat("x", 800)+"\r\n* 1 EXPUNGE\r\n")
				}
				tag = readTag(t, lines)
				io.WriteString(conn, "* OK "+strings.Repeat("y", 300)+"\r\n"+tag+" OK NOOP completed\r\n")
				silent(conn, lines)
			})
			expunged := make(chan struct{}, 1)
			client, wrapped := plainClientWith(t, addr, cfg, &imapclient.Options{
				UnilateralDataHandler: &imapclient.UnilateralDataHandler{
					Expunge: func(uint32) { expunged <- struct{}{} },
				},
			})

			if err := wrapped.command(func() error { return client.Noop().Wait() }); err != nil {
				t.Fatalf("first NOOP: %v", err)
			}
			sendFiller <- between
			if between {
				<-expunged
			}
			err := wrapped.command(func() error { return client.Noop().Wait() })

			if !between {
				if err != nil {
					t.Fatalf("second NOOP: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatal("the second NOOP succeeded past the bound")
			}
			if wrapped.timedOut() {
				t.Error("the wrapper recorded a timeout")
			}
			if !errors.Is(wrapped.stickyErr(), errReplyTooLarge) {
				t.Errorf("the connection failed with %v, want the reply bound", wrapped.stickyErr())
			}
		})
	}
}
