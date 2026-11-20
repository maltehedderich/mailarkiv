package processing

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The test binary is also the worker, as the server binary is in production.
func TestMain(m *testing.M) {
	if os.Getenv("MAILARKIV_TEST_PARSE_WORKER") == "1" {
		os.Exit(RunWorker(os.Stdin, os.Stdout))
	}
	os.Exit(m.Run())
}

// testWorkerCommand starts this test binary as a worker, with nothing in its
// environment but the switch above.
func testWorkerCommand() *exec.Cmd {
	cmd := exec.Command(os.Args[0])
	cmd.Env = []string{"MAILARKIV_TEST_PARSE_WORKER=1"}
	return cmd
}

func newTestPool(t *testing.T, size int, logger *slog.Logger) *Pool {
	t.Helper()
	pool, err := NewPool(size, testWorkerCommand, logger)
	if err != nil {
		t.Fatalf("NewPool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func plainFixture(t *testing.T, name string) []byte {
	t.Helper()
	original, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", "plain", name))
	if err != nil {
		t.Fatal(err)
	}
	return original
}

type lockedBuffer struct {
	mu     sync.Mutex
	buffer bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buffer.Write(p)
}

// entries decodes the JSON log lines written so far.
func (b *lockedBuffer) entries(t *testing.T) []map[string]any {
	t.Helper()
	b.mu.Lock()
	defer b.mu.Unlock()
	var entries []map[string]any
	for line := range strings.Lines(b.buffer.String()) {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("log line %q: %v", line, err)
		}
		entries = append(entries, entry)
	}
	return entries
}

func newLogger() (*slog.Logger, *lockedBuffer) {
	log := &lockedBuffer{}
	return slog.New(slog.NewJSONHandler(log, nil)), log
}

func slogDiscard() *slog.Logger { return slog.New(slog.DiscardHandler) }
