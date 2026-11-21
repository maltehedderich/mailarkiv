package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"mime"
	"mime/multipart"
	"net/mail"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/opstatus"
	"github.com/maltehedderich/mailarkiv/internal/sqlitedb"
)

// measure starts its own executable as its parse workers, which here is the
// test binary.
func TestMain(m *testing.M) {
	if len(os.Args) == 2 && os.Args[1] == "parse-worker" {
		os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
	}
	os.Exit(m.Run())
}

func TestGeneratorGivesTheSameMessagesForTheSameSeed(t *testing.T) {
	// seed and measure each build the vocabulary, so it must repeat too.
	vocab := newVocabulary()
	first := newGenerator(7, vocab)
	again := newGenerator(7, newVocabulary())
	other := newGenerator(8, vocab)
	for i := range 200 {
		if !bytes.Equal(first.message(i), again.message(i)) {
			t.Fatalf("message %d differs between two generators with seed 7", i)
		}
		if bytes.Equal(first.message(i), other.message(i)) {
			t.Fatalf("message %d is the same for seeds 7 and 8", i)
		}
	}
}

// Generated mail is German and English, about 8 KB on average, with a small
// attachment on one in ten, so the index resembles a small office's.
func TestGeneratedMail(t *testing.T) {
	g := newGenerator(1, newVocabulary())
	const n = 2000
	var total, attachments, german, english int
	ids := map[string]bool{}
	for i := range n {
		raw := g.message(i)
		total += len(raw)
		message, err := mail.ReadMessage(bytes.NewReader(raw))
		if err != nil {
			t.Fatalf("message %d: %v", i, err)
		}
		if _, err := message.Header.AddressList("From"); err != nil {
			t.Errorf("message %d From: %v", i, err)
		}
		if _, err := message.Header.Date(); err != nil {
			t.Errorf("message %d Date: %v", i, err)
		}
		id := message.Header.Get("Message-ID")
		if ids[id] {
			t.Errorf("message %d repeats Message-ID %s", i, id)
		}
		ids[id] = true
		attachments += countAttachments(t, message)
		body := string(raw)
		if strings.Contains(body, " und ") {
			german++
		}
		if strings.Contains(body, " the ") {
			english++
		}
	}
	if mean := total / n; mean < 7000 || mean > 9000 {
		t.Errorf("messages average %d bytes, want about 8 KB", mean)
	}
	if attachments < n*8/100 || attachments > n*12/100 {
		t.Errorf("%d of %d messages have an attachment, want about one in ten", attachments, n)
	}
	if german < n/3 || english < n/3 {
		t.Errorf("%d German and %d English messages of %d, want both languages", german, english, n)
	}
}

func countAttachments(t *testing.T, message *mail.Message) int {
	t.Helper()
	mediaType, params, err := mime.ParseMediaType(message.Header.Get("Content-Type"))
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "multipart/mixed" {
		return 0
	}
	count := 0
	parts := multipart.NewReader(message.Body, params["boundary"])
	for {
		part, err := parts.NextPart()
		if errors.Is(err, io.EOF) {
			return count
		}
		if err != nil {
			t.Fatal(err)
		}
		if disposition, _, _ := mime.ParseMediaType(part.Header.Get("Content-Disposition")); disposition == "attachment" {
			count++
		}
	}
}

func TestSeedAndMeasure(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	results := filepath.Join(t.TempDir(), "README.md")

	var stdout, stderr bytes.Buffer
	if code := run([]string{"seed", "--data-dir", dataDir, "--messages", "50"}, &stdout, &stderr); code != 0 {
		t.Fatalf("seed exited %d: %s", code, stderr.String())
	}
	assertSeeded(t, dataDir, 50)

	stdout.Reset()
	stderr.Reset()
	if code := run([]string{"measure", "--data-dir", dataDir, "--results", results}, &stdout, &stderr); code != 0 {
		t.Fatalf("measure exited %d: %s", code, stderr.String())
	}
	written, err := os.ReadFile(results)
	if err != nil {
		t.Fatal(err)
	}
	section := string(written)
	// Every number the sizing page reports, with a value.
	for _, row := range []string{
		`## Run of \d{4}-\d\d-\d\d`,
		`\| Messages \| 50 \|`,
		`\| Versions \| MailArkiv .+, go1\.[0-9.]+, Tantivy 0\.27\.0 \|`,
		`\| Without prefix words \| \d+ \| [0-9.]+ ms \| [0-9.]+ ms \| 0 \|`,
		`\| With prefix words \| \d+ \| [0-9.]+ ms \| [0-9.]+ ms \| 0 \|`,
		`\| Full rebuild \| [0-9.]+ s \|`,
		`\| Peak allocated memory of the search process \| [1-9][0-9]* (KiB|MiB) \|`,
		`\| Index size \| [1-9][0-9]* (KiB|MiB) \|`,
		`\| 95th percentile under 1 s \| [0-9.]+ ms \| Yes \|`,
		`\| Under 1 GiB allocated by search \| [1-9][0-9]* (KiB|MiB) \| Yes \|`,
	} {
		if !regexp.MustCompile(row).MatchString(section) {
			t.Errorf("the results have no row matching %s:\n%s", row, section)
		}
	}
}

// assertSeeded checks that the archive holds want messages, every message of
// the fixed set among them except unreadable-header, which an import refuses
// and only a collection can store.
func assertSeeded(t *testing.T, dataDir string, want int) {
	t.Helper()
	logger := slog.New(slog.DiscardHandler)
	db, err := sqlitedb.Open(filepath.Join(dataDir, "catalog.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	originals, err := archive.Open(dataDir, db, opstatus.New(logger, time.Now), logger, time.Now)
	if err != nil {
		t.Fatal(err)
	}
	page, err := originals.List(context.Background(), 1)
	if err != nil {
		t.Fatal(err)
	}
	if page.Total != want {
		t.Fatalf("the archive holds %d messages, want %d", page.Total, want)
	}
	stored := map[string]bool{}
	for _, message := range page.Items {
		stored[message.ContentHash] = true
	}
	fixed, err := filepath.Glob(filepath.Join("..", "..", "..", "web", "testdata", "searchset", "*.eml"))
	if err != nil || len(fixed) == 0 {
		t.Fatalf("no fixed set found: %v", err)
	}
	for _, path := range fixed {
		if filepath.Base(path) == "unreadable-header.eml" {
			continue
		}
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		hash := sha256.Sum256(content)
		if !stored[hex.EncodeToString(hash[:])] {
			t.Errorf("%s isn't in the archive", filepath.Base(path))
		}
	}
}
