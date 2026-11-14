package main

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The walking skeleton: add a source over HTTP, let the first sync run, and
// read every message back byte for byte.
func TestCollectFirstSync(t *testing.T) {
	double, caFile := newProcessDouble(t)
	if err := double.AddFolder("Gesendete Objekte"); err != nil {
		t.Fatal(err)
	}
	received := time.Date(2026, 11, 3, 9, 0, 0, 0, time.UTC)
	folderOf := map[string]string{}
	for i, message := range []struct {
		folder string
		body   string
	}{
		{"INBOX", "From: ben@example.test\r\nSubject: First\r\n\r\nFirst message\r\n"},
		{"INBOX", "From: ben@example.test\r\nSubject: Second\r\n\r\nLF in the body\nonly\n"},
		{"Gesendete Objekte", "From: anna@example.test\r\nSubject: Sent\r\n\r\nNo final line break"},
	} {
		if _, err := double.AddMessage(message.folder, []byte(message.body), received.Add(time.Duration(i)*time.Hour)); err != nil {
			t.Fatal(err)
		}
		folderOf[contentHash([]byte(message.body))] = message.folder
	}
	dataDir := filepath.Join(t.TempDir(), "data")
	keyPath := createKey(t, dataDir)
	createAdmin(t, dataDir)
	server := startCollecting(t, dataDir, keyPath, caFile)
	_, token := attemptSignIn(server, adminAddress, accountPassword)

	response, body := server.request(http.MethodPost, "/api/sources", sourceRequest(t, double, "INBOX", "Gesendete Objekte"), token)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("POST /api/sources = %d %s, want 201", response.StatusCode, body)
	}
	var added sourceAnswer
	if err := json.Unmarshal([]byte(body), &added); err != nil {
		t.Fatal(err)
	}
	first := server.waitForSuccessAfter(token, added.ID, "")
	if first.LastSuccess.Archived != len(folderOf) {
		t.Errorf("the first sync archived %d, want %d", first.LastSuccess.Archived, len(folderOf))
	}

	response, body = server.request(http.MethodGet, "/api/messages", "", token)
	if response.StatusCode != http.StatusOK {
		t.Fatalf("GET /api/messages = %d %s", response.StatusCode, body)
	}
	var page struct {
		Items []struct {
			ID          int64  `json:"id"`
			ContentHash string `json:"content_hash"`
			Latest      struct {
				SourceType string `json:"source_type"`
				Folder     string `json:"folder"`
			} `json:"latest"`
		} `json:"items"`
		Total int `json:"total"`
	}
	if err := json.Unmarshal([]byte(body), &page); err != nil {
		t.Fatal(err)
	}
	if page.Total != len(folderOf) {
		t.Fatalf("the list has %d messages, want %d: %s", page.Total, len(folderOf), body)
	}
	for _, item := range page.Items {
		folder, ok := folderOf[item.ContentHash]
		if !ok {
			t.Errorf("message %d isn't one the double holds", item.ID)
			continue
		}
		if item.Latest.SourceType != "imap_collection" || item.Latest.Folder != folder {
			t.Errorf("message %d latest = %+v, want a collection from %s", item.ID, item.Latest, folder)
		}
		response, original := server.request(http.MethodGet, "/api/messages/"+strconv.FormatInt(item.ID, 10)+"/original", "", token)
		if response.StatusCode != http.StatusOK || contentHash([]byte(original)) != item.ContentHash {
			t.Errorf("message %d downloads with status %d and hash %s, want 200 and %s",
				item.ID, response.StatusCode, contentHash([]byte(original)), item.ContentHash)
		}
	}

	t.Run("a restart syncs at start", func(t *testing.T) {
		if code := server.stop(syscall.SIGTERM); code != 0 {
			t.Fatalf("exit code = %d, want 0:\n%s", code, server.stderr())
		}
		before := doubleLogins(double)

		restarted := startCollecting(t, dataDir, keyPath, caFile)
		_, token := attemptSignIn(restarted, adminAddress, accountPassword)
		again := restarted.waitForSuccessAfter(token, added.ID, first.LastSuccess.At)

		if got := doubleLogins(double); got != before+1 {
			t.Errorf("LOGIN count = %d, want %d", got, before+1)
		}
		if again.LastSuccess.Archived != 0 {
			t.Errorf("the sync at start archived %d, want 0", again.LastSuccess.Archived)
		}
	})
}

func TestUsageIncludesCollection(t *testing.T) {
	_, stderr := runToExit(t)
	if !strings.Contains(stderr, "Usage:") {
		t.Fatalf("stderr holds no usage text: %q", stderr)
	}
	for _, command := range []string{"mailarkiv key create --data-dir DIR PATH", "--key-file PATH"} {
		if !strings.Contains(stderr, command) {
			t.Errorf("the usage text is missing %q:\n%s", command, stderr)
		}
	}
}
