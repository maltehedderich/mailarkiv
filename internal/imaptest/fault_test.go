package imaptest_test

import (
	"bytes"
	"io"
	"maps"
	"slices"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

var faultBodies = [][]byte{
	[]byte("From: ben@example.test\r\nSubject: One\r\n\r\nOne\r\n"),
	[]byte("From: ben@example.test\r\nSubject: Two\r\n\r\nTwo\r\n"),
	[]byte("From: ben@example.test\r\nSubject: Three\r\n\r\nThree\r\n"),
}

var faultReceived = time.Date(2026, 11, 2, 9, 30, 15, 0, time.UTC)

// withThreeMessages adds faultBodies to INBOX as UIDs 1 to 3.
func withThreeMessages(t *testing.T, server *imaptest.Server) {
	t.Helper()
	for i, body := range faultBodies {
		uid, err := server.AddMessage("INBOX", body, faultReceived.Add(time.Duration(i)*time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if uid != imap.UID(i+1) {
			t.Fatalf("UID = %d, want %d", uid, i+1)
		}
	}
}

func examined(t *testing.T, server *imaptest.Server, folder string) *imapclient.Client {
	t.Helper()
	client := signIn(t, server, imaptest.ImplicitTLS)
	if _, err := client.Select(folder, &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}
	return client
}

var (
	bodyPeek = &imap.FetchOptions{UID: true, BodySection: []*imap.FetchItemBodySection{{Peek: true}}}
	metadata = &imap.FetchOptions{UID: true, RFC822Size: true, InternalDate: true}
)

func byUID(messages []*imapclient.FetchMessageBuffer) map[imap.UID]*imapclient.FetchMessageBuffer {
	found := map[imap.UID]*imapclient.FetchMessageBuffer{}
	for _, message := range messages {
		found[message.UID] = message
	}
	return found
}

func TestFaults(t *testing.T) {
	tests := []struct {
		name  string
		fault imaptest.Fault
		// body is BODY[] for UID 2, or nil for NIL. found is false when UID 2
		// returns no FETCH response.
		body  []byte
		found bool
		size  int64
		dated bool
	}{
		{"none", imaptest.Fault{}, faultBodies[1], true, int64(len(faultBodies[1])), true},
		{"NIL body", imaptest.Fault{Body: imaptest.BodyNil}, nil, true, int64(len(faultBodies[1])), true},
		{"no data", imaptest.Fault{Body: imaptest.BodyMissing}, nil, false, int64(len(faultBodies[1])), true},
		{"empty body", imaptest.Fault{Body: imaptest.BodyEmpty}, []byte{}, true, int64(len(faultBodies[1])), true},
		{"other size", imaptest.Fault{Size: 150_000_001}, faultBodies[1], true, 150_000_001, true},
		{"no INTERNALDATE", imaptest.Fault{NoInternalDate: true}, faultBodies[1], true, int64(len(faultBodies[1])), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := newDouble(t, imaptest.ImplicitTLS)
			withThreeMessages(t, server)
			if err := server.SetFault("INBOX", 2, tt.fault); err != nil {
				t.Fatal(err)
			}
			client := examined(t, server, "INBOX")

			fetched, err := client.Fetch(imap.UIDSetNum(1, 2, 3), metadata).Collect()
			if err != nil {
				t.Fatal(err)
			}
			messages := byUID(fetched)
			if got := slices.Sorted(maps.Keys(messages)); !slices.Equal(got, []imap.UID{1, 2, 3}) {
				t.Fatalf("metadata for UIDs %v, want 1 to 3", got)
			}
			for uid, message := range messages {
				size, dated := int64(len(faultBodies[uid-1])), true
				if uid == 2 {
					size, dated = tt.size, tt.dated
				}
				if message.RFC822Size != size {
					t.Errorf("UID %d RFC822.SIZE = %d, want %d", uid, message.RFC822Size, size)
				}
				want := faultReceived.Add(time.Duration(uid-1) * time.Hour)
				if dated && !message.InternalDate.Equal(want) {
					t.Errorf("UID %d INTERNALDATE = %v, want %v", uid, message.InternalDate, want)
				}
				if !dated && !message.InternalDate.IsZero() {
					t.Errorf("UID %d INTERNALDATE = %v, want none", uid, message.InternalDate)
				}
			}

			fetched, err = client.Fetch(imap.UIDSetNum(1, 2, 3), bodyPeek).Collect()
			if err != nil {
				t.Fatal(err)
			}
			messages = byUID(fetched)
			for uid, want := range map[imap.UID][]byte{1: faultBodies[0], 3: faultBodies[2]} {
				if got := messages[uid].FindBodySection(&imap.FetchItemBodySection{Peek: true}); !bytes.Equal(got, want) {
					t.Errorf("UID %d body = %q, want %q", uid, got, want)
				}
			}
			message, found := messages[2]
			if found != tt.found {
				t.Fatalf("UID 2 has a FETCH response: %t, want %t", found, tt.found)
			}
			if !found {
				return
			}
			got := message.FindBodySection(&imap.FetchItemBodySection{Peek: true})
			if !bytes.Equal(got, tt.body) || (got == nil) != (tt.body == nil) {
				t.Errorf("UID 2 body = %q (nil: %t), want %q (nil: %t)", got, got == nil, tt.body, tt.body == nil)
			}
		})
	}
}

func TestCutLiteral(t *testing.T) {
	server := newDouble(t, imaptest.StartTLS)
	withThreeMessages(t, server)
	announced := int64(len(faultBodies[1]) + 10)
	if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyCut, Announced: announced}); err != nil {
		t.Fatal(err)
	}
	client := signIn(t, server, imaptest.StartTLS)
	if _, err := client.Select("INBOX", &imap.SelectOptions{ReadOnly: true}).Wait(); err != nil {
		t.Fatal(err)
	}

	fetch := client.Fetch(imap.UIDSetNum(2), bodyPeek)
	message := fetch.Next()
	if message == nil {
		t.Fatalf("no FETCH response: %v", fetch.Close())
	}
	var literal imap.LiteralReader
	for item := message.Next(); item != nil; item = message.Next() {
		if section, ok := item.(imapclient.FetchItemDataBodySection); ok {
			literal = section.Literal
			break
		}
	}
	if literal == nil {
		t.Fatal("no literal")
	}
	if literal.Size() != announced {
		t.Errorf("announced %d bytes, want %d", literal.Size(), announced)
	}
	// The client reports the end of the connection as the literal's end, so
	// only the count tells a cut literal apart.
	got, err := io.ReadAll(literal)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, faultBodies[1]) {
		t.Errorf("read %q, want %q", got, faultBodies[1])
	}
	// Discarding the rest of a cut literal would race with the client's
	// reader, so the client closes first.
	if err := client.Close(); err == nil {
		t.Error("the client closed cleanly after a cut literal")
	}
	if err := fetch.Close(); err == nil {
		t.Error("the FETCH succeeded")
	}
}

func TestSetFaultRefuses(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	withThreeMessages(t, server)
	if err := server.SetFault("INBOX", 4, imaptest.Fault{Body: imaptest.BodyNil}); err == nil {
		t.Error("SetFault accepted a missing UID")
	}
	short := imaptest.Fault{Body: imaptest.BodyCut, Announced: int64(len(faultBodies[1]))}
	if err := server.SetFault("INBOX", 2, short); err == nil {
		t.Error("SetFault accepted a cut literal no longer than the message")
	}

	if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	client := examined(t, server, "INBOX")
	if _, err := client.Fetch(imap.UIDSetNum(2), &imap.FetchOptions{UID: true, Flags: true}).Collect(); err == nil {
		t.Error("a faulted message served FLAGS")
	}

	if err := server.SetFault("INBOX", 2, imaptest.Fault{}); err != nil {
		t.Fatal(err)
	}
	fetched, err := client.Fetch(imap.UIDSetNum(2), bodyPeek).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if len(fetched) != 1 || !bytes.Equal(fetched[0].FindBodySection(&imap.FetchItemBodySection{Peek: true}), faultBodies[1]) {
		t.Error("the zero Fault didn't remove the fault")
	}
}

func TestBodyFetches(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	withThreeMessages(t, server)
	if err := server.SetFault("INBOX", 3, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	client := examined(t, server, "INBOX")

	for _, uids := range [][]imap.UID{{1}, {1, 3}, {3}} {
		if _, err := client.Fetch(imap.UIDSetNum(uids...), bodyPeek).Collect(); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := client.Fetch(imap.UIDSetNum(1, 2, 3), metadata).Collect(); err != nil {
		t.Fatal(err)
	}
	for uid, want := range map[imap.UID]int{1: 2, 2: 0, 3: 2} {
		if got := server.BodyFetches("INBOX", uid); got != want {
			t.Errorf("BodyFetches(INBOX, %d) = %d, want %d", uid, got, want)
		}
	}
}

func TestHoldBody(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	withThreeMessages(t, server)
	hold := server.HoldBody("INBOX", 2)
	client := examined(t, server, "INBOX")

	// A metadata fetch passes the hold.
	if _, err := client.Fetch(imap.UIDSetNum(2), metadata).Collect(); err != nil {
		t.Fatal(err)
	}

	done := make(chan []*imapclient.FetchMessageBuffer)
	go func() {
		fetched, err := client.Fetch(imap.UIDSetNum(2), bodyPeek).Collect()
		if err != nil {
			t.Error(err)
		}
		done <- fetched
	}()
	<-hold.Reached()
	select {
	case <-done:
		t.Fatal("the held fetch finished before Release")
	default:
	}
	hold.Release()
	fetched := <-done
	if len(fetched) != 1 || !bytes.Equal(fetched[0].FindBodySection(&imap.FetchItemBodySection{Peek: true}), faultBodies[1]) {
		t.Errorf("the released fetch returned %v", fetched)
	}

	// Release lets later fetches through too.
	if _, err := client.Fetch(imap.UIDSetNum(2), bodyPeek).Collect(); err != nil {
		t.Fatal(err)
	}
}

func TestCloseEndsAHeldFetch(t *testing.T) {
	server, err := imaptest.New(imaptest.Options{Security: imaptest.ImplicitTLS, User: user, Password: password})
	if err != nil {
		t.Fatal(err)
	}
	withThreeMessages(t, server)
	hold := server.HoldBody("INBOX", 1)
	client := examined(t, server, "INBOX")

	failed := make(chan error)
	go func() {
		_, err := client.Fetch(imap.UIDSetNum(1), bodyPeek).Collect()
		failed <- err
	}()
	<-hold.Reached()
	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-failed; err == nil {
		t.Error("the held fetch succeeded after Close")
	}
}

func TestRenumber(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	withThreeMessages(t, server)
	if err := server.SetFault("INBOX", 1, imaptest.Fault{NoInternalDate: true}); err != nil {
		t.Fatal(err)
	}
	before, err := server.UIDValidity("INBOX")
	if err != nil {
		t.Fatal(err)
	}

	renumbered, err := server.Renumber("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if want := map[imap.UID]imap.UID{1: 3, 2: 2, 3: 1}; !maps.Equal(renumbered, want) {
		t.Errorf("Renumber = %v, want %v", renumbered, want)
	}
	after, err := server.UIDValidity("INBOX")
	if err != nil {
		t.Fatal(err)
	}
	if after == before {
		t.Errorf("UIDVALIDITY stayed %d", after)
	}

	client := examined(t, server, "INBOX")
	fetched, err := client.Fetch(imap.UIDSetNum(1, 2, 3), &imap.FetchOptions{
		UID:          true,
		InternalDate: true,
		BodySection:  []*imap.FetchItemBodySection{{Peek: true}},
	}).Collect()
	if err != nil {
		t.Fatal(err)
	}
	messages := byUID(fetched)
	for old, uid := range renumbered {
		message := messages[uid]
		if message == nil {
			t.Fatalf("no message at new UID %d", uid)
		}
		if got := message.FindBodySection(&imap.FetchItemBodySection{Peek: true}); !bytes.Equal(got, faultBodies[old-1]) {
			t.Errorf("new UID %d body = %q, want %q", uid, got, faultBodies[old-1])
		}
		// The fault on old UID 1 moved to its new UID.
		want := faultReceived.Add(time.Duration(old-1) * time.Hour)
		if old == 1 {
			want = time.Time{}
		}
		if !message.InternalDate.Equal(want) {
			t.Errorf("new UID %d INTERNALDATE = %v, want %v", uid, message.InternalDate, want)
		}
	}

	t.Run("refuses messages AddMessage didn't add", func(t *testing.T) {
		body := []byte("A: b\r\n\r\n")
		appended := client.Append("INBOX", int64(len(body)), nil)
		if _, err := appended.Write(body); err != nil {
			t.Fatal(err)
		}
		if err := appended.Close(); err != nil {
			t.Fatal(err)
		}
		if _, err := appended.Wait(); err != nil {
			t.Fatal(err)
		}
		if _, err := server.Renumber("INBOX"); err == nil {
			t.Error("Renumber accepted a folder with an appended message")
		}
	})
}

func TestDeleteMessageForgetsItsFault(t *testing.T) {
	server := newDouble(t, imaptest.ImplicitTLS)
	withThreeMessages(t, server)
	if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyNil}); err != nil {
		t.Fatal(err)
	}
	if err := server.DeleteMessage("INBOX", 2); err != nil {
		t.Fatal(err)
	}
	if err := server.SetFault("INBOX", 2, imaptest.Fault{Body: imaptest.BodyNil}); err == nil {
		t.Error("SetFault accepted a deleted message")
	}
	client := examined(t, server, "INBOX")
	fetched, err := client.Fetch(imap.UIDSetNum(1, 2, 3), bodyPeek).Collect()
	if err != nil {
		t.Fatal(err)
	}
	if got := slices.Sorted(maps.Keys(byUID(fetched))); !slices.Equal(got, []imap.UID{1, 3}) {
		t.Errorf("fetched UIDs %v, want 1 and 3", got)
	}
}
