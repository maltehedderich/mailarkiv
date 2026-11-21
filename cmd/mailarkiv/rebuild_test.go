package main

import (
	"os"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
)

// fixtureSearches are three searches over the plain fixtures, each finding
// one of them.
var fixtureSearches = map[string]string{
	"plain-text.eml": "wochenplan",
	"html-only.eml":  "quartals",
	"attachment.eml": "vertrag",
}

// importFixtures imports the fixtures fixtureSearches names, waits until
// each search finds its message, and returns each search's IDs.
func importFixtures(t *testing.T, server *serverProcess, token string) map[string][]int64 {
	t.Helper()
	for name := range fixtureSearches {
		importFixture(t, server, token, plainFixture(t, name))
	}
	results := map[string][]int64{}
	for _, words := range fixtureSearches {
		server.waitForSearch(token, words)
		results[words], _ = server.searchIDs(token, words)
	}
	return results
}

// assertResults checks that each search finds what it found before.
func assertResults(t *testing.T, server *serverProcess, token string, want map[string][]int64) {
	t.Helper()
	for words, ids := range want {
		if found, _ := server.searchIDs(token, words); !slices.Equal(found, ids) {
			t.Errorf("%s found %v, want %v", words, found, ids)
		}
	}
}

func stopServer(t *testing.T, server *serverProcess) {
	t.Helper()
	if code := server.stop(syscall.SIGTERM); code != 0 {
		t.Fatalf("exit = %d, want 0:\n%s", code, server.stderr())
	}
}

// With index/ deleted, the server builds a new index while searches answer
// from it with a notice, and then they find what they found before.
func TestDeletedIndexBuilds(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	before := importFixtures(t, server, token)
	stopServer(t, server)

	if err := os.RemoveAll(filepath.Join(dataDir, "index")); err != nil {
		t.Fatal(err)
	}
	// The first commit hangs, which holds the build until the test kills
	// the search process.
	server = startFaultsWith(t, dataDir, "", "after-commit=hang", nil)
	server.waitForState("available", "building")
	c := readIndexCatalog(t, dataDir)
	if building := c.withRole(t, "building"); len(c.generations) != 1 || building.cursor >= c.highest {
		t.Errorf("generations %+v with highest %d, want one building, short of it", c.generations, c.highest)
	}
	for words := range before {
		if _, notIncluded := server.searchIDs(token, words); notIncluded == 0 {
			t.Errorf("%s left nothing out while building, want messages not included", words)
		}
	}

	server.killSearchProcess()
	server.waitForAllIndexed(token, "wochenplan", 1)
	server.waitForState("available", "up_to_date")
	assertResults(t, server, token, before)
}

// A rebuild stopped at each step continues from where it stopped. Searches
// during it find what they found before it, and after it they also find a
// message imported during it.
func TestRebuildInterrupted(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	before := importFixtures(t, server, token)
	stopServer(t, server)

	// The first search process hangs at the rebuild's first commit. Once the
	// test kills it, the next one commits, and the server stops after it
	// saves the rebuild's cursor.
	server = startFaultsWith(t, dataDir, "rebuild-after-cursor-commit=kill", "after-commit=hang", nil)
	server.waitForState("available", "up_to_date")
	server.startRebuild(token)
	started := server.waitForLogLine("rebuild started")
	waitForCommit(t, dataDir, int64(started["generation_id"].(float64)))
	assertResults(t, server, token, before)
	during := importFixture(t, server, token, indexMail("eintraga"))
	assertResults(t, server, token, before)
	server.killSearchProcess()
	server.waitForKill()

	cursor := readIndexCatalog(t, dataDir).withRole(t, "building").cursor
	for _, point := range []string{"before-swap-commit", "after-swap-commit"} {
		server = startFaultsWith(t, dataDir, point+"=kill", "", nil)
		server.waitForKill()
		// The swap makes the rebuild's generation active.
		role := map[string]string{"before-swap-commit": "building", "after-swap-commit": "active"}[point]
		rebuilt := readIndexCatalog(t, dataDir).withRole(t, role)
		if !rebuilt.rebuildID.Valid {
			t.Errorf("after %s the %s generation is %+v, want the rebuild's", point, role, rebuilt)
		}
		if rebuilt.cursor < cursor {
			t.Errorf("after %s the rebuild's cursor is %d, want at least %d", point, rebuilt.cursor, cursor)
		}
		cursor = rebuilt.cursor
	}

	server = startServer(t, dataDir)
	server.waitForAllIndexed(token, "eintraga", 1)
	assertResults(t, server, token, before)
	if found, _ := server.searchIDs(token, "eintraga"); !slices.Equal(found, []int64{during}) {
		t.Errorf("eintraga found %v, want the message imported during the rebuild, %d", found, during)
	}
	c := readIndexCatalog(t, dataDir)
	if len(c.generations) != 1 || c.generations[0].role != "active" || len(c.outcomes) != 1 {
		t.Errorf("generations %+v with rebuilds %v, want one active and one rebuild", c.generations, c.outcomes)
	}
	for id, outcome := range c.outcomes {
		if outcome.String != "finished" {
			t.Errorf("rebuild %d ended %+v, want finished", id, outcome)
		}
	}
}
