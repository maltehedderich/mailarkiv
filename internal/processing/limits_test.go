package processing

import (
	"errors"
	"path/filepath"
	"slices"
	"syscall"
	"testing"
	"time"
)

// storedProblem is a message_problem row as the catalog holds it.
type storedProblem struct {
	position     string
	declaredType *string
	kind         string
}

func (f *processingFixture) problems(id int64) []storedProblem {
	f.t.Helper()
	rows, err := f.db.Reader.Query(
		"SELECT position, declared_type, kind FROM message_problem WHERE message_id = ? ORDER BY position, kind", id)
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	var problems []storedProblem
	for rows.Next() {
		var p storedProblem
		if err := rows.Scan(&p.position, &p.declaredType, &p.kind); err != nil {
			f.t.Fatal(err)
		}
		problems = append(problems, p)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return problems
}

// expectOnlyProblem checks that a message has exactly one problem, for the
// whole message, with no declared type.
func (f *processingFixture) expectOnlyProblem(id int64, kind string) {
	f.t.Helper()
	got := f.problems(id)
	if len(got) != 1 || got[0].position != "" || got[0].declaredType != nil || got[0].kind != kind {
		f.t.Errorf("message %d has problems %+v, want only %s for the whole message", id, got, kind)
	}
}

// workerStops returns the exit field of each processing_worker_stopped
// entry for message id, and fails on an entry that isn't at error level.
func (f *processingFixture) workerStops(id int64) []string {
	f.t.Helper()
	var exits []string
	for _, entry := range f.log.entries(f.t) {
		if entry["msg"] != "processing_worker_stopped" {
			continue
		}
		if entry["level"] != "ERROR" || entry["message_id"] != float64(id) {
			f.t.Errorf("log entry %v, want message_id %d at ERROR", entry, id)
		}
		exit, _ := entry["exit"].(string)
		exits = append(exits, exit)
	}
	return exits
}

// waitUntilGone waits for a process to be reaped, for at most 5 s.
func waitUntilGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(syscall.Kill(pid, 0), syscall.ESRCH) {
		if time.Now().After(deadline) {
			t.Fatalf("worker %d still runs", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestWorkerKilledAtLimit(t *testing.T) {
	f := newProcessingFixtureWith(t, 1, testLimit, modeWorkerCommand("", "hang", "1"))
	hung := f.pool.pids()
	hanging := f.importFixture("plain-text.eml")

	start := time.Now()
	if taken := f.runOnce(f.processor); taken != 1 {
		t.Fatalf("processing took %d messages, want 1", taken)
	}
	if elapsed := time.Since(start); elapsed > testLimit+time.Second {
		t.Errorf("the hung worker held its message for %s, want at most %s", elapsed, testLimit+time.Second)
	}
	if got := f.processing(hanging); got.state != "unreadable" || got.seq != 1 {
		t.Errorf("the message is %+v, want unreadable with processed_seq 1", got)
	}
	f.expectOnlyProblem(hanging, "took_too_long")
	if row := f.row(hanging); row.Summary != nil || !row.ListDateIsArchiveTime {
		t.Errorf("row %+v, want no summary and the archive time", row)
	}
	if exits := f.workerStops(hanging); !slices.Equal(exits, []string{"signal: killed"}) {
		t.Errorf("logged worker stops %q, want one with signal: killed", exits)
	}

	// A new worker took the hung one's place and reads the next message.
	waitUntilGone(t, hung[0])
	next := f.importFixture("html-only.eml")
	f.runOnce(f.processor)
	if got := f.processing(next); got.state != "processed" {
		t.Errorf("the next message is %s, want processed", got.state)
	}
}

func TestWorkerDiesTwice(t *testing.T) {
	t.Run("die", func(t *testing.T) {
		f := newProcessingFixtureWith(t, 1, testLimit, modeWorkerCommand("", "die"))
		id := f.importFixture("plain-text.eml")
		f.runOnce(f.processor)
		if got := f.processing(id); got.state != "unreadable" {
			t.Errorf("the message is %s, want unreadable", got.state)
		}
		f.expectOnlyProblem(id, "malformed_structure")
		if exits := f.workerStops(id); !slices.Equal(exits, []string{"exit status 7", "exit status 7"}) {
			t.Errorf("logged worker stops %q, want two with exit status 7", exits)
		}
	})

	t.Run("die-once", func(t *testing.T) {
		marker := filepath.Join(t.TempDir(), "died")
		f := newProcessingFixtureWith(t, 1, testLimit, modeWorkerCommand(marker, "die-once"))
		id := f.importFixture("plain-text.eml")
		f.runOnce(f.processor)
		if got := f.processing(id); got.state != "processed" {
			t.Errorf("the message is %s, want processed on the second try", got.state)
		}
		if exits := f.workerStops(id); !slices.Equal(exits, []string{"exit status 7"}) {
			t.Errorf("logged worker stops %q, want one with exit status 7", exits)
		}
	})
}

func TestLimitValues(t *testing.T) {
	if MessageLimit != 60*time.Second {
		t.Errorf("MessageLimit is %s, want 60 s", MessageLimit)
	}
	if BackgroundWorkers != 2 || InteractiveWorkers != 2 {
		t.Errorf("%d background and %d interactive workers, want 2 and 2", BackgroundWorkers, InteractiveWorkers)
	}
	if InteractiveWait != 30*time.Second {
		t.Errorf("InteractiveWait is %s, want 30 s", InteractiveWait)
	}
}
