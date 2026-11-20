package web

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/processing"
)

func TestMain(m *testing.M) {
	// The processing pool starts this test binary as its workers.
	if os.Getenv("MAILARKIV_TEST_PARSE_WORKER") == "1" {
		os.Exit(processing.RunWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

func testWorkerCommand() *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{"MAILARKIV_TEST_PARSE_WORKER=1"}
	return cmd
}

// searchProgram finds bin/mailarkiv-search from the repository root.
func searchProgram(t *testing.T) string {
	t.Helper()
	return builtProgram(t, filepath.Join("bin", "mailarkiv-search"), "search:build")
}

// faultsSearchProgram finds the search program's build with fault points.
func faultsSearchProgram(t *testing.T) string {
	t.Helper()
	return builtProgram(t, filepath.Join("bin", "faults", "mailarkiv-search"), "search:build:faults")
}

// builtProgram finds path from the repository root, which task builds.
func builtProgram(t *testing.T, path, task string) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod above the test's directory")
		}
		dir = parent
	}
	program := filepath.Join(dir, path)
	if _, err := os.Stat(program); err != nil {
		t.Fatalf("%s is missing; run mise run %s", program, task)
	}
	return program
}

// background runs fn until the test ends.
// It returns a function that stops fn early and waits for it.
func background(t *testing.T, fn func(context.Context)) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(ctx)
	}()
	stop := func() {
		cancel()
		<-done
	}
	t.Cleanup(stop)
	return stop
}
