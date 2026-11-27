package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/maltehedderich/mailarkiv/internal/archive"
)

// flatMessage is a multipart message of n text parts.
func flatMessage(n int) []byte {
	var b strings.Builder
	b.WriteString("From: Viele Teile <teile@example.com>\r\nSubject: Viele Teile\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"flat\"\r\n\r\n")
	for i := 1; i <= n; i++ {
		fmt.Fprintf(&b, "--flat\r\nContent-Type: text/plain\r\n\r\nTeil %d\r\n", i)
	}
	b.WriteString("--flat--\r\n")
	return []byte(b.String())
}

// nestedMessage nests multiparts so that its text part sits levels deep, at
// part 1.1.1 and so on.
func nestedMessage(levels int) []byte {
	var b strings.Builder
	b.WriteString("From: Tief <tief@example.com>\r\nSubject: Tief\r\n")
	b.WriteString("Content-Type: multipart/mixed; boundary=\"n0\"\r\n\r\n")
	for depth := 1; depth < levels; depth++ {
		fmt.Fprintf(&b, "--n%d\r\nContent-Type: multipart/mixed; boundary=\"n%d\"\r\n\r\n", depth-1, depth)
	}
	fmt.Fprintf(&b, "--n%d\r\nContent-Type: text/plain\r\n\r\nGanz unten\r\n", levels-1)
	for depth := levels - 1; depth >= 0; depth-- {
		fmt.Fprintf(&b, "--n%d--\r\n", depth)
	}
	return []byte(b.String())
}

func malformedFixture(t *testing.T, name string) []byte {
	t.Helper()
	original, err := os.ReadFile(filepath.Join("..", "mailparse", "testdata", "malformed", name))
	if err != nil {
		t.Fatal(err)
	}
	return original
}

func typed(s string) *string { return &s }

// collectBytes stores original as a source's folder returned it. Collection
// keeps what an import refuses: a file that doesn't start with a header line.
func (f *processingFixture) collectBytes(original []byte) int64 {
	f.t.Helper()
	statements := []string{
		`INSERT OR IGNORE INTO imap_source (id, name, host, port, security, user_name, user_key, sealed_password, created_at)
		 VALUES (1, 'Anna', 'imap.example.test', 993, 'tls', 'anna@example.test', 'anna@example.test', x'00', 0)`,
		`INSERT OR IGNORE INTO imap_folder (id, source_id, name, chosen) VALUES (1, 1, 'INBOX', 1)`,
	}
	for _, statement := range statements {
		if _, err := f.db.Writer.Exec(statement); err != nil {
			f.t.Fatal(err)
		}
	}
	result, err := f.archive.StoreCollected(context.Background(), bytes.NewReader(original),
		archive.CollectedMeta{SourceID: 1, FolderID: 1})
	if err != nil {
		f.t.Fatal(err)
	}
	stored, ok := result.(archive.Stored)
	if !ok {
		f.t.Fatalf("collection gave %#v, want Stored", result)
	}
	return stored.Message.ID
}

// archiveDigest hashes every stored original with its path, and every
// history entry, so any change to either changes the digest.
func (f *processingFixture) archiveDigest() string {
	f.t.Helper()
	h := sha256.New()
	err := filepath.WalkDir(filepath.Join(f.dataDir, "blobs"), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "%s %x\n", path, sha256.Sum256(content))
		return nil
	})
	if err != nil {
		f.t.Fatal(err)
	}
	rows, err := f.db.Reader.Query("SELECT * FROM source_observation ORDER BY id")
	if err != nil {
		f.t.Fatal(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		f.t.Fatal(err)
	}
	values := make([]any, len(columns))
	targets := make([]any, len(columns))
	for i := range values {
		targets[i] = &values[i]
	}
	for rows.Next() {
		if err := rows.Scan(targets...); err != nil {
			f.t.Fatal(err)
		}
		fmt.Fprintf(h, "%v\n", values)
	}
	if err := rows.Err(); err != nil {
		f.t.Fatal(err)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// TestMalformedSet processes broken, hostile, and oversized messages, and
// checks that each ends in its expected state with its problems while no
// original or history entry changes. Imports and collection capture them, as
// in use.
func TestMalformedSet(t *testing.T) {
	type expected struct {
		state    string
		problems []storedProblem
		// summary is checked for the messages whose row is the point.
		summary *archive.Summary
	}
	set := []struct {
		name     string
		original []byte
		// collected marks the messages an import refuses for not starting
		// with a header line.
		collected bool
		want      expected
	}{
		{"broken-base64.eml", malformedFixture(t, "broken-base64.eml"), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"2", typed("application/pdf"), "invalid_encoding"}},
		}},
		{"unknown-charset.eml", malformedFixture(t, "unknown-charset.eml"), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1", typed("text/plain"), "unknown_charset"}},
		}},
		{"missing-boundary.eml", malformedFixture(t, "missing-boundary.eml"), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1", typed("multipart/mixed"), "malformed_structure"}},
		}},
		{"cut-off.eml", malformedFixture(t, "cut-off.eml"), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"", typed("multipart/mixed"), "cut_short"}},
		}},
		{"unreadable-header.eml", malformedFixture(t, "unreadable-header.eml"), true, expected{
			state:    "unreadable",
			problems: []storedProblem{{"", nil, "malformed_header"}},
		}},
		{"no-headers.eml", malformedFixture(t, "no-headers.eml"), true, expected{
			state:   "processed",
			summary: &archive.Summary{DateReason: typed("missing")},
		}},
		{"spoofed-sender.eml", malformedFixture(t, "spoofed-sender.eml"), false, expected{
			state: "processed",
			summary: &archive.Summary{
				SenderName: typed("ceo@example.com"), SenderAddress: typed("x@attacker.example"),
				Subject: typed("Dringende Ueberweisung"), DateHeader: typed("Mon, 09 Nov 2026 14:00:00 +0000"),
			},
		}},
		{"long-name.eml", malformedFixture(t, "long-name.eml"), false, expected{
			state: "processed",
			summary: &archive.Summary{
				SenderName: typed(strings.Repeat("Langername", 50)), SenderAddress: typed("lang@example.com"),
				Subject: typed("Langer Name"), DateHeader: typed("Mon, 09 Nov 2026 15:00:00 +0000"),
			},
		}},
		{"control-characters.eml", malformedFixture(t, "control-characters.eml"), false, expected{
			state: "processed",
			summary: &archive.Summary{
				SenderName: typed("Anna\u202e\x00Admin"), SenderAddress: typed("anna@example.com"),
				Subject: typed("Rechnung\u202efdp.exe\x00"), DateHeader: typed("Mon, 09 Nov 2026 16:00:00 +0000"),
			},
		}},
		{"html-subject.eml", malformedFixture(t, "html-subject.eml"), false, expected{
			state: "processed",
			summary: &archive.Summary{
				SenderName: typed("Jan Moll"), SenderAddress: typed("jan@example.com"),
				Subject: typed("<b>Fett</b><script>alert(1)</script>"), DateHeader: typed("Mon, 09 Nov 2026 17:00:00 +0000"),
			},
		}},
		{"1,200 parts", flatMessage(1200), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1001", typed("text/plain"), "too_many_parts"}},
		}},
		{"12,000 parts", flatMessage(12_000), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1001", typed("text/plain"), "too_many_parts"}},
		}},
		{"25 levels", nestedMessage(25), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1" + strings.Repeat(".1", 20), typed("multipart/mixed"), "nested_too_deeply"}},
		}},
		{"800 levels", nestedMessage(800), false, expected{
			state:    "processed_with_problems",
			problems: []storedProblem{{"1" + strings.Repeat(".1", 20), typed("multipart/mixed"), "nested_too_deeply"}},
		}},
	}

	// The production limit, so a slow parse under the race detector can't
	// pass for a hung worker.
	f := newProcessingFixtureWith(t, 2, MessageLimit, testWorkerCommand)
	ids := make([]int64, len(set))
	for i, message := range set {
		if message.collected {
			ids[i] = f.collectBytes(message.original)
		} else {
			ids[i] = f.importBytes(message.original).Message.ID
		}
	}
	before := f.archiveDigest()

	for f.runOnce(f.processor) > 0 {
	}

	for i, message := range set {
		t.Run(message.name, func(t *testing.T) {
			id := ids[i]
			if got := f.processing(id).state; got != message.want.state {
				t.Errorf("state %s, want %s", got, message.want.state)
			}
			if got := f.problems(id); !reflect.DeepEqual(got, message.want.problems) {
				t.Errorf("problems %s, want %s", describeProblems(got), describeProblems(message.want.problems))
			}
			if want := message.want.summary; want != nil {
				if got := f.row(id).Summary; !reflect.DeepEqual(got, want) {
					t.Errorf("summary %+v, want %+v", got, want)
				}
			}
			original, err := f.archive.OpenVerified(context.Background(), id)
			if err != nil {
				t.Fatalf("OpenVerified: %v", err)
			}
			defer original.File.Close()
			stored, err := io.ReadAll(original.File)
			if err != nil || !bytes.Equal(stored, message.original) {
				t.Errorf("the original downloads as %d different bytes (%v), want the %d imported", len(stored), err, len(message.original))
			}
		})
	}

	listed := map[int64]int{}
	for page := 1; ; page++ {
		result, err := f.archive.List(context.Background(), page)
		if err != nil {
			t.Fatal(err)
		}
		for _, row := range result.Items {
			listed[row.ID]++
		}
		if page*archive.PageSize >= result.Total {
			break
		}
	}
	for i, id := range ids {
		if listed[id] != 1 {
			t.Errorf("%s is listed %d times, want once", set[i].name, listed[id])
		}
	}

	if after := f.archiveDigest(); after != before {
		t.Error("processing changed a stored original or a history entry")
	}
}

func describeProblems(problems []storedProblem) string {
	var parts []string
	for _, p := range problems {
		declared := "NULL"
		if p.declaredType != nil {
			declared = *p.declaredType
		}
		parts = append(parts, fmt.Sprintf("{%q %s %s}", p.position, declared, p.kind))
	}
	return "[" + strings.Join(parts, " ") + "]"
}
