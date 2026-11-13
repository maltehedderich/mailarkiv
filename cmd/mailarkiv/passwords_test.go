package main

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// encodedForms returns marker and the ways an encoder could have written it:
// lowercase and uppercase hex, and standard and URL-safe base64 at each of
// the three byte offsets a longer encoded value could hold it at.
func encodedForms(marker string) []string {
	hexForm := hex.EncodeToString([]byte(marker))
	forms := []string{marker, hexForm, strings.ToUpper(hexForm)}
	for _, encoding := range []*base64.Encoding{base64.StdEncoding, base64.URLEncoding} {
		for offset := range 3 {
			encoded := encoding.EncodeToString(append(make([]byte, offset), marker...))
			// The first group of four holds bytes from before the marker,
			// and the last group bytes from after it.
			start := 0
			if offset > 0 {
				start = 4
			}
			forms = append(forms, encoded[start:len(encoded)-4])
		}
	}
	return forms
}

// copyTree copies every regular file under from to the same path under to.
func copyTree(t *testing.T, from, to string) {
	t.Helper()
	err := filepath.WalkDir(from, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(from, path)
		if err != nil {
			return err
		}
		target := filepath.Join(to, relative)
		if entry.IsDir() {
			return os.MkdirAll(target, 0o700)
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, content, 0o600)
	})
	if err != nil {
		t.Fatal(err)
	}
}

// A copy of the data directory taken while the server runs, with the WAL and
// any temp files, holds no mailbox password in any form, and neither does
// the log. Started with another key, the server opens none of them.
func TestNoPasswordInDataDir(t *testing.T) {
	anna := newMailServer(t, mailboxUser, mailboxPassword)
	ben := newMailServer(t, otherMailboxUser, otherMailboxPassword)
	caFile := writeCAs(t, anna, ben)
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	server := startCollecting(t, dataDir, keyPath, caFile, true)
	_, token := attemptSignIn(server, adminAddress, accountPassword)
	ids := []int64{
		server.addSource(token, sourceRequest(t, anna, "INBOX")),
		server.addSource(token, sourceRequestFor(t, ben, "Ben", otherMailboxUser, otherMailboxPassword, "INBOX")),
	}
	for _, id := range ids {
		server.waitForSuccessAfter(token, id, "")
	}

	copied := filepath.Join(t.TempDir(), "copy")
	copyTree(t, dataDir, copied)
	files := map[string][]byte{"the log": []byte(server.stderr())}
	var walFound bool
	err := filepath.WalkDir(copied, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		walFound = walFound || strings.HasSuffix(path, "-wal")
		files[path], err = os.ReadFile(path)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if !walFound {
		t.Errorf("the copy holds no WAL file, so it isn't the running server's: %v", files)
	}

	for _, password := range []string{mailboxPassword, otherMailboxPassword} {
		for _, form := range encodedForms(password) {
			for name, content := range files {
				if bytes.Contains(content, []byte(form)) {
					t.Errorf("%s holds %q, a form of the password %q", name, form, password)
				}
			}
		}
	}

	t.Run("another key opens no password", func(t *testing.T) {
		if code := server.stop(syscall.SIGTERM); code != 0 {
			t.Fatalf("exit code = %d, want 0:\n%s", code, server.stderr())
		}
		started := time.Now()
		restarted := startCollecting(t, dataDir, createKey(t, dataDir), caFile, true)
		_, token := attemptSignIn(restarted, adminAddress, accountPassword)

		_, sources := restarted.listedSources(token)
		checked := time.Now()
		for _, id := range ids {
			source, ok := sources[id]
			if !ok {
				t.Fatalf("source %d isn't listed: %v", id, sources)
			}
			assertPasswordUnreadable(t, source, started, checked)
		}
	})
}
