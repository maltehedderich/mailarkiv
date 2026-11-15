package main

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
)

// keyMarker sits in invalid key files, so a log that quotes one is easy to
// find.
const keyMarker = "MARKER-KEY-7d2e"

// keyLine is a key line holding raw as its key bytes, whatever their number.
func keyLine(raw []byte) string {
	return "mailarkiv-key-v1:" + base64.StdEncoding.EncodeToString(raw) + "\n"
}

// dirEntries lists the names in dir.
func dirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

// writeKeyFile writes content to a new file outside any data directory.
func writeKeyFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "key")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// Contract: serve refuses a misplaced, missing, unreadable, or invalid key
// before it takes the lock, with cause key and the reason, and the log never
// quotes the file.
func TestServeKeyRefusals(t *testing.T) {
	thirtyOne := []byte(keyMarker + "-0123456789abcdef")[:31]
	tests := []struct {
		name string
		// key makes the key file and returns its path. The data directory
		// doesn't exist yet, unless key makes it.
		key    func(t *testing.T, dataDir string) string
		reason string
		// secret is part of the file that must not reach the log.
		secret string
	}{
		{
			name: "a key in the data directory",
			key: func(t *testing.T, dataDir string) string {
				if err := os.Mkdir(dataDir, 0o700); err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(dataDir, "key")
				if err := passwordkey.Create(path); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "inside_data_dir",
		},
		{
			name:   "no file",
			key:    func(t *testing.T, _ string) string { return filepath.Join(t.TempDir(), "key") },
			reason: "missing",
		},
		{
			name: "a directory at the path",
			key: func(t *testing.T, _ string) string {
				path := filepath.Join(t.TempDir(), "key")
				if err := os.Mkdir(path, 0o700); err != nil {
					t.Fatal(err)
				}
				return path
			},
			reason: "unreadable",
		},
		{
			name:   "1,025 bytes",
			key:    func(t *testing.T, _ string) string { return writeKeyFile(t, strings.Repeat(keyMarker, 69)[:1025]) },
			reason: "invalid",
			secret: keyMarker,
		},
		{
			name: "a wrong prefix",
			key: func(t *testing.T, _ string) string {
				return writeKeyFile(t, keyMarker+":"+base64.StdEncoding.EncodeToString(make([]byte, 32))+"\n")
			},
			reason: "invalid",
			secret: keyMarker,
		},
		{
			name:   "31 key bytes",
			key:    func(t *testing.T, _ string) string { return writeKeyFile(t, keyLine(thirtyOne)) },
			reason: "invalid",
			secret: base64.StdEncoding.EncodeToString(thirtyOne),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dataDir := filepath.Join(t.TempDir(), "data")
			path := tt.key(t, dataDir)
			var before []string
			if _, err := os.Stat(dataDir); err == nil {
				before = dirEntries(t, dataDir)
			}

			code, stderr := runToExit(t, "serve", "--data-dir", dataDir, "--listen", "127.0.0.1:0", "--key-file", path)

			if code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			entry := startupFailure(t, stderr)
			if entry["msg"] != "startup failed" || entry["cause"] != "key" || entry["reason"] != tt.reason {
				t.Errorf("log entry = %v, want startup failed with cause key and reason %s", entry, tt.reason)
			}
			if tt.reason == "invalid" && entry["error"] != nil {
				t.Errorf("the entry for an invalid key has an error field: %v", entry["error"])
			}
			if tt.secret != "" && strings.Contains(stderr, tt.secret) {
				t.Errorf("the log quotes the key file:\n%s", stderr)
			}
			if after := dirEntries(t, dataDir); !slices.Equal(after, before) {
				t.Errorf("the data directory holds %q, want %q", after, before)
			}
		})
	}
}
