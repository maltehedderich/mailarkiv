package mailparse

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanFileName(t *testing.T) {
	tests := []struct {
		name     string
		original *string
		position string
		kind     AttachmentKind
		want     string
	}{
		{"an ordinary name", text("Vertrag_2026.pdf"), "2", KindFile, "Vertrag_2026.pdf"},
		{"non-ASCII stays", text("Prüfbericht.pdf"), "2", KindFile, "Prüfbericht.pdf"},
		{"a path", text("../../etc/passwd"), "2", KindFile, "etcpasswd"},
		{"backslashes", text(`C:\Temp\Rechnung.pdf`), "2", KindFile, "C:TempRechnung.pdf"},
		{"markup in the name", text("<b>x</b>.html"), "2", KindFile, "<b>x<b>.html"},
		{"right-to-left override", text("Rechnung\u202efdp.exe"), "2", KindFile, "Rechnungfdp.exe"},
		{"C0 controls", text("a\x00b\x09c\x1fd.txt"), "2", KindFile, "abcd.txt"},
		{"C1 controls", text("a\u0080b\u009fc.txt"), "2", KindFile, "abc.txt"},
		{
			"bidirectional controls",
			text("\u061c\u200e\u200f\u202a\u202b\u202c\u202d\u202e\u2066\u2067\u2068\u2069Plan.txt"),
			"2", KindFile, "Plan.txt",
		},
		{"spaces and dots at both ends", text("  . Bericht.pdf .  "), "2", KindFile, "Bericht.pdf"},
		{"dots and spaces inside stay", text("Q3 . final.pdf"), "2", KindFile, "Q3 . final.pdf"},
		{"no name", nil, "2", KindFile, "attachment-2"},
		{"nothing left", text("/.. ./"), "1.2", KindFile, "attachment-1.2"},
		{"controls only", text("\u202e\x07"), "3", KindFile, "attachment-3"},
		{"a message gains .eml", text("Weitergeleitet"), "2", KindMessage, "Weitergeleitet.eml"},
		{"a message with .eml", text("Nachricht.EML"), "2", KindMessage, "Nachricht.EML"},
		{"a message with no name", nil, "3", KindMessage, "attachment-3.eml"},
		{"a file doesn't gain .eml", text("Weitergeleitet"), "2", KindFile, "Weitergeleitet"},
		{"an embedded image", text("image001.png"), "1.2", KindEmbeddedImage, "image001.png"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CleanFileName(tt.original, tt.position, tt.kind); got != tt.want {
				t.Errorf("CleanFileName = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestCleanFileNameShortens(t *testing.T) {
	tests := []struct {
		name     string
		original string
		kind     AttachmentKind
		want     string
	}{
		{
			// 1 + 294 + 1 + 4 = 300 bytes. The extension keeps 4, which leaves
			// 196 for the rest: "a" and 97 "ä" take 195, and a 98th "ä" would
			// end at byte 197, so it goes.
			name:     "300 bytes on a character boundary",
			original: "a" + strings.Repeat("ä", 147) + "b.pdf",
			kind:     KindFile,
			want:     "a" + strings.Repeat("ä", 97) + ".pdf",
		},
		{
			name:     "no extension",
			original: strings.Repeat("x", 300),
			kind:     KindFile,
			want:     strings.Repeat("x", 200),
		},
		{
			// 198 bytes and .eml make 202.
			name:     "a message's added .eml",
			original: strings.Repeat("x", 198),
			kind:     KindMessage,
			want:     strings.Repeat("x", 196) + ".eml",
		},
		{
			name:     "200 bytes stay",
			original: strings.Repeat("x", 196) + ".pdf",
			kind:     KindFile,
			want:     strings.Repeat("x", 196) + ".pdf",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CleanFileName(&tt.original, "2", tt.kind)
			if got != tt.want {
				t.Errorf("CleanFileName gave %d bytes %q, want %d bytes %q", len(got), got, len(tt.want), tt.want)
			}
			if len(got) > 200 || !utf8.ValidString(got) {
				t.Errorf("%d bytes, valid UTF-8 %t", len(got), utf8.ValidString(got))
			}
		})
	}
}
