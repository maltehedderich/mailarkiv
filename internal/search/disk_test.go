package search

import (
	"path/filepath"
	"slices"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

func TestLowDiskPausesIndexing(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	disk := f.fakeDisk(3 * gib)
	f.module.diskCheckEvery = 20 * time.Millisecond
	first := indexedOnce(f)

	low := int64(1536) << 20
	disk.set(low)
	f.waitForIndexing(IndexingPaused)
	events := f.failures(opstatus.LowDiskSpace)
	if len(events) != 1 || events[0].Detail == nil || events[0].Detail.FreeBytes == nil || *events[0].Detail.FreeBytes != low {
		t.Fatalf("low_disk_space events %+v, want one with %d free bytes", events, low)
	}

	// Capture and search go on.
	second := f.importFixture("html-only.eml")
	f.waitForProcessed(2)
	result := f.search("wochenplan")
	if len(result.Rows) != 1 || result.Rows[0].ID != first {
		t.Errorf("search gave %+v, want the indexed message", result)
	}
	if result := f.search("bericht"); len(result.Rows) != 0 || result.NotIncluded != 1 {
		t.Errorf("search gave %+v, want nothing found and 1 not included", result)
	}

	disk.set(int64(2560) << 20)
	f.waitForReadings(disk, 3)
	if got := f.module.State().Indexing; got != IndexingPaused {
		t.Errorf("at 2.5 GiB indexing is %s, want paused", got)
	}
	disk.set(3 * gib)
	f.waitForIndexing(IndexingUpToDate)
	if got := f.ids("bericht"); !slices.Equal(got, []int64{second}) {
		t.Errorf("after resuming search found %v, want %d", got, second)
	}
	if events := f.failures(opstatus.LowDiskSpace); len(events) != 1 {
		t.Errorf("low_disk_space events %+v, want still one", events)
	}
}

func TestDiskValues(t *testing.T) {
	if pauseBelow != 2*gib || resumeAt != 3*gib {
		t.Errorf("pause below %d and resume at %d, want 2 GiB and 3 GiB", pauseBelow, resumeAt)
	}
	if m := New(Config{}); m.diskCheckEvery != 30*time.Second {
		t.Errorf("free space read every %s, want 30 s", m.diskCheckEvery)
	}

	// The reader is statfs on the index directory.
	dir := t.TempDir()
	free, err := statfsFree(dir)
	var stat syscall.Statfs_t
	if statErr := syscall.Statfs(dir, &stat); err != nil || statErr != nil || free != int64(stat.Bavail)*stat.Bsize {
		t.Errorf("statfsFree gave %d, %v; want %d", free, err, int64(stat.Bavail)*stat.Bsize)
	}

	// A reading comes before each batch.
	f := newFixture(t, searchProgram(t))
	f.module.batchMessages = 1
	var mu sync.Mutex
	var events []string
	record := func(event string) {
		mu.Lock()
		defer mu.Unlock()
		events = append(events, event)
	}
	disk := f.fakeDisk(3 * gib)
	f.module.freeSpace = func(path string) (int64, error) {
		record("read")
		return disk.read(path)
	}
	f.module.batchSent = func(sentBatch) { record("batch") }
	for _, name := range []string{"plain-text.eml", "html-only.eml", "attachment.eml"} {
		f.importFixture(name)
	}
	f.runProcessing()
	f.waitForProcessed(3)
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)

	mu.Lock()
	defer mu.Unlock()
	batches := 0
	for i, event := range events {
		if event != "batch" {
			continue
		}
		batches++
		// A reading lies between this batch and the one before.
		previous := -1
		for j := i - 1; j >= 0; j-- {
			if events[j] == "batch" {
				previous = j
				break
			}
		}
		if !slices.Contains(events[previous+1:i], "read") {
			t.Errorf("events %v: batch %d had no reading before it", events, batches)
		}
	}
	if batches != 3 {
		t.Errorf("events %v, want 3 batches", events)
	}
	for _, path := range disk.paths {
		if path != filepath.Join(f.dataDir, "index") {
			t.Errorf("read free space at %s, want the index directory", path)
		}
	}
}
