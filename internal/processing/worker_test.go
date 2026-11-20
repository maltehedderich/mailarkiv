package processing

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"reflect"
	"slices"
	"syscall"
	"testing"
	"time"

	"github.com/maltehedderich/mailarkiv/internal/archive"
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
	var fds []string
	for _, entry := range entries {
		fds = append(fds, entry.Name())
	}
	if want := []string{"0", "1", "2"}; !slices.Equal(fds, want) {
		t.Errorf("worker holds descriptors %q, want only %q", fds, want)
	}
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
