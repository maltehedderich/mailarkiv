package main

import (
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// startupFailure finds the log entry for a refused start. FR-026 and the CLI
// contract require the log to name the cause.
func startupFailure(t *testing.T, stderr string) map[string]any {
	t.Helper()
	for _, entry := range logEntries(t, stderr) {
		if entry["level"] == "ERROR" && entry["cause"] != nil {
			return entry
		}
	}
	t.Fatalf("no error entry names a cause:\n%s", stderr)
	return nil
}

func TestServeStartup(t *testing.T) {
	t.Run("creates the data directory and catalog closed to other users", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "new", "archive")
		startServer(t, dataDir)

		info, err := os.Stat(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("data directory mode = %o, want 700", got)
		}

		info, err = os.Stat(filepath.Join(dataDir, "catalog.db"))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("catalog.db mode = %o, want 600", got)
		}

		// TestMain sets a 022 umask, so a file the binary didn't protect shows here.
		err = filepath.WalkDir(dataDir, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if info.Mode().Perm()&0o077 != 0 {
				t.Errorf("%s has mode %o, open to other users", path, info.Mode().Perm())
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	})

	t.Run("bad arguments exit 2 and print usage", func(t *testing.T) {
		dataDir := t.TempDir()
		tests := []struct {
			name string
			args []string
		}{
			{"no command", nil},
			{"unknown command", []string{"explode"}},
			{"no data directory", []string{"serve"}},
			{"empty data directory", []string{"serve", "--data-dir", ""}},
			{"unknown option", []string{"serve", "--data-dir", dataDir, "--nope"}},
			{"extra argument", []string{"serve", "--data-dir", dataDir, "extra"}},
		}
		for _, tt := range tests {
			t.Run(tt.name, func(t *testing.T) {
				code, stderr := runToExit(t, tt.args...)
				if code != 2 {
					t.Errorf("exit code = %d, want 2", code)
				}
				if !strings.Contains(stderr, "Usage:") {
					t.Errorf("stderr holds no usage text: %q", stderr)
				}
			})
		}
	})

	t.Run("a taken port exits 1 and the log names it", func(t *testing.T) {
		taken, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer taken.Close()

		code, stderr := runToExit(t, "serve", "--data-dir", t.TempDir(), "--listen", taken.Addr().String())

		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if got := startupFailure(t, stderr)["cause"]; got != "port" {
			t.Errorf("cause = %v, want port", got)
		}
	})

	t.Run("a database newer than the binary exits 1 and the log names it", func(t *testing.T) {
		dataDir := t.TempDir()
		db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Writer.Exec(`PRAGMA user_version = 1000000`); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}

		code, stderr := runToExit(t, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0")

		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		entry := startupFailure(t, stderr)
		if entry["cause"] != "catalog" {
			t.Errorf("cause = %v, want catalog", entry["cause"])
		}
		if text := fmt.Sprint(entry["error"]); !strings.Contains(text, "newer") {
			t.Errorf("error = %q, want it to say the database is newer", text)
		}
	})

	t.Run("a data directory that can't be created exits 1 and the log names it", func(t *testing.T) {
		// A plain file in the way fails for every user, including root in a container.
		file := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(file, nil, 0o600); err != nil {
			t.Fatal(err)
		}

		code, stderr := runToExit(t, "serve", "--data-dir", filepath.Join(file, "archive"), "--listen", "127.0.0.1:0")

		if code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if got := startupFailure(t, stderr)["cause"]; got != "storage" {
			t.Errorf("cause = %v, want storage", got)
		}
	})

	for _, signal := range []syscall.Signal{syscall.SIGTERM, syscall.SIGINT} {
		t.Run(fmt.Sprintf("exits 0 on %s", signal), func(t *testing.T) {
			server := startServer(t, t.TempDir())

			if code := server.stop(signal); code != 0 {
				t.Fatalf("exit code = %d, want 0:\n%s", code, server.stderr())
			}
		})
	}

	t.Run("stderr holds only JSON lines with the contract's fields", func(t *testing.T) {
		server := startServer(t, t.TempDir())
		server.stop(syscall.SIGTERM)

		entries := logEntries(t, server.stderr())
		if len(entries) == 0 {
			t.Fatal("the server logged nothing")
		}
		for _, entry := range entries {
			for _, field := range []string{"time", "level", "msg"} {
				if _, present := entry[field]; !present {
					t.Errorf("entry has no %s: %v", field, entry)
				}
			}
		}
	})

	t.Run("reopens its own catalog", func(t *testing.T) {
		dataDir := t.TempDir()
		first := startServer(t, dataDir)
		if code := first.stop(syscall.SIGTERM); code != 0 {
			t.Fatalf("first exit code = %d, want 0", code)
		}

		second := startServer(t, dataDir)
		if code := second.stop(syscall.SIGTERM); code != 0 {
			t.Fatalf("second exit code = %d, want 0:\n%s", code, second.stderr())
		}
	})
}

// This phase's checkpoint, against the real binary.
func TestServeAnswers(t *testing.T) {
	server := startServer(t, t.TempDir())

	wantHeaders := map[string]string{
		"MailArkiv-Response":      "1",
		"Content-Security-Policy": "frame-ancestors 'none'; base-uri 'none'; form-action 'self'",
		"X-Content-Type-Options":  "nosniff",
		"Referrer-Policy":         "same-origin",
	}

	t.Run("an API path without a session", func(t *testing.T) {
		response, body := server.get("/api/messages")

		if response.StatusCode != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", response.StatusCode)
		}
		if body != `{"error":"no_session"}` {
			t.Fatalf("body = %q", body)
		}
		for name, value := range wantHeaders {
			if got := response.Header.Get(name); got != value {
				t.Errorf("%s = %q, want %q", name, got, value)
			}
		}
		if got := response.Header.Get("Cache-Control"); got != "no-store" {
			t.Errorf("Cache-Control = %q, want no-store", got)
		}
	})

	t.Run("the embedded interface", func(t *testing.T) {
		response, body := server.get("/")

		if response.StatusCode != http.StatusOK {
			t.Fatalf("status = %d, want 200", response.StatusCode)
		}
		if !strings.Contains(body, "<html") {
			t.Fatalf("body isn't the application page: %q", body)
		}
		for name, value := range wantHeaders {
			if got := response.Header.Get(name); got != value {
				t.Errorf("%s = %q, want %q", name, got, value)
			}
		}
	})
}

// FR-029: sign-in attempts and successful requests aren't logged, so nothing a
// client sends can reach the log this way.
func TestServeLogsNoRequests(t *testing.T) {
	server := startServer(t, t.TempDir())

	const marker = "MARKERVALUE"
	server.get("/" + marker + "?q=" + marker)
	server.get("/api/" + marker)
	server.stop(syscall.SIGTERM)

	if stderr := server.stderr(); strings.Contains(stderr, marker) {
		t.Fatalf("the log holds a request value:\n%s", stderr)
	}
}
