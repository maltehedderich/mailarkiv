package collect

import (
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/imaptest"
)

// A source whose server stalls doesn't hold up another source.
func TestSourcesIndependent(t *testing.T) {
	stalled := newDouble(t, imaptest.ImplicitTLS)
	healthy := newDoubleFor(t, imaptest.ImplicitTLS, "ben@example.test")
	mail := threeInInbox()
	addMessages(t, healthy, mail)
	reached := make(chan struct{}, 1)
	stalled.SetHook(func(command imaptest.Command) error {
		if command.Name == "EXAMINE" {
			select {
			case reached <- struct{}{}:
			default:
			}
			return imaptest.ErrStall
		}
		return nil
	})
	f := newFixture(t, stalled, healthy)
	// The stall would end at the idle limit, so it is long here.
	collector := f.startWith(t, shortLimits(2*time.Second, time.Minute))

	added, err := collector.Add(t.Context(), sourceFor(t, stalled, "tls", "INBOX"))
	if err != nil {
		t.Fatal(err)
	}
	<-reached
	other := sourceFor(t, healthy, "tls", "INBOX")
	other.Name, other.UserName = "Ben", "ben@example.test"
	addSource(t, f, collector, other)

	if state := f.get(t, collector, added.ID).State; state != Syncing {
		t.Errorf("the stalled source is %v, want still syncing", state)
	}
	if got, want := f.listed(t), hashesOf(mail[0].body, mail[1].body, mail[2].body); !slices.Equal(got, want) {
		t.Errorf("listed %v, want the healthy source's three messages", got)
	}
}
