package processing

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"testing"
)

type sample struct {
	Type  string   `json:"type"`
	ID    int64    `json:"id"`
	Words []string `json:"words"`
}

// frame builds a frame by hand: a 4-byte unsigned big-endian length, then
// the body, which may be shorter than the length says.
func frame(length uint32, body string) []byte {
	return append(binary.BigEndian.AppendUint32(nil, length), body...)
}

func TestFrameRoundTrip(t *testing.T) {
	want := sample{Type: "search", ID: 3, Words: []string{"Müller", "quartals"}}
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, want); err != nil {
		t.Fatalf("WriteFrame: %v", err)
	}
	var got sample
	if err := ReadFrame(&buffer, &got); err != nil {
		t.Fatalf("ReadFrame: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("read %+v, want %+v", got, want)
	}
	if buffer.Len() != 0 {
		t.Errorf("%d bytes left after one frame", buffer.Len())
	}
}

func TestFrameLayout(t *testing.T) {
	var buffer bytes.Buffer
	if err := WriteFrame(&buffer, map[string]string{"type": "hello"}); err != nil {
		t.Fatal(err)
	}
	want := frame(16, `{"type":"hello"}`)
	if !bytes.Equal(buffer.Bytes(), want) {
		t.Errorf("wrote % x, want % x", buffer.Bytes(), want)
	}

	// A frame written by hand, as the search process writes one, reads back.
	var got sample
	if err := ReadFrame(bytes.NewReader(frame(26, `{"type":"answer","id":258}`)), &got); err != nil {
		t.Fatal(err)
	}
	if got.Type != "answer" || got.ID != 258 {
		t.Errorf("read %+v, want an answer with ID 258", got)
	}
}

func TestMaxFrameValue(t *testing.T) {
	if MaxFrame != 67_108_864 {
		t.Errorf("MaxFrame = %d, want 64 MiB", MaxFrame)
	}
}

func TestReadFrameRefuses(t *testing.T) {
	tests := []struct {
		name  string
		input []byte
	}{
		{"a length of 0", frame(0, "")},
		{"a short body", frame(10, `{"id":1}`)},
		{"a length with no body", frame(10, "")},
		{"JSON that doesn't parse", frame(3, "{x}")},
		{"two values", frame(4, "1 2 ")},
		{"half a length", []byte{0, 0}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got sample
			err := ReadFrame(bytes.NewReader(tt.input), &got)
			if err == nil {
				t.Fatalf("ReadFrame accepted % x as %+v", tt.input, got)
			}
			// io.EOF alone means the other side closed between frames.
			if errors.Is(err, io.EOF) {
				t.Errorf("ReadFrame = %v, which reads as a clean end", err)
			}
		})
	}
}

// A length over the limit fails before the body is read, so a broken peer
// can't make the reader allocate or wait for it.
func TestReadFrameRefusesALengthOverTheLimit(t *testing.T) {
	for _, length := range []uint32{MaxFrame + 1, 1 << 31, 1<<32 - 1} {
		body := []byte(`{"id":1}`)
		reader := bytes.NewReader(frame(length, string(body)))
		var got sample
		if err := ReadFrame(reader, &got); err == nil {
			t.Fatalf("ReadFrame accepted a length of %d", length)
		}
		if reader.Len() != len(body) {
			t.Errorf("length %d: %d body bytes were read, want none", length, len(body)-reader.Len())
		}
	}

	// A length of exactly the limit is allowed, so the reader goes on to the
	// body, which is short here.
	reader := bytes.NewReader(frame(MaxFrame, `{"id":1}`))
	var got sample
	if err := ReadFrame(reader, &got); err == nil {
		t.Fatal("ReadFrame accepted a short body")
	}
	if reader.Len() != 0 {
		t.Errorf("a length of exactly %d left the body unread", MaxFrame)
	}
}

func TestReadFrameAtTheEnd(t *testing.T) {
	var got sample
	if err := ReadFrame(bytes.NewReader(nil), &got); err != io.EOF {
		t.Errorf("ReadFrame with nothing left = %v, want io.EOF", err)
	}
}

func TestWriteFrameRefusesAValueOverTheLimit(t *testing.T) {
	var buffer bytes.Buffer
	huge := sample{Words: []string{string(bytes.Repeat([]byte("a"), MaxFrame))}}
	if err := WriteFrame(&buffer, huge); err == nil {
		t.Fatal("WriteFrame accepted a value over the limit")
	}
	if buffer.Len() != 0 {
		t.Errorf("WriteFrame wrote %d bytes of a refused frame", buffer.Len())
	}
}
