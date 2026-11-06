package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
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

// attemptSignIn returns the answer's status and, on success, the token.
func attemptSignIn(server *serverProcess, address, password string) (status int, token string) {
	server.t.Helper()
	body := fmt.Sprintf(`{"email":%q,"password":%q}`, address, password)
	response, _ := server.request(http.MethodPost, "/api/session", body, "")
	for _, cookie := range response.Cookies() {
		if cookie.Name == "__Host-mailarkiv_session" {
			token = cookie.Value
		}
	}
	return response.StatusCode, token
}

// FR-004, FR-005, risk 9, User Story 2 scenario 7: set-password works while the
// server runs, ends the account's sessions at once, and lifts a sign-in block.
func TestAccountSetPassword(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "archive")
	if code, _, stderr := runAccount(t, accountPassword+"\n", "account", "create", "--data-dir", dataDir, "Admin@example.test"); code != 0 {
		t.Fatalf("account create: exit %d, stderr %q", code, stderr)
	}
	server := startServer(t, dataDir)

	setPassword := func(t *testing.T, password, address string) {
		t.Helper()
		code, stdout, stderr := runAccount(t, password+"\n", "account", "set-password", "--data-dir", dataDir, address)
		if code != 0 {
			t.Fatalf("exit = %d, want 0; stderr %q", code, stderr)
		}
		if stderr != "" {
			t.Errorf("stderr = %q, want nothing", stderr)
		}
		if strings.Count(stdout, "\n") != 1 || !strings.HasSuffix(stdout, "\n") || strings.TrimSpace(stdout) == "" {
			t.Errorf("stdout = %q, want one line", stdout)
		}
	}

	t.Run("ends the account's session at once", func(t *testing.T) {
		status, token := attemptSignIn(server, "admin@example.test", accountPassword)
		if status != http.StatusNoContent || token == "" {
			t.Fatalf("sign in = %d, want 204 with a session", status)
		}
		if response, body := server.request(http.MethodGet, "/api/session", "", token); response.StatusCode != http.StatusOK {
			t.Fatalf("GET /api/session = %d %s, want 200", response.StatusCode, body)
		}

		setPassword(t, "a brand new password", "ADMIN@example.test")

		response, body := server.request(http.MethodGet, "/api/session", "", token)
		if response.StatusCode != http.StatusUnauthorized || body != `{"error":"no_session"}` {
			t.Fatalf("GET /api/session after set-password = %d %q, want 401 no_session", response.StatusCode, body)
		}
		if status, _ := attemptSignIn(server, "admin@example.test", accountPassword); status != http.StatusUnauthorized {
			t.Errorf("sign in with the old password = %d, want 401", status)
		}
		if status, _ := attemptSignIn(server, "admin@example.test", "a brand new password"); status != http.StatusNoContent {
			t.Errorf("sign in with the new password = %d, want 204", status)
		}
	})

	t.Run("lets a blocked address sign in at once", func(t *testing.T) {
		for i := 1; i <= 10; i++ {
			if status, _ := attemptSignIn(server, "admin@example.test", "wrong horse battery"); status != http.StatusUnauthorized {
				t.Fatalf("failure %d = %d, want 401", i, status)
			}
		}
		if status, _ := attemptSignIn(server, "admin@example.test", "a brand new password"); status != http.StatusTooManyRequests {
			t.Fatalf("sign in when blocked = %d, want 429", status)
		}

		setPassword(t, "a third password here", "admin@example.test")

		if status, token := attemptSignIn(server, "admin@example.test", "a third password here"); status != http.StatusNoContent || token == "" {
			t.Fatalf("sign in after set-password = %d, want 204 with a session", status)
		}
	})

	before := storedAccounts(t, dataDir)
	refusals := []struct {
		name    string
		stdin   string
		address string
		stderr  string
	}{
		{"no account", "a valid long password\n", "nobody@example.test", "No account has that email address.\n"},
		{"11-character password", "elevenchars\n", "admin@example.test", "Passwords must have at least 12 characters.\n"},
		{"no password", "", "admin@example.test", "Passwords must have at least 12 characters.\n"},
		{"invalid address", "a valid long password\n", "admin.example.test", "That email address isn't valid.\n"},
	}
	for _, tt := range refusals {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runAccount(t, tt.stdin, "account", "set-password", "--data-dir", dataDir, tt.address)

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

	if status, _ := attemptSignIn(server, "admin@example.test", "a third password here"); status != http.StatusNoContent {
		t.Errorf("sign in after the refusals = %d, want 204: a refusal changed the password", status)
	}
}

// Contract: the password is never an argument, and set-password doesn't create
// a data directory.
func TestAccountSetPasswordBadArguments(t *testing.T) {
	dataDir := t.TempDir()
	tests := []struct {
		name string
		args []string
	}{
		{"password as an extra argument", []string{"account", "set-password", "--data-dir", dataDir, "admin@example.test", accountPassword}},
		{"no address", []string{"account", "set-password", "--data-dir", dataDir}},
		{"no data directory", []string{"account", "set-password", "admin@example.test"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, stdout, stderr := runAccount(t, accountPassword+"\n", tt.args...)
			if code != 2 {
				t.Errorf("exit = %d, want 2", code)
			}
			if !strings.Contains(stderr, "mailarkiv account set-password --data-dir DIR EMAIL") {
				t.Errorf("stderr = %q, want the usage text", stderr)
			}
			if stdout != "" {
				t.Errorf("stdout = %q, want nothing", stdout)
			}
		})
	}

	t.Run("a missing data directory", func(t *testing.T) {
		missing := filepath.Join(dataDir, "missing")
		code, stdout, stderr := runAccount(t, accountPassword+"\n", "account", "set-password", "--data-dir", missing, "admin@example.test")
		if code != 1 {
			t.Errorf("exit = %d, want 1; stderr %q", code, stderr)
		}
		if stdout != "" {
			t.Errorf("stdout = %q, want nothing", stdout)
		}
		if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("set-password created the data directory: %v", err)
		}
	})
}
