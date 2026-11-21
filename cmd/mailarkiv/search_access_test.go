package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// The search process talks to the server only through its standard input and
// output, so it holds no socket another host account could connect to. Every
// file it has open is a pipe, /dev/null, or a file in the data directory.
func TestSearchProcessHasNoAddress(t *testing.T) {
	dataDir := t.TempDir()
	createAdmin(t, dataDir)
	server := startServer(t, dataDir)
	token := signInOverHTTP(t, server.baseURL, adminAddress)
	importFixture(t, server, token, plainFixture(t, "html-only.eml"))
	// A search that finds the message means the index is open and has
	// committed, so the process holds every file it keeps.
	server.waitForSearch(token, "quartals")

	resolved, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	fdDir := filepath.Join("/proc", strconv.Itoa(server.searchProcess()), "fd")
	entries, err := os.ReadDir(fdDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatalf("%s lists no open files", fdDir)
	}
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		switch {
		case strings.HasPrefix(target, "pipe:"), target == "/dev/null":
		case target == resolved, strings.HasPrefix(target, resolved+"/"):
		default:
			t.Errorf("descriptor %s is %s, want a pipe, /dev/null, or a file under %s", entry.Name(), target, resolved)
		}
	}
}
