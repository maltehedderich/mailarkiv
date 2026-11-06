package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

const accountPassword = "correct horse battery"

// runAccount runs the binary with stdin as the password input. A pipe isn't a
// terminal, so the command reads one line and asks nothing.
func runAccount(t *testing.T, stdin string, args ...string) (exitCode int, stdout, stderr string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), processTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, serverBinary, args...)
	cmd.Stdin = strings.NewReader(stdin)
	var out, errOut bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errOut

	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("mailarkiv %v didn't exit within %s:\n%s", args, processTimeout, errOut.String())
	}
	var exit *exec.ExitError
	if err != nil && !errors.As(err, &exit) {
		t.Fatalf("mailarkiv %v: %v", args, err)
	}
	return cmd.ProcessState.ExitCode(), out.String(), errOut.String()
}

// storedAccounts reads the account table: address to password hash.
func storedAccounts(t *testing.T, dataDir string) map[string]string {
	t.Helper()
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	rows, err := db.Reader.QueryContext(context.Background(), "SELECT email, password_hash FROM account")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	accounts := map[string]string{}
	for rows.Next() {
		var email, hash string
		if err := rows.Scan(&email, &hash); err != nil {
			t.Fatal(err)
		}
		accounts[email] = hash
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return accounts
}

func TestAccountCreate(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "new", "archive")

	t.Run("creates the data directory, the catalog, and the account", func(t *testing.T) {
		code, stdout, stderr := runAccount(t, accountPassword+"\n", "account", "create", "--data-dir", dataDir, "Admin@example.test")

		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr %q", code, stderr)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want nothing", stderr)
		}
		if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") || strings.TrimSpace(stdout) == "" {
			t.Errorf("stdout = %q, want one line", stdout)
		}
		info, err := os.Stat(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != 0o700 {
			t.Errorf("data directory mode = %o, want 700", got)
		}
		accounts := storedAccounts(t, dataDir)
		if len(accounts) != 1 || accounts["admin@example.test"] == "" {
			t.Fatalf("accounts = %v, want only admin@example.test", accounts)
		}
	})

	before := storedAccounts(t, dataDir)
	refusals := []struct {
		name    string
		stdin   string
		address string
		stderr  string
	}{
		{"invalid address", accountPassword + "\n", "admin.example.test", "That email address isn't valid.\n"},
		{"existing address with other capital letters", "another password here\n", "ADMIN@Example.TEST", "An account with that email address already exists.\n"},
		{"11-character password", "elevenchars\n", "new@example.test", "Passwords must have at least 12 characters.\n"},
		{"no password", "", "new@example.test", "Passwords must have at least 12 characters.\n"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runAccount(t, tt.stdin, "account", "create", "--data-dir", dataDir, tt.address)

			if code != 1 {
				t.Errorf("exit = %d, want 1", code)
			}
			if stderr != tt.stderr {
				t.Errorf("stderr = %q, want %q", stderr, tt.stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
			after := storedAccounts(t, dataDir)
			if len(after) != len(before) || after["admin@example.test"] != before["admin@example.test"] {
				t.Fatalf("accounts changed: %v, then %v", before, after)
			}
		})
	}

	t.Run("a 12-character password is enough", func(t *testing.T) {
		code, _, stderr := runAccount(t, "twelve chars\n", "account", "create", "--data-dir", dataDir, "twelve@example.test")
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr %q", code, stderr)
		}
	})
}

// The password is never an argument (contract). Each of these is a usage
// error, and none creates an account.
func TestAccountCreateBadArguments(t *testing.T) {
	dataDir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{"password as an extra argument", []string{"account", "create", "--data-dir", dataDir, "admin@example.test", accountPassword}},
		{"no address", []string{"account", "create", "--data-dir", dataDir}},
		{"no data directory", []string{"account", "create", "admin@example.test"}},
		{"no subcommand", []string{"account"}},
		{"unknown subcommand", []string{"account", "delete", "--data-dir", dataDir, "admin@example.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runAccount(t, accountPassword+"\n", tt.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, "mailarkiv account create --data-dir DIR EMAIL") {
				t.Errorf("stderr = %q, want the usage text", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}
	if _, err := os.Stat(filepath.Join(dataDir, "catalog.db")); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a usage error created the catalog: %v", err)
	}
}

// Contract: the command refuses to run for anyone but the data directory's
// owner, so sudo can't leave root-owned catalog files behind.
func TestCheckOwner(t *testing.T) {
	tests := []struct {
		name       string
		dirUID     uint32
		currentUID int
		allowed    bool
	}{
		{"the owner", 1000, 1000, true},
		{"root on a user's directory", 1000, 0, false},
		{"a user on root's directory", 0, 1000, false},
		{"another user", 1000, 1001, false},
		{"root on root's directory", 0, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := checkOwner(tt.dirUID, tt.currentUID)
			if tt.allowed && err != nil {
				t.Fatalf("checkOwner(%d, %d) = %v, want nil", tt.dirUID, tt.currentUID, err)
			}
			if !tt.allowed && err == nil {
				t.Fatalf("checkOwner(%d, %d) = nil, want an error", tt.dirUID, tt.currentUID)
			}
		})
	}
}
