package archive

import (
	"strings"
	"testing"
)

// File acceptance boundaries: a body passes when it starts with a header
// line, optionally after one line starting with "From ".
func TestCheckHead(t *testing.T) {
	passes := []struct {
		name string
		head string
	}{
		{"header line", "Subject: x\r\n"},
		{"space before the colon, empty value", "X-Empty :\n"},
		{"From header", "From: a@example.test\n"},
		{"mbox From line, then a header", "From a@example.test Mon Nov  2 09:14:55 2026\nSubject: x\n"},
		{"tab before the colon", "Subject\t: x\n"},
		{"binary after a valid first line", "Subject: x\r\n\x00\xff\xfe\x01\x02PK\x03\x04"},
		{"header with no line end", "Subject:"},
	}
	for _, tt := range passes {
		t.Run("passes "+tt.name, func(t *testing.T) {
			if reason, refused := checkHead([]byte(tt.head)); refused {
				t.Fatalf("checkHead(%q) refused it as %v", tt.head, reason)
			}
		})
	}

	refusals := []struct {
		name string
		head string
		want Reason
	}{
		{"empty", "", EmptyFile},
		{"PDF", "%PDF-1.7\n", NoMailHeader},
		{"ZIP", "PK\x03\x04", NoMailHeader},
		{"blank first line", "\r\nSubject: x", NoMailHeader},
		{"no name before the colon", ": value", NoMailHeader},
		{"space inside the name", "Sub ject: x", NoMailHeader},
		{"From line, then no header", "From a@example.test\nnot a header\n", NoMailHeader},
		{"From line alone", "From a@example.test Mon Nov  2 09:14:55 2026\n", NoMailHeader},
		{"two From lines", "From a@example.test\nFrom b@example.test\nSubject: x\n", NoMailHeader},
		{"5,000 bytes with no colon", strings.Repeat("a", 5000), NoMailHeader},
		{"non-ASCII name", "Sübject: x\n", NoMailHeader},
		{"control byte in the name", "Sub\x01ject: x\n", NoMailHeader},
	}
	for _, tt := range refusals {
		t.Run("refuses "+tt.name, func(t *testing.T) {
			reason, refused := checkHead([]byte(tt.head))
			if !refused || reason != tt.want {
				t.Fatalf("checkHead(%q) = %v, %v, want %v", tt.head, reason, refused, tt.want)
			}
		})
	}
}

// The check reads only the first 4 KiB, which holds two lines of RFC 5322's
// 998-byte maximum. A header that starts later doesn't count.
func TestCheckHeadReadsOnlyTheFirst4KiB(t *testing.T) {
	longFromLine := "From " + strings.Repeat("a", 4090) + "\n"
	reason, refused := checkHead([]byte(longFromLine + "Subject: x\n"))
	if !refused || reason != NoMailHeader {
		t.Fatalf("checkHead with the header after 4 KiB = %v, %v, want NoMailHeader", reason, refused)
	}

	nameAcrossTheEdge := strings.Repeat("X", 4096) + ": value\n"
	reason, refused = checkHead([]byte(nameAcrossTheEdge))
	if !refused || reason != NoMailHeader {
		t.Fatalf("checkHead with the colon after 4 KiB = %v, %v, want NoMailHeader", reason, refused)
	}
}
