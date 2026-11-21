package search

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// indexedOnce starts processing and the search module, with one message
// indexed, and returns its ID.
func indexedOnce(f *fixture) int64 {
	f.t.Helper()
	id := f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	return id
}

func TestNeedsRebuildAfterCrashes(t *testing.T) {
	t.Run("five crashes within 30 minutes", func(t *testing.T) {
		f := newFixture(t, searchProgram(t))
		f.fastRestarts()
		first := indexedOnce(f)

		// Crashes at 0, 7, 14, 21, and 28 minutes.
		for i := range 5 {
			if i > 0 {
				f.advance(7 * time.Minute)
			}
			if got := f.needsRebuild(); got != "" {
				t.Fatalf("marked %q after %d crashes", got, i)
			}
			f.killSearch()
		}
		if got := f.needsRebuild(); got != "search_process_stopped" {
			t.Fatalf("search_needs_rebuild holds %q, want search_process_stopped", got)
		}
		var since int64
		if err := f.db.Reader.QueryRow("SELECT since FROM search_needs_rebuild").Scan(&since); err != nil {
			t.Fatal(err)
		}
		if want := f.now().UnixMilli(); since != want {
			t.Errorf("since %d, want the fifth crash at %d", since, want)
		}
		events := f.failures(opstatus.IndexNeedsRebuild)
		if len(events) != 1 || events[0].Detail == nil || events[0].Detail.Cause != opstatus.CauseSearchProcessStopped {
			t.Errorf("index_needs_rebuild events %+v, want one with cause search_process_stopped", events)
		}

		// The process starts again, but nothing is indexed.
		f.waitFor("search needs rebuild with indexing stopped", func() bool {
			return f.module.pid() != 0 && f.module.State() == State{Search: SearchNeedsRebuild, Indexing: IndexingStopped}
		})
		f.importFixture("html-only.eml")
		f.waitForProcessed(2)
		result := f.search("wochenplan")
		if !result.MayBeIncomplete || len(result.Rows) != 1 || result.Rows[0].ID != first {
			t.Errorf("search gave %+v, want the first message and may be incomplete", result)
		}
		if result.NotIncluded != 1 {
			t.Errorf("not included %d, want the message processed after the mark", result.NotIncluded)
		}
		if got := f.ids("bericht"); len(got) != 0 {
			t.Errorf("search found %v, want nothing indexed after the mark", got)
		}
	})

	t.Run("a fifth crash 31 minutes after the first", func(t *testing.T) {
		f := newFixture(t, searchProgram(t))
		f.fastRestarts()
		indexedOnce(f)

		// Crashes at 0, 1, 2, 3, and 31 minutes.
		for _, at := range []time.Duration{0, time.Minute, time.Minute, time.Minute, 28 * time.Minute} {
			f.advance(at)
			f.killSearch()
		}
		f.waitForIndexing(IndexingUpToDate)
		if got := f.needsRebuild(); got != "" {
			t.Errorf("marked %q", got)
		}
		if events := f.failures(opstatus.IndexNeedsRebuild); len(events) != 0 {
			t.Errorf("index_needs_rebuild events %+v, want none", events)
		}
		if result := f.search("wochenplan"); result.MayBeIncomplete {
			t.Error("search may be incomplete without a mark")
		}
	})
}

func TestNeedsRebuildAfterMaintenanceFailures(t *testing.T) {
	f := newFixture(t, faultsSearchProgram(t))
	f.fastRestarts()
	setFaults := f.switchableFaults(faultsSearchProgram(t))
	setFaults("during-merge=fail")
	f.module.maintainEvery = 1
	runs := f.maintenanceRuns()
	f.runProcessing()
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	generation := f.activeID()

	failedRuns := func(n int) {
		t.Helper()
		f.waitFor("failed maintenance runs", func() bool { return len(f.failures(opstatus.IndexMaintenanceFailed)) == n })
	}
	f.importNote(1)
	failedRuns(1)
	f.importNote(2)
	failedRuns(2)

	// A run that succeeds resets the count.
	setFaults("")
	f.killSearch()
	f.importNote(3)
	f.waitFor("a run that succeeds", func() bool {
		return slices.ContainsFunc(runs(), func(run maintenanceRun) bool { return run.answer.Failed == 0 })
	})
	setFaults("during-merge=fail")
	f.killSearch()
	f.importNote(4)
	failedRuns(3)
	f.importNote(5)
	failedRuns(4)
	if got := f.needsRebuild(); got != "" {
		t.Fatalf("marked %q after two failed runs in a row", got)
	}
	f.importNote(6)
	failedRuns(5)
	f.waitFor("the mark", func() bool { return f.needsRebuild() == "index_maintenance_failed" })

	for _, event := range f.failures(opstatus.IndexMaintenanceFailed) {
		if event.Detail == nil || event.Detail.GenerationID == nil || *event.Detail.GenerationID != generation {
			t.Errorf("event %+v, want generation %d", event, generation)
		}
	}
	events := f.failures(opstatus.IndexNeedsRebuild)
	if len(events) != 1 || events[0].Detail.Cause != opstatus.CauseIndexMaintenanceFailed {
		t.Errorf("index_needs_rebuild events %+v, want one with cause index_maintenance_failed", events)
	}
	f.waitForIndexing(IndexingStopped)
	if got := f.module.State().Search; got != SearchNeedsRebuild {
		t.Errorf("search is %s, want needs_rebuild", got)
	}
}

func TestMaintenanceValues(t *testing.T) {
	m := New(Config{})
	if m.maintainEvery != 50 || m.maintainAfter != 10*time.Minute {
		t.Errorf("maintenance after %d commits or %s, want 50 or 10 minutes", m.maintainEvery, m.maintainAfter)
	}

	f := newFixture(t, searchProgram(t))
	recorded := f.maintenanceRuns()
	f.runProcessing()
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	runs := func() int { return len(recorded()) }
	commit := func(n int) {
		t.Helper()
		f.importNote(n)
		f.waitFor("the commit", func() bool {
			rows := f.generations()
			return len(rows) == 1 && rows[0].cursor == int64(n)
		})
	}
	for n := 1; n <= 49; n++ {
		commit(n)
	}
	if got := runs(); got != 0 {
		t.Fatalf("%d maintenance runs after 49 commits, want none", got)
	}
	commit(50)
	f.waitFor("maintenance after 50 commits", func() bool { return runs() == 1 })

	// One commit, 10 minutes after the last run.
	f.advance(10 * time.Minute)
	commit(51)
	f.waitFor("maintenance after 10 minutes", func() bool { return runs() == 2 })
	if run := recorded()[1]; run.generation != f.activeID() || run.answer.Failed != 0 {
		t.Errorf("run %+v, want the active generation and no failed merge", run)
	}
}

func TestIndexDamaged(t *testing.T) {
	f := newFixture(t, faultsSearchProgram(t))
	f.fastRestarts()
	setFaults := f.switchableFaults(faultsSearchProgram(t))
	indexedOnce(f)

	setFaults("on-open=fail")
	f.killSearch()
	f.waitFor("the mark", func() bool { return f.needsRebuild() == "index_damaged" })
	f.waitFor("index_needs_rebuild", func() bool { return len(f.failures(opstatus.IndexNeedsRebuild)) == 1 })
	if event := f.failures(opstatus.IndexNeedsRebuild)[0]; event.Detail.Cause != opstatus.CauseIndexDamaged {
		t.Errorf("detail %+v, want cause index_damaged", event.Detail)
	}
	if state := f.module.State(); state != (State{Search: SearchNeedsRebuild, Indexing: IndexingStopped}) {
		t.Errorf("state %+v, want needs_rebuild and stopped", state)
	}
	// No index is open, so search can't answer.
	_, err := f.module.Search(context.Background(), Query{Words: "wochenplan", Page: 1})
	unavailable, ok := errors.AsType[*UnavailableError](err)
	if !ok || unavailable.Cause != opstatus.IndexNeedsRebuild {
		t.Errorf("search gave %v, want unavailable with index_needs_rebuild", err)
	}
}

func TestNeedsRebuildSurvivesRestart(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	account := f.account()
	id := f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)
	stop := f.startSearch()
	f.waitForIndexing(IndexingUpToDate)
	stop()

	if _, err := f.db.Writer.Exec(
		"INSERT INTO search_needs_rebuild (id, category, since) VALUES (1, 'index_maintenance_failed', ?)",
		f.now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	f.newModule()
	f.startSearch()
	f.waitFor("needs rebuild", func() bool {
		return f.module.pid() != 0 && f.module.State() == State{Search: SearchNeedsRebuild, Indexing: IndexingStopped}
	})
	if result := f.search("wochenplan"); !result.MayBeIncomplete || len(result.Rows) != 1 || result.Rows[0].ID != id {
		t.Errorf("search gave %+v, want the message and may be incomplete", result)
	}

	if _, err := f.module.StartRebuild(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	f.waitForIndexing(IndexingUpToDate)
	if got := f.needsRebuild(); got != "" {
		t.Errorf("search_needs_rebuild holds %q after the rebuild", got)
	}
	if state := f.module.State(); state.Search != SearchAvailable {
		t.Errorf("search is %s, want available", state.Search)
	}
	if result := f.search("wochenplan"); result.MayBeIncomplete || len(result.Rows) != 1 {
		t.Errorf("search gave %+v, want the message and not incomplete", result)
	}
}
