// Package processing reads untrusted mail in worker processes, so a message
// that hangs or crashes the parser can't stop the server. It also owns the
// frames the server exchanges with its workers and with the search process.
package processing

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// MaxFrame is the largest frame body, in bytes.
const MaxFrame = 64 << 20

// WriteFrame writes v as one frame: a 4-byte unsigned big-endian length,
// then that many bytes of JSON. It writes nothing if v is over MaxFrame.
func WriteFrame(w io.Writer, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return fmt.Errorf("encode a frame: %w", err)
	}
	if len(body) > MaxFrame {
		return fmt.Errorf("a frame of %d bytes is over the limit of %d", len(body), MaxFrame)
	}
	// One write, so the length and body never reach the reader apart.
	frame := binary.BigEndian.AppendUint32(make([]byte, 0, 4+len(body)), uint32(len(body)))
	if _, err := w.Write(append(frame, body...)); err != nil {
		return fmt.Errorf("write a frame: %w", err)
	}
	return nil
}

// ReadFrame reads one frame into v. It returns io.EOF only when r ends before
// a frame starts, which means the other side closed the connection. A length
// of 0 or over MaxFrame fails before any of the body is read.
func ReadFrame(r io.Reader, v any) error {
	var length [4]byte
	if _, err := io.ReadFull(r, length[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return io.EOF
		}
		return fmt.Errorf("read a frame's length: %w", err)
	}
	size := binary.BigEndian.Uint32(length[:])
	if size == 0 {
		return errors.New("a frame has a length of 0")
	}
	if size > MaxFrame {
		return fmt.Errorf("a frame of %d bytes is over the limit of %d", size, MaxFrame)
	}
	body := make([]byte, size)
	if _, err := io.ReadFull(r, body); err != nil {
		// A body that ends early is a broken frame, not a clean end.
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return fmt.Errorf("read a frame of %d bytes: %w", size, err)
	}
	if err := json.Unmarshal(body, v); err != nil {
		return fmt.Errorf("decode a frame: %w", err)
	}
	return nil
}
