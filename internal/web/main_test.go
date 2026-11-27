package web

import (
	"bufio"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/processing"
)

// The processing pools start this test binary as their workers. The modes
// other than 1 break the worker on purpose.
func TestMain(m *testing.M) {
	switch os.Getenv("MAILARKIV_TEST_PARSE_WORKER") {
	case "":
		os.Exit(m.Run())
	case "1":
		os.Exit(processing.RunWorker(os.Stdin, os.Stdout))
	case "hang":
		os.Exit(hangWorker(os.Getenv("MAILARKIV_TEST_MARKER_DIR")))
	case "cut":
		os.Exit(processing.RunWorker(os.Stdin, &cutWriter{out: os.Stdout}))
	default:
		os.Exit(3)
	}
}

// hangWorker takes one request with its original, creates a file named for
// its process in dir, so a test can tell it holds a request, and never
// answers.
func hangWorker(dir string) int {
	in := bufio.NewReader(os.Stdin)
	var request struct {
		Size int64 `json:"size"`
	}
	if err := processing.ReadFrame(in, &request); err != nil {
		return 0
	}
	if _, err := io.CopyN(io.Discard, in, request.Size); err != nil {
		return 3
	}
	if err := os.WriteFile(filepath.Join(dir, strconv.Itoa(os.Getpid())), nil, 0o600); err != nil {
		return 3
	}
	for {
		time.Sleep(time.Hour)
	}
}

// cutWriter lets a worker's first write through, which is an answer's frame,
// then exits partway through the second, which is an attachment's bytes.
type cutWriter struct {
	out    io.Writer
	writes int
}

func (w *cutWriter) Write(p []byte) (int, error) {
	w.writes++
	if w.writes == 1 {
		return w.out.Write(p)
	}
	_, _ = w.out.Write(p[:len(p)/2])
	os.Exit(9)
	return 0, nil
}

func testWorkerCommand() *exec.Cmd {
	return workerCommand("1", "")
}

// workerCommand starts this test binary as a worker in mode. markers is
// hang's directory.
func workerCommand(mode, markers string) *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{"MAILARKIV_TEST_PARSE_WORKER=" + mode, "MAILARKIV_TEST_MARKER_DIR=" + markers}
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
