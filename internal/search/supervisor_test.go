package search

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"testing/synctest"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/opstatus"
)

func TestProcessStartsWithNothingButTheDataDirectory(t *testing.T) {
	program := searchProgram(t)
	f := newFixture(t, program)
	f.runSearch()
	f.waitFor("the search process", func() bool { return f.module.pid() != 0 })
	pid := f.module.pid()

	proc := filepath.Join("/proc", strconv.Itoa(pid))
	if cmdline, err := os.ReadFile(filepath.Join(proc, "cmdline")); err != nil || string(cmdline) != program+"\x00" {
		t.Errorf("command line %q, %v; want only %s", cmdline, err, program)
	}
	if environ, err := os.ReadFile(filepath.Join(proc, "environ")); err != nil || len(environ) != 0 {
		t.Errorf("environment %q, %v; want it empty", environ, err)
	}
	cwd, err := os.Readlink(filepath.Join(proc, "cwd"))
	want, _ := filepath.EvalSymlinks(f.dataDir)
	if err != nil || cwd != want {
		t.Errorf("working directory %q, %v; want %s", cwd, err, want)
	}
	// The kernel doesn't show the parent-death signal, so read it where the
	// command is made.
	cmd := f.module.processCommand()
	if cmd.SysProcAttr == nil || cmd.SysProcAttr.Pdeathsig != syscall.SIGKILL {
		t.Errorf("SysProcAttr %+v, want Pdeathsig SIGKILL", cmd.SysProcAttr)
	}

	started := f.logged("search process started")
	if len(started) != 1 || started[0]["restarts"] != float64(0) || started[0]["level"] != "INFO" {
		t.Errorf("start log %v, want one entry with restarts 0", started)
	}
	if got := f.module.State().Search; got != SearchAvailable {
		t.Errorf("search is %s, want available", got)
	}
}

func TestProgramThatDoesNotStart(t *testing.T) {
	notExecutable := filepath.Join(t.TempDir(), "mailarkiv-search")
	if err := os.WriteFile(notExecutable, []byte("#!/bin/sh\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name    string
		program string
		mode    string
		want    opstatus.Cause
	}{
		{"missing", filepath.Join(t.TempDir(), "mailarkiv-search"), "", opstatus.CauseMissing},
		{"not executable", notExecutable, "", opstatus.CauseNotExecutable},
		{"bad hello", "", "bad_hello", opstatus.CauseBadHello},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := newFixture(t, tt.program)
			if tt.mode != "" {
				f.module.command = fakeSearchCommand(tt.mode)
			}
			f.runSearch()
			f.waitFor("search_process_did_not_start", func() bool {
				return len(f.failures(opstatus.SearchProcessDidNotStart)) > 0
			})
			event := f.failures(opstatus.SearchProcessDidNotStart)[0]
			if event.Detail == nil || event.Detail.Cause != tt.want {
				t.Errorf("detail %+v, want cause %s", event.Detail, tt.want)
			}
			if state := f.module.State(); state.Search != SearchUnavailable || state.Indexing != IndexingStopped {
				t.Errorf("state %+v, want unavailable and stopped", state)
			}

			// Search names the cause and, once the wait has begun, the next start.
			f.waitFor("a next start time", func() bool {
				_, err := f.module.Search(context.Background(), Query{Words: "quartals", Page: 1})
				unavailable, ok := errors.AsType[*UnavailableError](err)
				if !ok {
					t.Fatalf("search answered %v, want UnavailableError", err)
				}
				if unavailable.Cause != opstatus.SearchProcessDidNotStart {
					t.Fatalf("cause %s, want search_process_did_not_start", unavailable.Cause)
				}
				return unavailable.NextStartAt != nil
			})
		})
	}
}

func TestKilledProcessIsStartedAgain(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	f.runSearch()
	f.waitFor("the search process", func() bool { return f.module.pid() != 0 })
	first := f.module.pid()
	if err := syscall.Kill(first, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}

	f.waitFor("search_process_stopped", func() bool { return len(f.failures(opstatus.SearchProcessStopped)) == 1 })
	event := f.failures(opstatus.SearchProcessStopped)[0]
	if event.Detail == nil || event.Detail.Signal == nil || *event.Detail.Signal != int(syscall.SIGKILL) || event.Detail.ExitStatus != nil {
		t.Errorf("detail %+v, want signal 9", event.Detail)
	}
	f.waitFor("a new search process", func() bool {
		pid := f.module.pid()
		return pid != 0 && pid != first
	})
	started := f.logged("search process started")
	if len(started) != 2 || started[1]["restarts"] != float64(1) {
		t.Errorf("start log %v, want a second start with restarts 1", started)
	}
	f.waitFor("search available again", func() bool { return f.module.State().Search == SearchAvailable })
	var states []any
	for _, entry := range f.logged("search state") {
		if len(states) == 0 || states[len(states)-1] != entry["state"] {
			states = append(states, entry["state"])
		}
	}
	if want := []any{"available", "unavailable", "available"}; !slices.Equal(states, want) {
		t.Errorf("search state lines went %v, want %v", states, want)
	}
}

// A real panic line is logged with its location alone, and the panic is a
// stop like any other.
func TestPanicLineIsLogged(t *testing.T) {
	f := newFixture(t, "")
	setFaults := f.switchableFaults(faultsSearchProgram(t))
	setFaults("on-open=panic")
	f.fastRestarts()
	f.runSearch()
	f.waitFor("the panic line", func() bool { return len(f.logged("search process panic")) > 0 })
	setFaults("")

	entry := f.logged("search process panic")[0]
	location, _ := entry["location"].(string)
	if !regexp.MustCompile(`^src/faults\.rs:[0-9]+$`).MatchString(location) || entry["level"] != "ERROR" {
		t.Errorf("log entry %v, want an error with the location in src/faults.rs", entry)
	}
	f.waitFor("search_process_stopped", func() bool { return len(f.failures(opstatus.SearchProcessStopped)) > 0 })
	stopped := f.failures(opstatus.SearchProcessStopped)[0]
	if stopped.Detail == nil || stopped.Detail.Signal == nil || *stopped.Detail.Signal != int(syscall.SIGABRT) {
		t.Errorf("stopped event %+v, want signal 6", stopped)
	}
	if ignored := f.logged("search process output ignored"); len(ignored) != 0 {
		t.Errorf("logged ignored output %v after a panic line alone", ignored)
	}
}

// A log frame is logged with its level and target. A level or target that
// isn't a word the program sends is logged as unknown.
func TestLogFramesAreLogged(t *testing.T) {
	f := newFixture(t, "")
	f.module.command = fakeSearchCommand("log")
	f.runSearch()
	f.waitFor("two log lines", func() bool { return len(f.logged("search process log")) == 2 })

	entries := f.logged("search process log")
	want := []map[string]any{
		{"level": "WARN", "msg": "search process log", "log_level": "warn", "target": "tantivy::postings::postings_writer"},
		{"level": "WARN", "msg": "search process log", "log_level": "unknown", "target": "unknown"},
	}
	for i, entry := range entries {
		delete(entry, "time")
		if !maps.Equal(entry, want[i]) {
			t.Errorf("log line %d = %v, want %v", i, entry, want[i])
		}
	}
	if strings.Contains(f.log.String(), "Quartalsbericht") {
		t.Error("the log holds text from a log frame")
	}
}

func TestStandardErrorLinesAreOnlyCounted(t *testing.T) {
	f := newFixture(t, "")
	f.module.command = fakeSearchCommand("stderr")
	f.runSearch()
	f.waitFor("the ignored output", func() bool { return len(f.logged("search process output ignored")) > 0 })

	entry := f.logged("search process output ignored")[0]
	if entry["lines"] != float64(3) || entry["level"] != "WARN" {
		t.Errorf("log entry %v, want 3 lines at WARN", entry)
	}
	if strings.Contains(f.log.String(), "Quartalsbericht") {
		t.Error("the log holds text the search process wrote")
	}
	// The stop is recorded just after the ignored output is logged.
	f.waitFor("search_process_stopped", func() bool { return len(f.failures(opstatus.SearchProcessStopped)) > 0 })
	stopped := f.failures(opstatus.SearchProcessStopped)
	if stopped[0].Detail.ExitStatus == nil || *stopped[0].Detail.ExitStatus != 0 {
		t.Errorf("stopped events %+v, want exit status 0", stopped)
	}
}

func TestRunStopsTheProcess(t *testing.T) {
	f := newFixture(t, searchProgram(t))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		f.module.Run(ctx)
	}()
	f.waitFor("the search process", func() bool { return f.module.pid() != 0 })
	pid := f.module.pid()

	stopping := time.Now()
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("Run didn't return within 10 s")
	}
	// Closing standard input ends it well before the kill at 5 s.
	if elapsed := time.Since(stopping); elapsed > 2*time.Second {
		t.Errorf("stopping took %s", elapsed)
	}
	if err := syscall.Kill(pid, 0); !errors.Is(err, syscall.ESRCH) {
		t.Errorf("process %d is still there: %v", pid, err)
	}
	if got := f.failures(opstatus.SearchProcessStopped); len(got) != 0 {
		t.Errorf("a requested stop recorded %+v", got)
	}
}

// The wait before each start doubles from 1 s to 60 s, and a process that ran
// for 10 minutes resets it. The failed starts are one event that repeats.
func TestRestartWait(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		logger, log := newLogger()
		status := opstatus.New(logger, time.Now)
		m := New(Config{
			DataDir: t.TempDir(), Program: filepath.Join(t.TempDir(), "missing"),
			Status: status, Logger: logger, Now: time.Now,
		})
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			m.Run(ctx)
		}()
		// Nine starts, and less than the tenth wait.
		time.Sleep((1 + 2 + 4 + 8 + 16 + 32 + 60 + 60 + 30) * time.Second)
		cancel()
		<-done

		var starts []time.Time
		for _, entry := range log.entries(t) {
			if entry["msg"] != "search failure" {
				continue
			}
			at, err := time.Parse(time.RFC3339Nano, entry["time"].(string))
			if err != nil {
				t.Fatal(err)
			}
			starts = append(starts, at)
		}
		failures := status.Snapshot().Failures
		if len(failures) != 1 || failures[0].Repeats != 9 || len(starts) != 9 ||
			!failures[0].At.Equal(starts[0]) || !failures[0].LastAt.Equal(starts[8]) {
			t.Errorf("listed %+v for starts %v, want one event repeated 9 times from the first to the last", failures, starts)
		}
		var waits []time.Duration
		for i := 1; i < len(starts); i++ {
			waits = append(waits, starts[i].Sub(starts[i-1]))
		}
		want := []time.Duration{1, 2, 4, 8, 16, 32, 60, 60}
		for i := range want {
			want[i] *= time.Second
		}
		if !slices.Equal(waits, want) {
			t.Errorf("waits %v, want %v", waits, want)
		}
	})

	var wait restartWait
	for range 8 {
		wait.next(time.Second)
	}
	if got := wait.next(9 * time.Minute); got != time.Minute {
		t.Errorf("after a 9 minute run the wait is %s, want 60 s", got)
	}
	if got := wait.next(10 * time.Minute); got != time.Second {
		t.Errorf("after a 10 minute run the wait is %s, want 1 s", got)
	}
	if got := wait.next(0); got != 2*time.Second {
		t.Errorf("the next wait is %s, want 2 s", got)
	}
}
