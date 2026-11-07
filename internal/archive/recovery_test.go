package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

// restarted is what a new start of the server has: a new status and log on the
// same data directory and catalog.
func (f *fixture) restarted(t *testing.T) *fixture {
	t.Helper()
	log := &lockedBuffer{}
	logger := slog.New(slog.NewJSONHandler(log, nil))
	clock := func() time.Time { return testNow }
	status := opstatus.New(logger, clock)
	archive, err := Open(f.dataDir, f.db, status, logger, clock)
	if err != nil {
		t.Fatal(err)
	}
	return &fixture{archive: archive, db: f.db, status: status, dataDir: f.dataDir, log: log}
}

// recovered is a new start that has run recovery, which must succeed.
func (f *fixture) recovered(t *testing.T) *fixture {
	t.Helper()
	next := f.restarted(t)
	if err := next.archive.Recover(context.Background()); err != nil {
		t.Fatalf("Recover: %v", err)
	}
	return next
}

// seed writes a file at a path relative to the data directory, the way an
// earlier crash would have left it.
func (f *fixture) seed(t *testing.T, relative string, body []byte) string {
	t.Helper()
	path := filepath.Join(f.dataDir, filepath.FromSlash(relative))
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func (f *fixture) assertRecovery(t *testing.T, removed, notRemoved, unreferenced int) {
	t.Helper()
	result := f.status.Snapshot().Recovery
	if result == nil {
		t.Fatal("no recovery result was recorded")
	}
	if result.TempFilesRemoved != removed || result.TempFilesNotRemoved != notRemoved || result.UnreferencedFound != unreferenced {
		t.Errorf("recovery found %d removed, %d not removed, %d unreferenced; want %d, %d, %d",
			result.TempFilesRemoved, result.TempFilesNotRemoved, result.UnreferencedFound, removed, notRemoved, unreferenced)
	}
	if !result.FinishedAt.Equal(testNow) {
		t.Errorf("finished at %v, want the clock's %v", result.FinishedAt, testNow)
	}
}

func (f *fixture) assertUnreferenced(t *testing.T, want ...opstatus.UnreferencedOriginal) {
	t.Helper()
	got := slices.Clone(f.status.Snapshot().Unreferenced)
	byHash := func(a, b opstatus.UnreferencedOriginal) int { return strings.Compare(a.ContentHash, b.ContentHash) }
	slices.SortFunc(got, byHash)
	slices.SortFunc(want, byHash)
	if !slices.Equal(got, want) {
		t.Errorf("unreferenced = %+v, want %+v", got, want)
	}
}

func (f *fixture) assertNoFailures(t *testing.T) {
	t.Helper()
	if failures := f.status.Snapshot().Failures; len(failures) != 0 {
		t.Errorf("failure events = %+v, want none", failures)
	}
}

func (f *fixture) logEntries(t *testing.T) []map[string]any {
	t.Helper()
	var entries []map[string]any
	for line := range strings.Lines(f.log.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line isn't JSON: %q", line)
		}
		entries = append(entries, entry)
	}
	return entries
}

// assertInvalidNamesLogged checks for the one line that counts the invalid
// names. The names themselves stay out of the log (FR-029).
func (f *fixture) assertInvalidNamesLogged(t *testing.T, count int) {
	t.Helper()
	var lines []map[string]any
	for _, entry := range f.logEntries(t) {
		if entry["msg"] == "invalid names in blobs" {
			lines = append(lines, entry)
		}
	}
	if len(lines) != 1 {
		t.Fatalf("%d lines count the invalid names, want 1:\n%s", len(lines), f.log.String())
	}
	if lines[0]["count"] != float64(count) {
		t.Errorf("count = %v, want %d", lines[0]["count"], count)
	}
}

func (f *fixture) assertBytes(t *testing.T, path string, want []byte) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the file wasn't kept: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s holds %q, want %q", path, got, want)
	}
}

// otherPair is a pair of hex digits that isn't pair, for a blob in the wrong
// prefix directory.
func otherPair(pair string) string {
	if pair == "00" {
		return "11"
	}
	return "00"
}

// FR-024, FR-025, and the data model's recovery steps.
func TestRecovery(t *testing.T) {
	ctx := context.Background()

	t.Run("a clean data directory gives a clean result", func(t *testing.T) {
		f := newFixture(t).recovered(t)

		f.assertRecovery(t, 0, 0, 0)
		f.assertUnreferenced(t)
		f.assertNoFailures(t)
		if got := f.status.ProblemCount(); got != 0 {
			t.Errorf("problem count = %d, want 0", got)
		}
	})

	t.Run("leftover temp files are removed", func(t *testing.T) {
		f := newFixture(t)
		f.seed(t, "tmp/up-111", []byte("Subject: partial"))
		f.seed(t, "tmp/up-222", nil)

		recovered := f.recovered(t)

		recovered.assertTempEmpty(t)
		recovered.assertRecovery(t, 2, 0, 0)
		recovered.assertUnreferenced(t)
		recovered.assertNoFailures(t)
	})

	t.Run("an unreferenced blob is listed with its hash and size, and kept", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("Subject: unreferenced\n\nBody.\n")
		hash := hashOf(body)
		path := f.seed(t, blobPath(hash), body)

		recovered := f.recovered(t)

		recovered.assertUnreferenced(t, opstatus.UnreferencedOriginal{ContentHash: hash, Size: int64(len(body))})
		recovered.assertRecovery(t, 0, 0, 1)
		recovered.assertBytes(t, path, body)
		recovered.assertNoFailures(t)
		if got := recovered.status.ProblemCount(); got != 1 {
			t.Errorf("problem count = %d, want 1", got)
		}
	})

	t.Run("every unreferenced blob is listed", func(t *testing.T) {
		f := newFixture(t)
		var want []opstatus.UnreferencedOriginal
		for _, body := range [][]byte{[]byte("Subject: one\n"), []byte("Subject: two, longer\n"), []byte("Subject: three, longer still\n")} {
			f.seed(t, blobPath(hashOf(body)), body)
			want = append(want, opstatus.UnreferencedOriginal{ContentHash: hashOf(body), Size: int64(len(body))})
		}

		recovered := f.recovered(t)

		recovered.assertUnreferenced(t, want...)
		recovered.assertRecovery(t, 0, 0, 3)
	})

	t.Run("a referenced blob isn't listed", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("Subject: referenced\n")
		f.importBytes(t, body, name("kept.eml"))

		recovered := f.recovered(t)

		recovered.assertUnreferenced(t)
		recovered.assertRecovery(t, 0, 0, 0)
		recovered.assertBytes(t, filepath.Join(f.dataDir, blobPath(hashOf(body))), body)
	})

	// Blobs are never hashed here, so bytes that don't match change nothing: an
	// unreferenced blob is still listed under its name, and a referenced one
	// isn't flagged. A download or repeat import finds the damage.
	t.Run("a damaged referenced blob isn't flagged", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("Subject: damaged later\n")
		f.importBytes(t, body, name("kept.eml"))
		damaged := []byte("Subject: damaged later\nextra\n")
		path := f.seed(t, blobPath(hashOf(body)), damaged)

		recovered := f.recovered(t)

		recovered.assertUnreferenced(t)
		recovered.assertRecovery(t, 0, 0, 0)
		recovered.assertNoFailures(t)
		recovered.assertBytes(t, path, damaged)
	})

	t.Run("a damaged unreferenced blob is listed by its name and its size on disk", func(t *testing.T) {
		f := newFixture(t)
		hash := hashOf([]byte("Subject: what the name promises\n"))
		onDisk := []byte("other bytes")
		path := f.seed(t, blobPath(hash), onDisk)

		recovered := f.recovered(t)

		recovered.assertUnreferenced(t, opstatus.UnreferencedOriginal{ContentHash: hash, Size: int64(len(onDisk))})
		recovered.assertNoFailures(t)
		recovered.assertBytes(t, path, onDisk)
	})

	const marker = "MARKERINVALIDNAME"
	hash := hashOf([]byte("Subject: misplaced\n"))
	upper := strings.ToUpper(hash)
	invalid := []struct {
		name string
		path string
	}{
		{"wrong first prefix directory", "blobs/" + otherPair(hash[0:2]) + "/" + hash[2:4] + "/" + hash},
		{"wrong second prefix directory", "blobs/" + hash[0:2] + "/" + otherPair(hash[2:4]) + "/" + hash},
		{"uppercase hex", "blobs/" + hash[0:2] + "/" + hash[2:4] + "/" + upper},
		{"uppercase hex in the directories too", "blobs/" + upper[0:2] + "/" + upper[2:4] + "/" + upper},
		{"short name", "blobs/" + hash[0:2] + "/" + hash[2:4] + "/" + hash[:63]},
		{"long name", "blobs/" + hash[0:2] + "/" + hash[2:4] + "/" + hash + "0"},
		{"not hex", "blobs/gg/gg/" + strings.Repeat("g", 64)},
		{"a marker for a name", "blobs/" + marker + ".eml"},
		{"a marker for a directory", "blobs/" + marker + "/x"},
		{"directly under blobs", "blobs/" + hash},
		{"one level too shallow", "blobs/" + hash[0:2] + "/" + hash},
		{"one level too deep", "blobs/" + hash[0:2] + "/" + hash[2:4] + "/ef/" + hash},
	}
	for _, tt := range invalid {
		t.Run("an invalid name is logged, not listed, and not deleted: "+tt.name, func(t *testing.T) {
			f := newFixture(t)
			body := []byte("Subject: stray\n")
			path := f.seed(t, tt.path, body)

			recovered := f.recovered(t)

			recovered.assertInvalidNamesLogged(t, 1)
			recovered.assertUnreferenced(t)
			recovered.assertRecovery(t, 0, 0, 0)
			recovered.assertNoFailures(t)
			recovered.assertBytes(t, path, body)
			if got := recovered.status.ProblemCount(); got != 0 {
				t.Errorf("problem count = %d, want 0", got)
			}
			for _, part := range strings.Split(tt.path, "/")[1:] {
				if len(part) > 2 && strings.Contains(recovered.log.String(), part) {
					t.Errorf("the log holds the name %q:\n%s", part, recovered.log.String())
				}
			}
		})
	}

	t.Run("a link at a valid path is an invalid name", func(t *testing.T) {
		f := newFixture(t)
		path := filepath.Join(f.dataDir, blobPath(hash))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink("elsewhere", path); err != nil {
			t.Fatal(err)
		}

		recovered := f.recovered(t)

		recovered.assertInvalidNamesLogged(t, 1)
		recovered.assertUnreferenced(t)
		if _, err := os.Lstat(path); err != nil {
			t.Errorf("the link wasn't kept: %v", err)
		}
	})

	t.Run("invalid names are counted together, beside a valid blob", func(t *testing.T) {
		f := newFixture(t)
		for _, tt := range invalid {
			f.seed(t, tt.path, []byte("Subject: stray\n"))
		}
		body := []byte("Subject: valid and unreferenced\n")
		f.seed(t, blobPath(hashOf(body)), body)
		before := files(t, filepath.Join(f.dataDir, "blobs"))

		recovered := f.recovered(t)

		recovered.assertInvalidNamesLogged(t, len(invalid))
		recovered.assertUnreferenced(t, opstatus.UnreferencedOriginal{ContentHash: hashOf(body), Size: int64(len(body))})
		recovered.assertRecovery(t, 0, 0, 1)
		if after := files(t, filepath.Join(f.dataDir, "blobs")); !slices.Equal(before, after) {
			t.Errorf("blobs/ went from %v to %v", before, after)
		}
		if strings.Contains(recovered.log.String(), marker) {
			t.Errorf("the log holds an invalid name:\n%s", recovered.log.String())
		}
	})

	t.Run("a second run gives the same result, and the blob count never falls", func(t *testing.T) {
		f := newFixture(t)
		f.importBytes(t, []byte("Subject: referenced\n"), name("kept.eml"))
		body := []byte("Subject: unreferenced\n")
		f.seed(t, blobPath(hashOf(body)), body)
		f.seed(t, "blobs/"+marker, []byte("stray"))
		f.seed(t, "tmp/up-111", []byte("Subject: partial"))
		blobs := filepath.Join(f.dataDir, "blobs")
		before := files(t, blobs)
		want := opstatus.UnreferencedOriginal{ContentHash: hashOf(body), Size: int64(len(body))}

		first := f.recovered(t)

		first.assertTempEmpty(t)
		first.assertRecovery(t, 1, 0, 1)
		first.assertUnreferenced(t, want)
		first.assertInvalidNamesLogged(t, 1)
		if after := files(t, blobs); !slices.Equal(before, after) {
			t.Fatalf("blobs/ went from %v to %v", before, after)
		}

		second := first.recovered(t)

		second.assertTempEmpty(t)
		second.assertRecovery(t, 0, 0, 1)
		second.assertUnreferenced(t, want)
		second.assertInvalidNamesLogged(t, 1)
		second.assertNoFailures(t)
		if after := files(t, blobs); !slices.Equal(before, after) {
			t.Errorf("blobs/ went from %v to %v", before, after)
		}
	})

	t.Run("the result goes to the log", func(t *testing.T) {
		f := newFixture(t)
		f.seed(t, "tmp/up-111", nil)
		body := []byte("Subject: unreferenced\n")
		f.seed(t, blobPath(hashOf(body)), body)

		recovered := f.recovered(t)

		var logged bool
		for _, entry := range recovered.logEntries(t) {
			if entry["temp_files_removed"] == float64(1) && entry["temp_files_not_removed"] == float64(0) && entry["unreferenced_found"] == float64(1) {
				logged = true
			}
		}
		if !logged {
			t.Errorf("no log line holds the result (FR-028):\n%s", recovered.log.String())
		}
	})

	// FR-026: unusable storage or an unusable catalog stops the start.
	t.Run("a catalog that can't be read stops recovery and deletes nothing", func(t *testing.T) {
		f := newFixture(t)
		body := []byte("Subject: unreferenced\n")
		path := f.seed(t, blobPath(hashOf(body)), body)
		next := f.restarted(t)
		if err := f.db.Close(); err != nil {
			t.Fatal(err)
		}

		// Startup names the catalog as the cause through this error (FR-026).
		if err := next.archive.Recover(ctx); !errors.Is(err, ErrCatalogUnusable) {
			t.Errorf("Recover = %v, want ErrCatalogUnusable", err)
		}
		next.assertBytes(t, path, body)
	})

	t.Run("a tmp/ that can't be read stops recovery", func(t *testing.T) {
		f := newFixture(t)
		next := f.restarted(t)
		tmp := filepath.Join(f.dataDir, "tmp")
		if err := os.Remove(tmp); err != nil {
			t.Fatal(err)
		}
		// A plain file where the directory belongs fails even for root.
		if err := os.WriteFile(tmp, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		err := next.archive.Recover(ctx)
		if err == nil {
			t.Fatal("Recover = nil, want an error")
		}
		if errors.Is(err, ErrCatalogUnusable) {
			t.Errorf("Recover = %v, which names the catalog, and storage is the cause", err)
		}
	})
}
