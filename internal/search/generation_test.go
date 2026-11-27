package search

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

func TestPrepareMakesIndexAndRemovesUnnamedDirectories(t *testing.T) {
	f := newFixture(t, "")
	index := filepath.Join(f.dataDir, "index")
	info, err := os.Stat(index)
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0o700 {
		t.Fatalf("index/ is %v, %v; want a directory with mode 0700", info, err)
	}

	if _, err := f.db.Writer.Exec(
		"INSERT INTO search_generation (id, role, schema_version, cursor, created_at) VALUES (3, 'active', 1, 0, 0)"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"3", "7", "leftover"} {
		if err := os.MkdirAll(filepath.Join(index, name, "inner"), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	if err := Prepare(context.Background(), f.dataDir, f.db); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(index)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	if !slices.Equal(names, []string{"3"}) {
		t.Errorf("index/ holds %v, want only 3", names)
	}
}

func TestFirstStartBuildsThenActivates(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	f.importFixture("plain-text.eml")
	report := f.importFixture("html-only.eml")
	f.importFixture("attachment.eml")
	f.runProcessing()
	f.waitForProcessed(3)

	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	if got, want := f.generations(), []generationRow{{1, "active", 3}}; !slices.Equal(got, want) {
		t.Errorf("generations %+v, want %+v", got, want)
	}
	if _, err := os.Stat(filepath.Join(f.dataDir, "index", "1", "meta.json")); err != nil {
		t.Errorf("the index isn't in index/1: %v", err)
	}
	if got := f.ids("quartals"); !slices.Equal(got, []int64{report}) {
		t.Errorf("quartals finds %v, want %d", got, report)
	}

	var indexing []string
	for _, entry := range f.logged("search state") {
		indexing = append(indexing, entry["indexing"].(string))
	}
	building := slices.Index(indexing, "building")
	if building < 0 || slices.Index(indexing[building:], "up_to_date") < 0 {
		t.Errorf("logged indexing states %v, want building, then up_to_date", indexing)
	}
}

func TestMissingActiveDirectoryStartsANewBuild(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	if _, err := f.db.Writer.Exec(
		"INSERT INTO search_generation (id, role, schema_version, cursor, created_at) VALUES (5, 'active', 1, 9, 0)"); err != nil {
		t.Fatal(err)
	}
	plan := f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)

	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	// IDs are never reused.
	if got, want := f.generations(), []generationRow{{6, "active", 1}}; !slices.Equal(got, want) {
		t.Errorf("generations %+v, want %+v", got, want)
	}
	if got := f.ids("wochenplan"); !slices.Equal(got, []int64{plan}) {
		t.Errorf("wochenplan finds %v, want %d", got, plan)
	}
}

func TestRestartOpensTheActiveIndex(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	plan := f.importFixture("plain-text.eml")
	f.runProcessing()
	f.waitForProcessed(1)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.module.Run(ctx)
	}()
	f.waitForIndexing(IndexingUpToDate)
	cancel()
	<-done

	f.module = New(f.module.config)
	f.runSearch()
	f.waitForIndexing(IndexingUpToDate)
	if got, want := f.generations(), []generationRow{{1, "active", 1}}; !slices.Equal(got, want) {
		t.Errorf("generations %+v, want %+v", got, want)
	}
	if got := f.ids("wochenplan"); !slices.Equal(got, []int64{plan}) {
		t.Errorf("wochenplan finds %v after a restart, want %d once", got, plan)
	}
}

// A data directory where index/ can't be prepared still serves. Searches
// answer Unavailable with Index write failed.
func TestUnpreparedIndexMakesSearchUnavailable(t *testing.T) {
	for _, tt := range []struct {
		name  string
		setUp func(index string) error
		cause opstatus.Cause
	}{
		{"unreadable", func(index string) error { return os.Chmod(index, 0) }, opstatus.CausePermissionDenied},
		{"a file", func(index string) error {
			if err := os.Remove(index); err != nil {
				return err
			}
			return os.WriteFile(index, nil, 0o600)
		}, opstatus.CauseOther},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, "")
			index := filepath.Join(f.dataDir, "index")
			if err := tt.setUp(index); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(index, 0o700) })

			err := Prepare(context.Background(), f.dataDir, f.db)
			if err == nil {
				t.Fatal("Prepare succeeded")
			}
			f.module.PrepareFailed(err)

			_, err = f.module.Search(context.Background(), Query{Words: "quartals", Page: 1})
			unavailable, ok := errors.AsType[*UnavailableError](err)
			if !ok || unavailable.Cause != opstatus.IndexWriteFailed || unavailable.NextStartAt != nil {
				t.Fatalf("Search = %v, want Unavailable for index_write_failed with no next start", err)
			}
			failures := f.failures(opstatus.IndexWriteFailed)
			if len(failures) != 1 || failures[0].Detail == nil || failures[0].Detail.Cause != tt.cause ||
				failures[0].Detail.GenerationID != nil {
				t.Fatalf("index_write_failed events = %+v, want one with cause %s and no generation", failures, tt.cause)
			}
		})
	}
}
