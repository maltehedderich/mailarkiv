package processing

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
	"github.com/maltehedderich/mailarkiv/internal/mailparse"
)

func readThrough(t *testing.T, pool *Pool, original []byte, withText bool) readAnswer {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	answer, err := pool.read(ctx, bytes.NewReader(original), int64(len(original)), withText)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	return answer
}

func TestWorkerRead(t *testing.T) {
	logger, _ := newLogger()
	pool := newTestPool(t, 1, logger)
	tests := []struct {
		fixture, sender, subject string
		attachments              []string
	}{
		{"plain-text.eml", "anna@example.test", "Wochenplan", nil},
		{"attachment.eml", "dora@example.test", "Vertrag", []string{"Vertrag_2026.pdf"}},
		{"signature-logo.eml", "emil@example.test", "Mit Logo", []string{"logo.png"}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			answer := readThrough(t, pool, plainFixture(t, tt.fixture), false)
			message := answer.Message
			if len(message.From) == 0 || message.From[0].Address != tt.sender {
				t.Errorf("From %+v, want %s first", message.From, tt.sender)
			}
			if message.Subject == nil || *message.Subject != tt.subject {
				t.Errorf("Subject %v, want %q", message.Subject, tt.subject)
			}
			var names []string
			for _, attachment := range message.Attachments {
				names = append(names, *attachment.Name)
			}
			if !slices.Equal(names, tt.attachments) {
				t.Errorf("attachments %q, want %q", names, tt.attachments)
			}
			if answer.Text != nil {
				t.Errorf("text %q without asking for it", answer.Text)
			}
		})
	}

	answer := readThrough(t, pool, plainFixture(t, "html-only.eml"), true)
	if want := []string{"Der Quartalsbericht liegt bei. Erster Zweiter"}; !reflect.DeepEqual(answer.Text, want) {
		t.Errorf("text %q, want %q", answer.Text, want)
	}
}

// A worker reads only what arrives on standard input, so a hostile message
// has no file or connection to reach.
func TestWorkerOpensNoFile(t *testing.T) {
	logger, _ := newLogger()
	pool := newTestPool(t, 1, logger)
	readThrough(t, pool, plainFixture(t, "attachment.eml"), true)

	pids := pool.pids()
	if len(pids) != 1 {
		t.Fatalf("pool has %d workers, want 1", len(pids))
	}
	entries, err := os.ReadDir(fmt.Sprintf("/proc/%d/fd", pids[0]))
	if err != nil {
		t.Fatal(err)
	}
	var extra []string
	for _, entry := range entries {
		fd := entry.Name()
		target, err := os.Readlink(fmt.Sprintf("/proc/%d/fd/%s", pids[0], fd))
		if err != nil {
			t.Fatal(err)
		}
		if fd == "0" || fd == "1" || fd == "2" || isCgroupCPULimit(target) {
			continue
		}
		extra = append(extra, fd+" -> "+target)
	}
	if len(extra) > 0 {
		t.Errorf("worker holds descriptors %q beyond standard input, output, and error", extra)
	}
}

// isCgroupCPULimit reports whether target is a file the worker's Go runtime
// keeps open, read only, to follow the cgroup's CPU limit with GOMAXPROCS:
// cpu.max on cgroup v2, or cpu.cfs_quota_us and cpu.cfs_period_us on v1.
func isCgroupCPULimit(target string) bool {
	switch filepath.Base(target) {
	case "cpu.max", "cpu.cfs_quota_us", "cpu.cfs_period_us":
		return strings.HasPrefix(target, "/sys/fs/cgroup/")
	}
	return false
}

func TestWorkerStartsAgain(t *testing.T) {
	logger, _ := newLogger()
	pool := newTestPool(t, 1, logger)
	before := pool.pids()
	if err := syscall.Kill(before[0], syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for pids := pool.pids(); len(pids) != 1 || pids[0] == before[0]; pids = pool.pids() {
		if time.Now().After(deadline) {
			t.Fatalf("workers %v 10 s after %v died, want one new worker", pids, before)
		}
		time.Sleep(10 * time.Millisecond)
	}
	answer := readThrough(t, pool, plainFixture(t, "plain-text.eml"), false)
	if answer.Message.Subject == nil || *answer.Message.Subject != "Wochenplan" {
		t.Errorf("after a worker died, read gave %+v", answer.Message)
	}
	if after := pool.pids(); len(after) != 1 || after[0] == before[0] {
		t.Errorf("workers %v after %v died, want one new worker", after, before)
	}
}

func requestFrame(t *testing.T, size int64) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, workerRequest{Op: "read", Size: size}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func TestRunWorker(t *testing.T) {
	original := plainFixture(t, "plain-text.eml")
	input := append(requestFrame(t, int64(len(original))), original...)
	input = append(input, requestFrame(t, int64(len(original)))...)
	input = append(input, original...)
	var output bytes.Buffer
	if status := RunWorker(bytes.NewReader(input), &output); status != 0 {
		t.Fatalf("RunWorker exited %d after its input closed, want 0", status)
	}
	for i := range 2 {
		var answer readAnswer
		if err := ReadFrame(&output, &answer); err != nil {
			t.Fatalf("answer %d: %v", i, err)
		}
		if answer.Message.Subject == nil || *answer.Message.Subject != "Wochenplan" {
			t.Errorf("answer %d: %+v", i, answer.Message)
		}
	}
	if output.Len() != 0 {
		t.Errorf("%d bytes after two answers", output.Len())
	}
}

func TestRunWorkerRefuses(t *testing.T) {
	var unknown bytes.Buffer
	if err := WriteFrame(&unknown, workerRequest{Op: "format_disk"}); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		input []byte
	}{
		{"a length of 0", binary.BigEndian.AppendUint32(nil, 0)},
		{"JSON that doesn't parse", append(binary.BigEndian.AppendUint32(nil, 3), "{x}"...)},
		{"an original over the size limit", requestFrame(t, archive.MaxSize+1)},
		{"a negative size", requestFrame(t, -1)},
		{"an original that ends early", append(requestFrame(t, 100), "From: a@example.test\r\n"...)},
		{"an unknown operation", unknown.Bytes()},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if status := RunWorker(bytes.NewReader(tt.input), io.Discard); status != 3 {
				t.Errorf("RunWorker exited %d, want 3", status)
			}
		})
	}
}

// runWorkerOnce sends one request with its original straight to RunWorker
// and returns everything it wrote.
func runWorkerOnce(t *testing.T, request workerRequest, original []byte) *bytes.Buffer {
	t.Helper()
	request.Size = int64(len(original))
	var input bytes.Buffer
	if err := WriteFrame(&input, request); err != nil {
		t.Fatal(err)
	}
	input.Write(original)
	var output bytes.Buffer
	if status := RunWorker(&input, &output); status != 0 {
		t.Fatalf("RunWorker exited %d, want 0", status)
	}
	return &output
}

func TestWorkerReadForTheView(t *testing.T) {
	tests := []struct {
		fixture string
		want    *viewAnswer
	}{
		{"signature-logo.eml", &viewAnswer{HTML: &htmlFlags{}, Text: &textFlags{}}},
		{"plain-text.eml", &viewAnswer{Text: &textFlags{}}},
		{"html-only.eml", &viewAnswer{HTML: &htmlFlags{}}},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			var answer readAnswer
			output := runWorkerOnce(t, workerRequest{Op: "read", View: true}, plainFixture(t, tt.fixture))
			if err := ReadFrame(output, &answer); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(answer.View, tt.want) {
				t.Errorf("view %+v, want %+v", answer.View, tt.want)
			}
			if answer.Message.Plain != nil || answer.Message.HTML != nil {
				t.Error("the answer carries the bodies")
			}
		})
	}

	t.Run("flags", func(t *testing.T) {
		original := "From: anna@example.test\r\nMIME-Version: 1.0\r\nContent-Type: multipart/alternative; boundary=\"alt\"\r\n\r\n" +
			"--alt\r\nContent-Type: text/plain\r\n\r\n" + strings.Repeat("a", mailparse.MaxBody+1) + "\r\n" +
			"--alt\r\nContent-Type: text/html\r\n\r\n<img src=\"https://images.example.org/a.png\">\r\n--alt--\r\n"
		var answer readAnswer
		if err := ReadFrame(runWorkerOnce(t, workerRequest{Op: "read", View: true}, []byte(original)), &answer); err != nil {
			t.Fatal(err)
		}
		want := &viewAnswer{HTML: &htmlFlags{RemoteImages: true}, Text: &textFlags{Truncated: true}}
		if !reflect.DeepEqual(answer.View, want) {
			t.Errorf("view %+v, want %+v", answer.View, want)
		}
	})

	t.Run("not asked", func(t *testing.T) {
		var answer readAnswer
		if err := ReadFrame(runWorkerOnce(t, workerRequest{Op: "read"}, plainFixture(t, "signature-logo.eml")), &answer); err != nil {
			t.Fatal(err)
		}
		if answer.View != nil {
			t.Errorf("view %+v without asking for it", answer.View)
		}
	})
}

func TestWorkerBodies(t *testing.T) {
	tests := []struct {
		op, fixture string
		want        *string
	}{
		{"text", "plain-text.eml", typed("Hallo Team,\nder Wochenplan steht.\n")},
		{"text", "html-only.eml", nil},
		// The logo's bytes start with the PNG signature, so they show.
		{"html", "signature-logo.eml", typed(`<html><head></head><body><p>Gruss, Emil</p>` +
			`<img src="data:image/png;base64,iVBORw0KGgpzeW50aGV0aWMgbG9nbw==" alt="Logo"/></body></html>`)},
		{"html", "plain-text.eml", nil},
	}
	for _, tt := range tests {
		t.Run(tt.op+" of "+tt.fixture, func(t *testing.T) {
			var answer bodyAnswer
			if err := ReadFrame(runWorkerOnce(t, workerRequest{Op: tt.op}, plainFixture(t, tt.fixture)), &answer); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(answer.Body, tt.want) {
				t.Errorf("body %s, want %s", describeText(answer.Body), describeText(tt.want))
			}
		})
	}
}

func describeText(s *string) string {
	if s == nil {
		return "nil"
	}
	return fmt.Sprintf("%q", *s)
}

// attachmentMessage is a message with a text body and one base64 attachment
// whose encoded form is encoded.
func attachmentMessage(name, encoded string) []byte {
	var b strings.Builder
	b.WriteString("From: anna@example.test\r\nMIME-Version: 1.0\r\nContent-Type: multipart/mixed; boundary=\"mix\"\r\n\r\n")
	b.WriteString("--mix\r\nContent-Type: text/plain\r\n\r\nAnbei.\r\n")
	b.WriteString("--mix\r\nContent-Type: application/octet-stream\r\nContent-Disposition: attachment; filename=\"" + name + "\"\r\n")
	b.WriteString("Content-Transfer-Encoding: base64\r\n\r\n")
	for len(encoded) > 76 {
		b.WriteString(encoded[:76] + "\r\n")
		encoded = encoded[76:]
	}
	b.WriteString(encoded + "\r\n--mix--\r\n")
	return []byte(b.String())
}

func TestWorkerAttachment(t *testing.T) {
	t.Run("the size, then the bytes", func(t *testing.T) {
		output := runWorkerOnce(t, workerRequest{Op: "attachment", Position: "2"}, plainFixture(t, "attachment.eml"))
		var header attachmentAnswer
		if err := ReadFrame(output, &header); err != nil {
			t.Fatal(err)
		}
		content := "%PDF-1.4\n% synthetic contract for tests\n%%EOF\n"
		if want := (attachmentAnswer{Found: true, Name: "Vertrag_2026.pdf", Size: int64(len(content))}); header != want {
			t.Errorf("header %+v, want %+v", header, want)
		}
		if got := output.String(); got != content {
			t.Errorf("after the header %q, want %q", got, content)
		}
	})

	t.Run("the cleaned name", func(t *testing.T) {
		original := attachmentMessage("../../etc/passwd", base64.StdEncoding.EncodeToString([]byte("root")))
		var header attachmentAnswer
		if err := ReadFrame(runWorkerOnce(t, workerRequest{Op: "attachment", Position: "2"}, original), &header); err != nil {
			t.Fatal(err)
		}
		if header.Name != "etcpasswd" {
			t.Errorf("name %q, want etcpasswd", header.Name)
		}
	})

	t.Run("a damaged part sends what decoded", func(t *testing.T) {
		// The asterisk is outside the base64 alphabet. The rest decodes to
		// "Heiler Anhang".
		original := attachmentMessage("heil.bin", "SGVpbGVy*IEFuaGFuZw==")
		output := runWorkerOnce(t, workerRequest{Op: "attachment", Position: "2"}, original)
		var header attachmentAnswer
		if err := ReadFrame(output, &header); err != nil {
			t.Fatal(err)
		}
		if want := (attachmentAnswer{Found: true, Name: "heil.bin", Size: 13}); header != want {
			t.Errorf("header %+v, want %+v", header, want)
		}
		if got := output.String(); got != "Heiler Anhang" {
			t.Errorf("bytes %q, want %q", got, "Heiler Anhang")
		}
	})

	for _, position := range []string{"1", "3", "", "2.1", "../1"} {
		t.Run("no attachment at "+strconv.Quote(position), func(t *testing.T) {
			output := runWorkerOnce(t, workerRequest{Op: "attachment", Position: position}, plainFixture(t, "attachment.eml"))
			var header attachmentAnswer
			if err := ReadFrame(output, &header); err != nil {
				t.Fatal(err)
			}
			if header != (attachmentAnswer{}) || output.Len() != 0 {
				t.Errorf("header %+v and %d bytes, want not found and nothing after", header, output.Len())
			}
		})
	}
}

// The bytes come after the frame, so an attachment larger than a frame
// reaches the server whole through the pipe.
func TestWorkerAttachmentOf20MB(t *testing.T) {
	content := make([]byte, 20_000_000)
	for i := range content {
		content[i] = byte(i * 7)
	}
	original := attachmentMessage("gross.bin", base64.StdEncoding.EncodeToString(content))
	// Parsing 27 MB under the race detector takes longer than testLimit.
	pool, err := NewPool(1, MessageLimit, testWorkerCommand, slogDiscard())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(context.Background(), MessageLimit)
	defer cancel()
	var header attachmentAnswer
	received := sha256.New()
	request := workerRequest{Op: "attachment", Size: int64(len(original)), Position: "2"}
	err = pool.exchange(ctx, request, bytes.NewReader(original), func(answer io.Reader, streaming func()) error {
		if err := ReadFrame(answer, &header); err != nil {
			return err
		}
		streaming()
		_, err := io.CopyN(received, answer, header.Size)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if header.Size != int64(len(content)) {
		t.Errorf("size %d, want %d", header.Size, len(content))
	}
	if want := sha256.Sum256(content); !bytes.Equal(received.Sum(nil), want[:]) {
		t.Error("the bytes differ")
	}
	// The worker answered completely, so it takes the next request.
	if answer := readThrough(t, pool, plainFixture(t, "plain-text.eml"), false); answer.Message.Subject == nil {
		t.Errorf("the next read gave %+v", answer.Message)
	}
}
