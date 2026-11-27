// Command sizing measures how large an archive search supports. seed fills a
// data directory with synthetic mail through the archive's own import, and
// measure runs MailArkiv's processing, indexing, and search on it with the
// shipped search program, then appends the results to a Markdown file.
// `mise run sizing` runs both, outside the default checks.
package main

import (
	"bytes"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/processing"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

const (
	exitFailed  = 1
	exitBadArgs = 2

	usage = `Usage:
  sizing seed --data-dir DIR --messages N [--seed S]
  sizing measure --data-dir DIR --results FILE

seed imports N synthetic messages, the fixed search set among them. measure
processes and indexes them, times searches and a rebuild, and appends the
results to FILE.
`

	// fixedSetDir holds the messages the automated search tests use, from
	// the repository root.
	fixedSetDir = "internal/web/testdata/searchset"
	// importer is the account address the generated imports record.
	importer = "sizing@example.test"
	// importers run at once. Each import waits on its own fsyncs, so several
	// keep the disk busy.
	importers = 8
	// progressEvery is how often a long run prints how far it is.
	progressEvery = time.Minute
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		return badArgs(stderr)
	}
	// Module logs go to stderr as JSON lines, as the server's do.
	logger := slog.New(slog.NewJSONHandler(stderr, nil))
	var err error
	switch args[0] {
	case "seed":
		flags := newFlags(stderr)
		dataDir := flags.String("data-dir", "", "")
		messages := flags.Int("messages", 0, "")
		seed := flags.Uint64("seed", 1, "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *dataDir == "" || *messages < 1 {
			return badArgs(stderr)
		}
		err = seedArchive(context.Background(), seedOptions{
			dataDir: *dataDir, messages: *messages, seed: *seed, out: stdout, logger: logger,
		})
	case "measure":
		flags := newFlags(stderr)
		dataDir := flags.String("data-dir", "", "")
		results := flags.String("results", "", "")
		if flags.Parse(args[1:]) != nil || flags.NArg() != 0 || *dataDir == "" || *results == "" {
			return badArgs(stderr)
		}
		err = measure(context.Background(), measureOptions{
			dataDir: *dataDir, results: *results, out: stdout, logger: logger,
		})
	case "parse-worker":
		// measure starts this binary as its parse workers, as the server
		// starts its own.
		if len(args) != 1 {
			return badArgs(stderr)
		}
		return processing.RunWorker(os.Stdin, os.Stdout)
	default:
		return badArgs(stderr)
	}
	if err != nil {
		fmt.Fprintf(stderr, "sizing %s: %v\n", args[0], err)
		return exitFailed
	}
	return 0
}

func newFlags(stderr io.Writer) *flag.FlagSet {
	flags := flag.NewFlagSet("sizing", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.Usage = func() { fmt.Fprint(stderr, usage) }
	return flags
}

func badArgs(stderr io.Writer) int {
	fmt.Fprint(stderr, usage)
	return exitBadArgs
}

// repositoryRoot is the nearest directory above the working directory that
// holds go.mod. The fixed set and the search program are found from it.
func repositoryRoot() (string, error) {
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", errors.New("run sizing inside the repository: no go.mod above the working directory")
		}
		dir = parent
	}
}

type seedOptions struct {
	dataDir  string
	messages int
	seed     uint64
	out      io.Writer
	logger   *slog.Logger
}

// seedArchive imports the fixed set and then generated messages until the
// archive holds o.messages. A fixed message that an import refuses is
// skipped, because only a collection could store it. Generated message i is
// the same for the same seed, so a seed that stopped part way can run again:
// it finds the messages already archived and imports the rest.
func seedArchive(ctx context.Context, o seedOptions) error {
	root, err := repositoryRoot()
	if err != nil {
		return err
	}
	fixed, err := filepath.Glob(filepath.Join(root, fixedSetDir, "*.eml"))
	if err != nil {
		return err
	}
	if len(fixed) == 0 {
		return fmt.Errorf("no fixed set in %s", fixedSetDir)
	}
	if o.messages < len(fixed) {
		return fmt.Errorf("--messages must be at least %d, the size of the fixed set", len(fixed))
	}
	if err := os.MkdirAll(o.dataDir, 0o700); err != nil {
		return err
	}
	db, err := sqlitedb.Open(filepath.Join(o.dataDir, "catalog.db"))
	if err != nil {
		return err
	}
	defer db.Close()
	originals, err := archive.Open(o.dataDir, db, opstatus.New(o.logger, time.Now), o.logger, time.Now)
	if err != nil {
		return err
	}

	started := time.Now()
	stored := 0
	for _, path := range fixed {
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		err = importOne(ctx, originals, content, filepath.Base(path))
		if refused, ok := errors.AsType[*refusedError](err); ok {
			fmt.Fprintf(o.out, "Skipped %s, which an import refuses: %s\n", filepath.Base(path), refused.reason)
			continue
		}
		if err != nil {
			return fmt.Errorf("import %s: %w", filepath.Base(path), err)
		}
		stored++
	}

	generated := o.messages - stored
	g := newGenerator(o.seed, newVocabulary())
	ctx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	var next, done atomic.Int64
	var wg sync.WaitGroup
	for range importers {
		wg.Go(func() {
			for ctx.Err() == nil {
				i := int(next.Add(1) - 1)
				if i >= generated {
					return
				}
				if err := importOne(ctx, originals, g.message(i), fmt.Sprintf("sizing-%07d.eml", i)); err != nil {
					cancel(fmt.Errorf("import generated message %d: %w", i, err))
					return
				}
				done.Add(1)
			}
		})
	}
	finished := make(chan struct{})
	go func() { wg.Wait(); close(finished) }()
	ticker := time.NewTicker(progressEvery)
	defer ticker.Stop()
	for waiting := true; waiting; {
		select {
		case <-finished:
			waiting = false
		case <-ticker.C:
			fmt.Fprintf(o.out, "Imported %d of %d generated messages after %s\n",
				done.Load(), generated, formatDuration(time.Since(started)))
		}
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	fmt.Fprintf(o.out, "Imported %d messages, %d of them generated, in %s\n",
		o.messages, generated, formatDuration(time.Since(started)))
	return nil
}

// refusedError means the import's file check refused the message.
type refusedError struct{ reason archive.Reason }

func (e *refusedError) Error() string { return "refused: " + e.reason.String() }

// importOne imports content and fails unless the archive stored it.
func importOne(ctx context.Context, originals *archive.Archive, content []byte, name string) error {
	result, err := originals.Import(ctx, bytes.NewReader(content), archive.ImportMeta{
		AccountEmail: importer, FileName: &name,
	})
	if err != nil {
		return err
	}
	switch r := result.(type) {
	case archive.Stored:
		return nil
	case archive.Refused:
		return &refusedError{reason: r.Reason}
	case archive.Failed:
		return fmt.Errorf("failed: %s", r.Category)
	default:
		return fmt.Errorf("not stored: %T", result)
	}
}
