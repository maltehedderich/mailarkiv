package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"
)

// crashSetup is how a crash matrix row's data directory gets to the point
// where the faults binary starts.
type crashSetup int

const (
	// unprocessed leaves the three messages for processing.
	unprocessed crashSetup = iota
	// processedOnly has them processed and no index yet.
	processedOnly
	// indexed has them in an active index, and the faults binary starts a
	// rebuild.
	indexed
)

// crashRow is one stop point. before checks the catalog and the index
// directories after the faults binary died, and after checks them once the
// default binary has indexed every message. Both get what before kept.
type crashRow struct {
	point  string
	setup  crashSetup
	before func(t *testing.T, dataDir string, c indexCatalog) (kept any)
	after  func(t *testing.T, dataDir string, server *serverProcess, kept any)
}

// rebuildKept is what a rebuild row remembers from before the restart.
type rebuildKept struct {
	old, building int64
	segments      []indexSegment
}

// assertRebuilding checks that the rebuild runs, the old index is still
// active, and the new one holds every message at its cursor.
func assertRebuilding(t *testing.T, dataDir string, c indexCatalog) any {
	t.Helper()
	active, building := c.withRole(t, "active"), c.withRole(t, "building")
	if !building.rebuildID.Valid || c.outcomes[building.rebuildID.Int64].Valid {
		t.Errorf("building %+v with rebuilds %v, want a running rebuild", building, c.outcomes)
	}
	if building.cursor != c.highest {
		t.Errorf("the rebuild's cursor is %d, want the batch's last, %d", building.cursor, c.highest)
	}
	segments := indexSegments(t, dataDir, building.id)
	if documents(segments) != 3 {
		t.Errorf("the rebuild's index holds %+v, want 3 documents", segments)
	}
	return rebuildKept{old: active.id, building: building.id, segments: segments}
}

// assertRebuildFinished checks that the restarted server finished the
// rebuild without sending what it had again, and removed the old index.
func assertRebuildFinished(t *testing.T, dataDir string, server *serverProcess, kept rebuildKept) {
	t.Helper()
	server.waitForLogLine("rebuild finished")
	c := readIndexCatalog(t, dataDir)
	if active := c.withRole(t, "active"); active.id != kept.building || len(c.generations) != 1 {
		t.Errorf("generations %+v, want only %d, active", c.generations, kept.building)
	}
	for id, outcome := range c.outcomes {
		if outcome.String != "finished" {
			t.Errorf("rebuild %d ended %+v, want finished", id, outcome)
		}
	}
	assertNotSentAgain(t, dataDir, kept.building, kept.segments)
	waitForNoDir(t, indexDir(dataDir, kept.old))
}

// assertNotSentAgain checks that a generation still holds the segments it
// had, with none of their documents deleted, so no batch was sent again.
func assertNotSentAgain(t *testing.T, dataDir string, generation int64, before []indexSegment) {
	t.Helper()
	after := indexSegments(t, dataDir, generation)
	if !slices.EqualFunc(before, after, func(a, b indexSegment) bool {
		return a.ID == b.ID && a.MaxDoc == b.MaxDoc && b.Deletes == nil
	}) {
		t.Errorf("segments went from %+v to %+v, want the same and none deleted", before, after)
	}
}

// waitForNoDir waits for a directory to be removed.
func waitForNoDir(t *testing.T, dir string) {
	t.Helper()
	deadline := time.Now().Add(processTimeout)
	for {
		if _, err := os.Stat(dir); errors.Is(err, fs.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still there after %s", dir, processTimeout)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

var crashRows = []crashRow{
	{
		point: "after-processing-commit",
		setup: unprocessed,
		before: func(t *testing.T, dataDir string, c indexCatalog) any {
			if c.processed != 1 {
				t.Errorf("%d messages processed, want 1", c.processed)
			}
			for _, g := range c.generations {
				if g.cursor != 0 {
					t.Errorf("generation %+v, want cursor 0", g)
				}
			}
			return nil
		},
	},
	{
		point: "after-index-send",
		setup: processedOnly,
		before: func(t *testing.T, dataDir string, c indexCatalog) any {
			building := c.withRole(t, "building")
			// The search process died with the server, before it committed.
			if segments := indexSegments(t, dataDir, building.id); building.cursor != 0 || len(segments) != 0 {
				t.Errorf("generation %+v holds %+v, want cursor 0 and nothing committed", building, segments)
			}
			return building.id
		},
		after: func(t *testing.T, dataDir string, _ *serverProcess, kept any) {
			if segments := indexSegments(t, dataDir, kept.(int64)); documents(segments) != 3 {
				t.Errorf("the index holds %+v, want 3 documents", segments)
			}
		},
	},
	{
		point: "after-index-commit",
		setup: processedOnly,
		before: func(t *testing.T, dataDir string, c indexCatalog) any {
			building := c.withRole(t, "building")
			segments := indexSegments(t, dataDir, building.id)
			if building.cursor != 0 || documents(segments) != 3 {
				t.Errorf("generation %+v holds %+v, want cursor 0 and the batch's 3 documents", building, segments)
			}
			return rebuildKept{building: building.id, segments: segments}
		},
		after: func(t *testing.T, dataDir string, _ *serverProcess, kept any) {
			// The batch sent again deleted every document of the first
			// commit, so that segment is gone and 3 documents are left.
			k := kept.(rebuildKept)
			segments := indexSegments(t, dataDir, k.building)
			if documents(segments) != 3 || slices.ContainsFunc(segments, func(s indexSegment) bool { return s.ID == k.segments[0].ID }) {
				t.Errorf("the index holds %+v, want 3 documents and not %s", segments, k.segments[0].ID)
			}
		},
	},
	{
		point: "after-cursor-commit",
		setup: processedOnly,
		before: func(t *testing.T, dataDir string, c indexCatalog) any {
			building := c.withRole(t, "building")
			if building.cursor != 3 || c.highest != 3 {
				t.Errorf("generation %+v with highest %d, want cursor 3", building, c.highest)
			}
			return rebuildKept{building: building.id, segments: indexSegments(t, dataDir, building.id)}
		},
		after: func(t *testing.T, dataDir string, _ *serverProcess, kept any) {
			k := kept.(rebuildKept)
			assertNotSentAgain(t, dataDir, k.building, k.segments)
		},
	},
	{
		point:  "rebuild-after-cursor-commit",
		setup:  indexed,
		before: assertRebuilding,
		after: func(t *testing.T, dataDir string, server *serverProcess, kept any) {
			assertRebuildFinished(t, dataDir, server, kept.(rebuildKept))
		},
	},
	{
		point:  "before-swap-commit",
		setup:  indexed,
		before: assertRebuilding,
		after: func(t *testing.T, dataDir string, server *serverProcess, kept any) {
			assertRebuildFinished(t, dataDir, server, kept.(rebuildKept))
		},
	},
	{
		point: "after-swap-commit",
		setup: indexed,
		before: func(t *testing.T, dataDir string, c indexCatalog) any {
			active := c.withRole(t, "active")
			if !active.rebuildID.Valid || c.outcomes[active.rebuildID.Int64].String != "finished" || len(c.generations) != 1 {
				t.Errorf("generations %+v with rebuilds %v, want only the rebuild's, active and finished", c.generations, c.outcomes)
			}
			return active.id
		},
		after: func(t *testing.T, dataDir string, _ *serverProcess, kept any) {
			// Startup removed the old index, so only the new one is left.
			entries, err := os.ReadDir(filepath.Join(dataDir, "index"))
			if err != nil {
				t.Fatal(err)
			}
			var names []string
			for _, entry := range entries {
				names = append(names, entry.Name())
			}
			if want := []string{strconv.FormatInt(kept.(int64), 10)}; !slices.Equal(names, want) {
				t.Errorf("index/ holds %v, want %v", names, want)
			}
		},
	},
}

// A crash at each stop point on the way from processing to a swapped index
// loses no message and indexes none twice.
func TestIndexCrashMatrix(t *testing.T) {
	for _, row := range crashRows {
		t.Run(row.point, func(t *testing.T) {
			t.Parallel()
			dataDir := t.TempDir()
			createAdmin(t, dataDir)
			words := map[int64]string{}
			contents := [][]byte{indexMail("eintraga"), indexMail("eintragb"), indexMail("eintragc")}
			for i, id := range captureUnprocessed(t, dataDir, contents...) {
				words[id] = []string{"eintraga", "eintragb", "eintragc"}[i]
			}

			switch row.setup {
			case processedOnly:
				processWithoutIndexing(t, dataDir)
			case indexed:
				server := startServer(t, dataDir)
				token := signInOverHTTP(t, server.baseURL, adminAddress)
				server.waitForAllIndexed(token, sharedWord, 3)
				if code := server.stop(syscall.SIGTERM); code != 0 {
					t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
				}
			}

			faults := startFaultsWith(t, dataDir, row.point+"=kill", "", nil)
			if row.setup == indexed {
				token := signInOverHTTP(t, faults.baseURL, adminAddress)
				faults.waitForState("available", "up_to_date")
				faults.startRebuild(token)
			}
			faults.waitForKill()
			kept := row.before(t, dataDir, readIndexCatalog(t, dataDir))

			server := startServer(t, dataDir)
			token := signInOverHTTP(t, server.baseURL, adminAddress)
			server.waitForAllIndexed(token, sharedWord, 3)
			server.waitForState("available", "up_to_date")
			if row.after != nil {
				row.after(t, dataDir, server, kept)
			}
			server.assertEachFoundOnce(token, words)
		})
	}
}
