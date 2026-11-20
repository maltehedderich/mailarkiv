package search

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

func TestSearchCallLimit(t *testing.T) {
	t.Run("a search that hangs passes the call limit", func(t *testing.T) {
		f := searchingWithFault(t, "during-search=hang")
		started := time.Now()
		_, err := f.module.Search(context.Background(), Query{Words: "wochenplan", Page: 1})
		took := time.Since(started)
		if !errors.Is(err, ErrDidNotAnswer) {
			t.Fatalf("search gave %v, want ErrDidNotAnswer", err)
		}
		if took < 12*time.Second || took > 13*time.Second {
			t.Errorf("search took %s, want the 12 s limit", took)
		}
		events := f.failures(opstatus.SearchProcessDidNotAnswer)
		if len(events) != 1 || events[0].Detail == nil || events[0].Detail.Operation != opstatus.OperationSearch {
			t.Errorf("events %+v, want one with operation search", events)
		}
	})

	t.Run("a search stopped at its deadline took too long", func(t *testing.T) {
		f := searchingWithFault(t, "during-search=fail")
		_, err := f.module.Search(context.Background(), Query{Words: "wochenplan", Page: 1})
		if !errors.Is(err, ErrTookTooLong) {
			t.Fatalf("search gave %v, want ErrTookTooLong", err)
		}
		if events := f.failures(opstatus.SearchProcessDidNotAnswer); len(events) != 0 {
			t.Errorf("events %+v, want none", events)
		}
	})
}

// searchingWithFault runs the search program's fault build with faults, and
// one message indexed.
func searchingWithFault(t *testing.T, faults string) *fixture {
	t.Helper()
	t.Setenv(faultsVariable, faults)
	f := newFixture(t, faultsSearchProgram(t))
	f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	return f
}
