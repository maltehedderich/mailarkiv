package web

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/search"
)

const gib = int64(1) << 30

// fakeDisk answers free space readings with what the test sets. A value set
// with first answers only the next reading.
type fakeDisk struct {
	mu    sync.Mutex
	free  int64
	first *int64
}

func (d *fakeDisk) read(string) (int64, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.first != nil {
		free := *d.first
		d.first = nil
		return free, nil
	}
	return d.free, nil
}

func (d *fakeDisk) set(first, free int64) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.free, d.first = free, &first
}

func TestRebuildRoute(t *testing.T) {
	t.Run("without a search process", func(t *testing.T) {
		h := newHarness(t, Routes())
		h.createAccount(t, "admin@example.test")
		session := h.signIn(t, "admin@example.test")
		response, body := h.send(t, http.MethodPost, "/api/search/rebuild", nil, session)
		assertJSONResponse(t, response, body, http.StatusServiceUnavailable, `{"error":"search_unavailable"}`)
	})

	disk := &fakeDisk{free: 1 * gib}
	h := newHarnessWith(t, Routes(), harnessOptions{search: searchProgram(t), freeSpace: disk.read, holdSearch: true})
	h.createAccount(t, "admin@example.test")
	session := h.signIn(t, "admin@example.test")
	rebuild := func() (*http.Response, string) {
		t.Helper()
		return h.send(t, http.MethodPost, "/api/search/rebuild", nil, session)
	}

	// Low disk space holds the first build, so no index is active.
	h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(plainFixture(t, "plain-text.eml")), session)
	waitFor(t, "the message processed", func() bool {
		highest, err := h.archive.HighestProcessedSeq(context.Background())
		return err == nil && highest == 1
	})
	h.startSearch()
	waitFor(t, "indexing paused", func() bool { return h.search.State().Indexing == search.IndexingPaused })
	response, body := rebuild()
	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"no_index"}`)

	// The next message makes indexing read free space again.
	disk.set(3*gib, 3*gib)
	h.send(t, http.MethodPost, "/api/imports", bytes.NewReader(plainFixture(t, "html-only.eml")), session)
	waitFor(t, "indexing up to date", func() bool { return h.search.State().Indexing == search.IndexingUpToDate })
	needed := 2*gib + activeIndexSize(t, h)
	disk.set(needed, needed)
	response, body = rebuild()
	assertJSONResponse(t, response, body, http.StatusConflict,
		fmt.Sprintf(`{"error":"not_enough_space","needed_bytes":%d,"free_bytes":%d}`, needed, needed))

	// The first reading allows the rebuild, and the rest pause it.
	disk.set(needed+1, 1*gib)
	response, body = rebuild()
	assertJSONResponse(t, response, body, http.StatusAccepted,
		`{"rebuild":{"id":1,"started_at":`+quote(formatTime(testNow))+`,"started_by":"admin@example.test"}}`)
	response, body = rebuild()
	assertJSONResponse(t, response, body, http.StatusConflict, `{"error":"rebuild_running"}`)

	// Signed-in only.
	response, body = h.send(t, http.MethodPost, "/api/search/rebuild", nil, nil)
	assertJSONResponse(t, response, body, http.StatusUnauthorized, noSession)
}

// activeIndexSize sums the sizes of the files in the active index's
// directory, as the search program's size does.
func activeIndexSize(t *testing.T, h *harness) int64 {
	t.Helper()
	var id int64
	if err := h.db.Reader.QueryRow("SELECT id FROM search_generation WHERE role = 'active'").Scan(&id); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(h.dataDir, "index", strconv.FormatInt(id, 10)))
	if err != nil {
		t.Fatal(err)
	}
	var size int64
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().IsRegular() {
			size += info.Size()
		}
	}
	return size
}
