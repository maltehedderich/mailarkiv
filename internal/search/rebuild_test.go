package search

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// The words each fixture's subject holds.
var fixtureWords = map[string]string{
	"plain-text.eml": "wochenplan",
	"html-only.eml":  "bericht",
	"attachment.eml": "vertrag",
}

// rebuildFixture imports and processes the three fixtures. The module sends
// one message per batch and isn't running yet.
func rebuildFixture(t *testing.T, program string) *fixture {
	t.Helper()
	f := newFixture(t, program)
	f.module.batchMessages = 1
	for name := range fixtureWords {
		f.importFixture(name)
	}
	f.runProcessing()
	f.waitForProcessed(3)
	return f
}

// results searches for each fixture's word.
func (f *fixture) results() map[string][]int64 {
	f.t.Helper()
	out := map[string][]int64{}
	for _, words := range fixtureWords {
		out[words] = f.ids(words)
	}
	return out
}

type rebuildRow struct {
	startedBy int64
	startedAt int64
	endedAt   sql.NullInt64
	outcome   sql.NullString
}

func (f *fixture) rebuildRow(id int64) rebuildRow {
	f.t.Helper()
	var row rebuildRow
	err := f.db.Reader.QueryRow("SELECT started_by, started_at, ended_at, outcome FROM search_rebuild WHERE id = ?", id).
		Scan(&row.startedBy, &row.startedAt, &row.endedAt, &row.outcome)
	if err != nil {
		f.t.Fatal(err)
	}
	return row
}

// rebuildRunning reads whether search_rebuild has a running rebuild. It runs
// in the module's goroutines, so it can't fail the test.
func (f *fixture) rebuildRunning() bool {
	var running bool
	err := f.db.Reader.QueryRow("SELECT EXISTS (SELECT 1 FROM search_rebuild WHERE outcome IS NULL)").Scan(&running)
	return err == nil && running
}

func (f *fixture) indexDirExists(id int64) bool {
	_, err := os.Stat(filepath.Join(f.dataDir, "index", itoa64(id)))
	return err == nil
}

func TestRebuild(t *testing.T) {
	f := rebuildFixture(t, searchProgram(t))
	account := f.account()
	f.module.commitMessages = 1
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	before := f.results()
	old := f.activeID()

	// The rebuild waits after its first batch until released.
	release := make(chan struct{})
	held := make(chan struct{}, 1)
	f.module.batchSent = func(b sentBatch) {
		if b.generation != old {
			select {
			case held <- struct{}{}:
				<-release
			default:
			}
		}
	}
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})

	f.advance(time.Minute)
	rebuild, err := f.module.StartRebuild(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	if rebuild.ID == 0 || rebuild.StartedBy != "admin@example.test" || !rebuild.StartedAt.Equal(f.now()) {
		t.Errorf("rebuild %+v, want started by admin@example.test now", rebuild)
	}
	row := f.rebuildRow(rebuild.ID)
	if row.startedBy != account || row.startedAt != f.now().UnixMilli() || row.endedAt.Valid || row.outcome.Valid {
		t.Errorf("search_rebuild %+v, want a running rebuild", row)
	}
	generations := f.generations()
	if len(generations) != 2 || generations[0] != (generationRow{old, roleActive, 3}) || generations[1].role != roleBuilding {
		t.Fatalf("generations %+v, want the active one and a building one", generations)
	}
	building := generations[1].id
	var rebuildID int64
	if err := f.db.Reader.QueryRow("SELECT rebuild_id FROM search_generation WHERE id = ?", building).Scan(&rebuildID); err != nil || rebuildID != rebuild.ID {
		t.Errorf("building generation's rebuild %d, %v; want %d", rebuildID, err, rebuild.ID)
	}
	if got := f.logged("rebuild started"); len(got) != 1 || got[0]["rebuild_id"] != float64(rebuild.ID) || got[0]["generation_id"] != float64(building) {
		t.Errorf("log %v, want rebuild started with its IDs", got)
	}

	select {
	case <-held:
	case <-time.After(30 * time.Second):
		t.Fatal("the rebuild sent no batch")
	}
	if progress, err := f.module.Progress(context.Background()); err != nil || progress != (Progress{Done: 1, ToDo: 3}) {
		t.Errorf("progress %+v, %v; want 1 of 3", progress, err)
	}
	if state := f.module.State(); state != (State{Search: SearchAvailable, Indexing: IndexingRebuilding}) {
		t.Errorf("state %+v, want available and rebuilding", state)
	}
	if got := f.results(); !mapsEqual(got, before) {
		t.Errorf("during the rebuild searches found %v, want %v", got, before)
	}
	// A message captured during the rebuild.
	added := f.importNote(1)
	f.waitForProcessed(4)
	if progress, err := f.module.Progress(context.Background()); err != nil || progress != (Progress{Done: 1, ToDo: 4}) {
		t.Errorf("progress %+v, %v; want 1 of 4", progress, err)
	}

	close(release)
	f.waitFor("rebuild finished", func() bool { return len(f.logged("rebuild finished")) == 1 })
	f.waitForIndexing(IndexingUpToDate)
	if got := f.generations(); len(got) != 1 || got[0] != (generationRow{building, roleActive, 4}) {
		t.Errorf("generations %+v, want only generation %d, active at 4", got, building)
	}
	row = f.rebuildRow(rebuild.ID)
	if row.outcome.String != "finished" || row.endedAt.Int64 != f.now().UnixMilli() {
		t.Errorf("search_rebuild %+v, want finished now", row)
	}
	entry := f.logged("rebuild finished")[0]
	if entry["rebuild_id"] != float64(rebuild.ID) || entry["generation_id"] != float64(building) || entry["messages"] != float64(4) {
		t.Errorf("log %v, want rebuild finished with its IDs and 4 messages", entry)
	}
	f.waitFor("the old index's directory to go", func() bool { return !f.indexDirExists(old) })
	if !f.indexDirExists(building) {
		t.Error("the new index's directory is gone")
	}
	if got := f.results(); !mapsEqual(got, before) {
		t.Errorf("after the rebuild searches found %v, want %v", got, before)
	}
	if got := f.ids("notiz1"); !slices.Equal(got, []int64{added}) {
		t.Errorf("the message captured during the rebuild: found %v, want %d", got, added)
	}
}

func mapsEqual(a, b map[string][]int64) bool {
	if len(a) != len(b) {
		return false
	}
	for key, value := range a {
		if !slices.Equal(value, b[key]) {
			return false
		}
	}
	return true
}

func TestRebuildRefusals(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	account := f.account()
	disk := f.fakeDisk(1 * gib)
	f.module.diskCheckEvery = 20 * time.Millisecond
	rebuilds := func() int {
		var count int
		if err := f.db.Reader.QueryRow("SELECT count(*) FROM search_rebuild").Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}

	_, err := f.module.StartRebuild(context.Background(), account)
	if _, ok := errors.AsType[*UnavailableError](err); !ok {
		t.Errorf("with no search process: %v, want UnavailableError", err)
	}

	// Low disk space holds the first build, so no index is active.
	f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)
	f.runSearch()
	f.waitForIndexing(IndexingPaused)
	if _, err := f.module.StartRebuild(context.Background(), account); !errors.Is(err, ErrNoIndex) {
		t.Errorf("during the first build: %v, want ErrNoIndex", err)
	}

	disk.set(3 * gib)
	f.waitForIndexing(IndexingUpToDate)
	needed := 2*gib + f.indexSize(f.activeID())
	disk.set(needed)
	_, err = f.module.StartRebuild(context.Background(), account)
	space, ok := errors.AsType[*NotEnoughSpaceError](err)
	if !ok || space.Needed != needed || space.Free != needed {
		t.Errorf("with free space equal to the need: %v, want NotEnoughSpaceError %d of %d", err, needed, needed)
	}
	if got := rebuilds(); got != 0 {
		t.Fatalf("%d rebuilds written by refusals", got)
	}

	// Free space allows the rebuild, and once it runs pauses it.
	disk.setRule(func() int64 {
		if f.rebuildRunning() {
			return 1 * gib
		}
		return needed + 1
	})
	if _, err := f.module.StartRebuild(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	if _, err := f.module.StartRebuild(context.Background(), account); !errors.Is(err, ErrRebuildRunning) {
		t.Errorf("with a rebuild running: %v, want ErrRebuildRunning", err)
	}
	if got := rebuilds(); got != 1 {
		t.Errorf("%d rebuilds, want 1", got)
	}
}

func TestRebuildFails(t *testing.T) {
	program := faultsSearchProgram(t)
	f := newFixture(t, program)
	f.fastRestarts()
	f.fastRetries()
	setFaults := f.switchableFaults(program)
	account := f.account()
	id := indexedOnce(f)
	old := f.activeID()

	// Every commit now answers write_failed. The active index has nothing to
	// commit.
	setFaults("after-commit=fail")
	f.killSearch()
	f.waitForIndexing(IndexingUpToDate)
	rebuild, err := f.module.StartRebuild(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	f.waitFor("rebuild started", func() bool { return len(f.logged("rebuild started")) == 1 })
	building := int64(f.logged("rebuild started")[0]["generation_id"].(float64))
	f.waitFor("rebuild_failed", func() bool { return len(f.failures(opstatus.RebuildFailed)) == 1 })
	event := f.failures(opstatus.RebuildFailed)[0]
	if event.Detail == nil || event.Detail.RebuildID == nil || *event.Detail.RebuildID != rebuild.ID ||
		event.Detail.GenerationID == nil || *event.Detail.GenerationID != building {
		t.Errorf("detail %+v, want rebuild %d and generation %d", event.Detail, rebuild.ID, building)
	}
	if got := len(f.failures(opstatus.IndexWriteFailed)); got != 5 {
		t.Errorf("%d failed batches, want 5", got)
	}
	if row := f.rebuildRow(rebuild.ID); row.outcome.String != "failed" || !row.endedAt.Valid {
		t.Errorf("search_rebuild %+v, want failed", row)
	}
	if got := f.generations(); len(got) != 1 || got[0].id != old || got[0].role != roleActive {
		t.Errorf("generations %+v, want only the old active one", got)
	}
	f.waitFor("the new index's directory to go", func() bool { return !f.indexDirExists(building) })
	f.waitForIndexing(IndexingUpToDate)
	if got := f.ids("wochenplan"); !slices.Equal(got, []int64{id}) {
		t.Errorf("the old index found %v, want %d", got, id)
	}
}

func TestRebuildSurvivesLowDiskAndRestarts(t *testing.T) {
	program := faultsSearchProgram(t)
	f := newFixture(t, program)
	f.fastRestarts()
	f.fastRetries()
	disk := f.fakeDisk(3 * gib)
	f.module.diskCheckEvery = 20 * time.Millisecond
	// Each start moves the clock 11 minutes, so the crashes never reach 5
	// in 30 minutes. While crashing is above 0, a start ends the process
	// at its first commit.
	var crashing atomic.Int32
	f.module.command = func() *exec.Cmd {
		f.advance(11 * time.Minute)
		cmd := exec.Command(program)
		cmd.Env = []string{}
		if crashing.Load() > 0 {
			crashing.Add(-1)
			cmd.Env = append(cmd.Env, faultsVariable+"=after-commit=exit")
		}
		return cmd
	}
	account := f.account()
	id := indexedOnce(f)

	disk.setRule(func() int64 {
		if f.rebuildRunning() {
			return 1 * gib
		}
		return 3 * gib
	})
	rebuild, err := f.module.StartRebuild(context.Background(), account)
	if err != nil {
		t.Fatal(err)
	}
	f.waitForIndexing(IndexingPaused)
	f.waitForReadings(disk, 10)

	// Five starts that each end at the rebuild's commit, after this kill.
	crashing.Store(5)
	f.killSearch()
	disk.set(3 * gib)
	f.waitFor("rebuild finished", func() bool { return len(f.logged("rebuild finished")) == 1 })

	if got := len(f.failures(opstatus.SearchProcessStopped)); got != 6 {
		t.Errorf("%d stops, want 6", got)
	}
	if got := f.failures(opstatus.RebuildFailed); len(got) != 0 {
		t.Errorf("rebuild_failed events %+v, want none", got)
	}
	if row := f.rebuildRow(rebuild.ID); row.outcome.String != "finished" {
		t.Errorf("search_rebuild %+v, want finished", row)
	}
	if got := f.needsRebuild(); got != "" {
		t.Errorf("marked %q", got)
	}
	f.waitForIndexing(IndexingUpToDate)
	if got := f.ids("wochenplan"); !slices.Equal(got, []int64{id}) {
		t.Errorf("the new index found %v, want %d", got, id)
	}
}

func TestSwapWithoutGap(t *testing.T) {
	f := rebuildFixture(t, searchProgram(t))
	account := f.account()
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	want := f.results()

	var mu sync.Mutex
	var problems []string
	var searches atomic.Int64
	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			select {
			case <-stop:
				return
			default:
			}
			for words, ids := range want {
				result, err := f.module.Search(context.Background(), Query{Words: words, Page: 1})
				mu.Lock()
				switch {
				case err != nil:
					problems = append(problems, err.Error())
				case len(result.Rows) != len(ids):
					problems = append(problems, words+" found a different set")
				default:
					for i, row := range result.Rows {
						if row.ID != ids[i] {
							problems = append(problems, words+" found a different set")
							break
						}
					}
				}
				mu.Unlock()
				searches.Add(1)
			}
		}
	}()

	if _, err := f.module.StartRebuild(context.Background(), account); err != nil {
		t.Fatal(err)
	}
	f.waitFor("rebuild finished", func() bool { return len(f.logged("rebuild finished")) == 1 })
	after := searches.Load()
	f.waitFor("searches after the swap", func() bool { return searches.Load() > after+30 })
	close(stop)
	<-done

	mu.Lock()
	defer mu.Unlock()
	if len(problems) > 0 {
		t.Errorf("%d of %d searches went wrong, first: %s", len(problems), searches.Load(), problems[0])
	}
}
