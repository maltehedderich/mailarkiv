package archive

import "bytes"

// headSize is all the file check reads. It holds two lines of RFC 5322's
// 998-byte maximum, which is enough for an mbox From line and one header.
const headSize = 4096

var mboxFromLine = []byte("From ")

// checkHead accepts a file starting with a header line, optionally after one
// line starting with "From ". Nothing else about the bytes is inspected.
func checkHead(head []byte) (reason Reason, refused bool) {
	if len(head) == 0 {
		return EmptyFile, true
	}
	head = head[:min(len(head), headSize)]

	line := head
	if bytes.HasPrefix(head, mboxFromLine) {
		end := bytes.IndexByte(head, '\n')
		if end < 0 {
			return NoMailHeader, true
		}
		line = head[end+1:]
	}
	if !startsWithHeaderLine(line) {
		return NoMailHeader, true
	}
	return Reason{}, false
}

// startsWithHeaderLine reports whether b starts with one or more bytes from
// 0x21 to 0x7E other than ':', then optional spaces or tabs, then ':'.
func startsWithHeaderLine(b []byte) bool {
	i := 0
	for i < len(b) && b[i] >= 0x21 && b[i] <= 0x7e && b[i] != ':' {
		i++
	}
	if i == 0 {
		return false
	}
	for i < len(b) && (b[i] == ' ' || b[i] == '\t') {
		i++
	}
	return i < len(b) && b[i] == ':'
}
