package mailparse

import (
	"bytes"
	"strconv"
	"strings"
	"unicode"

	"github.com/jhillyerd/enmime/v2/mediatype"
)

// Bounds on the structure Read parses. enmime fails a whole message at its
// own part limit and has no depth limit, and its time grows with the square
// of the depth, so scanParts applies both before enmime runs.
const (
	maxParts = 1_000
	maxDepth = 20
)

// frame is a multipart whose closing delimiter hasn't come yet.
type frame struct {
	// dash is "--" and the boundary, which starts each delimiter line.
	dash []byte
	// position is the multipart's part number, or "" for the message.
	position string
	// depth is the multipart's own depth. Its parts sit one deeper.
	depth int
	parts int
}

// scanParts walks the multipart structure of original as enmime will split
// it, reading only header blocks and delimiter lines. Parts count in document
// order, multiparts included, and the parts of the message sit at depth 1. At
// the first part past maxParts, or deeper than maxDepth, it cuts the input
// before that part's delimiter line and closes every boundary still open. It
// closes them too when the input ends with one open. It never enters an
// attached message, which enmime reads as one part.
//
// It returns original itself when nothing needed cutting or closing, and a
// new slice otherwise.
func scanParts(original []byte) ([]byte, []Problem) {
	s := lineScanner{input: original}
	root := s.header(nil)
	rootType, rootBoundary, ok := root.multipart(true)
	// A message that ends inside its header has no body to close, and a
	// closer there would join the header.
	if !ok || !root.ended {
		return original, nil
	}
	stack := []frame{{dash: dashed(rootBoundary)}}
	total := 0
	for {
		start := s.offset
		line, ended, more := s.next()
		if !more {
			break
		}
		index, closing := matchDelimiter(stack, line, ended)
		if index < 0 {
			// Content, a preamble, or an epilogue.
			continue
		}
		// A delimiter of an outer multipart ends the ones inside it.
		stack = stack[:index+1]
		if closing {
			stack = stack[:index]
			if len(stack) == 0 {
				// enmime reads the rest as the message's epilogue.
				return original, nil
			}
			continue
		}
		parent := &stack[index]
		parent.parts++
		total++
		position := partNumber(parent.position, parent.parts)
		depth := parent.depth + 1
		h := s.header(stack)
		switch {
		case total > maxParts:
			return closeAt(original, start, stack), []Problem{{Position: position, DeclaredType: h.declaredType(), Kind: TooManyParts}}
		case depth > maxDepth:
			return closeAt(original, start, stack), []Problem{{Position: position, DeclaredType: h.declaredType(), Kind: NestedTooDeeply}}
		}
		if _, boundary, ok := h.multipart(false); ok {
			stack = append(stack, frame{dash: dashed(boundary), position: position, depth: depth})
		}
	}
	if len(stack) == 1 && rootBoundary == "" {
		// A message without a boundary has none to close. Read reports it as
		// a malformed structure.
		return original, nil
	}
	return closeAt(original, len(original), stack), []Problem{{Position: "", DeclaredType: rootType, Kind: CutShort}}
}

func dashed(boundary string) []byte { return []byte("--" + boundary) }

func partNumber(parent string, number int) string {
	if parent == "" {
		return strconv.Itoa(number)
	}
	return parent + "." + strconv.Itoa(number)
}

// matchDelimiter finds the innermost open multipart that line is a delimiter
// of, as enmime's boundary reader does: the boundary, then white space or the
// line's end, or "--" for the closing delimiter. A delimiter needs its line
// ending, and a closing delimiter doesn't. It returns -1 for any other line.
func matchDelimiter(stack []frame, line []byte, ended bool) (index int, closing bool) {
	for i := len(stack) - 1; i >= 0; i-- {
		rest, found := bytes.CutPrefix(line, stack[i].dash)
		if !found {
			continue
		}
		switch {
		case bytes.HasPrefix(rest, []byte("--")):
			return i, true
		case len(rest) == 0 && ended, len(rest) > 0 && unicode.IsSpace(rune(rest[0])):
			return i, false
		}
	}
	return -1, false
}

// closeAt copies original up to at and appends a closing delimiter for every
// frame in stack, innermost first, in the input's own line ending.
func closeAt(original []byte, at int, stack []frame) []byte {
	eol := []byte("\n")
	if i := bytes.IndexByte(original, '\n'); i > 0 && original[i-1] == '\r' {
		eol = []byte("\r\n")
	}
	out := make([]byte, 0, at+len(eol)+len(stack)*80)
	out = append(out, original[:at]...)
	if at > 0 && original[at-1] != '\n' {
		out = append(out, eol...)
	}
	for i := len(stack) - 1; i >= 0; i-- {
		out = append(out, stack[i].dash...)
		out = append(out, "--"...)
		out = append(out, eol...)
	}
	return out
}

// lineScanner reads lines of its input from offset.
type lineScanner struct {
	input  []byte
	offset int
}

// next returns the next line without its ending, whether it had one, and
// false at the end of the input.
func (s *lineScanner) next() (line []byte, ended, more bool) {
	if s.offset >= len(s.input) {
		return nil, false, false
	}
	rest := s.input[s.offset:]
	end := bytes.IndexByte(rest, '\n')
	if end < 0 {
		s.offset = len(s.input)
		return rest, false, true
	}
	s.offset += end + 1
	return bytes.TrimSuffix(rest[:end], []byte("\r")), true, true
}

// partHeader is what a header block says about a part's type.
type partHeader struct {
	contentType string
	present     bool
	// ended is false when the input ends inside the header.
	ended bool
}

// header reads a header block up to its blank line, or up to a delimiter of
// a multipart in stack, which enmime's reader stops at. It keeps the first
// Content-Type field, unfolded as enmime unfolds it.
func (s *lineScanner) header(stack []frame) partHeader {
	var h partHeader
	inContentType := false
	for {
		start := s.offset
		line, ended, more := s.next()
		if !more {
			return h
		}
		if len(line) == 0 {
			h.ended = true
			return h
		}
		if index, _ := matchDelimiter(stack, line, ended); index >= 0 {
			s.offset = start
			h.ended = true
			return h
		}
		colon := bytes.IndexByte(line, ':')
		switch {
		case line[0] == ' ' || line[0] == '\t', colon < 0:
			// A continuation, indented or not.
			if inContentType {
				h.contentType += " " + strings.TrimSpace(string(line))
			}
		case colon > 0:
			name := strings.TrimRight(string(line[:colon]), " ")
			inContentType = !h.present && strings.EqualFold(name, "Content-Type")
			if inContentType {
				h.present = true
				h.contentType = strings.TrimSpace(string(line[colon+1:]))
			}
		}
	}
}

// multipart reports whether enmime splits the part into parts, and with which
// boundary. The message is split when its type is multipart, even without a
// boundary. A part inside is split when it has a boundary and isn't text.
func (h partHeader) multipart(message bool) (declared, boundary string, ok bool) {
	if !h.present {
		return "", "", false
	}
	mtype, params, _, err := mediatype.Parse(h.contentType)
	if err != nil {
		return "", "", false
	}
	mtype = strings.ToLower(mtype)
	boundary = params["boundary"]
	if message {
		ok = strings.HasPrefix(mtype, "multipart/")
	} else {
		ok = boundary != "" && mtype != "text/plain" && mtype != "text/html"
	}
	return cutRunes(mtype, maxDeclaredType), boundary, ok
}

// declaredType is the part's type as a problem records it: lowercased,
// without parameters, and "" when the part declares none.
func (h partHeader) declaredType() string {
	if !h.present {
		return ""
	}
	mtype, _, _, err := mediatype.Parse(h.contentType)
	if err != nil {
		return ""
	}
	return cutRunes(strings.ToLower(mtype), maxDeclaredType)
}
