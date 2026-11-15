package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/passwordkey"
)

const keyInsideData = "The key must be outside the data directory.\n"

// assertNoFile checks that nothing exists at path.
func assertNoFile(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Lstat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("%s exists or can't be checked: %v", path, err)
	}
}

// Contract: key create writes a key only its owner can read, never inside
// the data directory, never over a file, and never into a missing directory.
func TestKeyCreate(t *testing.T) {
	t.Run("creates a key only its owner can read or write", func(t *testing.T) {
		dataDir := filepath.Join(t.TempDir(), "data")
		path := filepath.Join(t.TempDir(), "key")

		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", dataDir, path)

		if code != 0 || stderr != "" {
			t.Fatalf("key create = %d, stderr %q, want 0 and nothing", code, stderr)
		}
		want := "Created the key at " + path + ". Back it up apart from the data directory. " +
			"Without it, every IMAP password must be saved again.\n"
		if stdout != want {
			t.Errorf("stdout = %q, want %q", stdout, want)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Errorf("key mode = %o, want 600", got)
		}
		if _, err := passwordkey.Load(path); err != nil {
			t.Errorf("the key doesn't load: %v", err)
		}
	})

	t.Run("refuses a path inside the data directory", func(t *testing.T) {
		dataDir := t.TempDir()
		path := filepath.Join(dataDir, "key")

		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", dataDir, path)

		if code != 1 || stdout != "" || stderr != keyInsideData {
			t.Errorf("key create = %d, stdout %q, stderr %q, want 1 and %q", code, stdout, stderr, keyInsideData)
		}
		assertNoFile(t, path)
	})

	t.Run("refuses a path through a symbolic link into the data directory", func(t *testing.T) {
		dataDir := t.TempDir()
		link := filepath.Join(t.TempDir(), "link")
		if err := os.Symlink(dataDir, link); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", dataDir, filepath.Join(link, "key"))

		if code != 1 || stdout != "" || stderr != keyInsideData {
			t.Errorf("key create = %d, stdout %q, stderr %q, want 1 and %q", code, stdout, stderr, keyInsideData)
		}
		assertNoFile(t, filepath.Join(dataDir, "key"))
	})

	t.Run("never replaces a file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "key")
		const existing = "an operator's file\n"
		if err := os.WriteFile(path, []byte(existing), 0o600); err != nil {
			t.Fatal(err)
		}

		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", t.TempDir(), path)

		want := "A file already exists at " + path + ". MailArkiv never replaces a key.\n"
		if code != 1 || stdout != "" || stderr != want {
			t.Errorf("key create = %d, stdout %q, stderr %q, want 1 and %q", code, stdout, stderr, want)
		}
		if content, err := os.ReadFile(path); err != nil || string(content) != existing {
			t.Errorf("the file holds %q, %v, want %q", content, err, existing)
		}
	})

	t.Run("doesn't create a missing parent directory", func(t *testing.T) {
		parent := filepath.Join(t.TempDir(), "missing")
		path := filepath.Join(parent, "key")

		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", t.TempDir(), path)

		const prefix = "The key couldn't be written: "
		cause, found := strings.CutPrefix(stderr, prefix)
		if code != 1 || stdout != "" || !found || strings.TrimSpace(cause) == "" {
			t.Errorf("key create = %d, stdout %q, stderr %q, want 1 and %q with a cause", code, stdout, stderr, prefix)
		}
		assertNoFile(t, parent)
	})

	t.Run("a missing path exits 2 with the usage text", func(t *testing.T) {
		code, stdout, stderr := runAccount(t, "", "key", "create", "--data-dir", t.TempDir())

		if code != 2 || stdout != "" || !strings.Contains(stderr, "Usage:") {
			t.Errorf("key create = %d, stdout %q, stderr %q, want 2 and the usage text", code, stdout, stderr)
		}
	})
}
