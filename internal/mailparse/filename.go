package mailparse

import (
	"strings"
	"unicode"
)

// maxFileName is the most bytes of a download's file name.
const maxFileName = 200

// CleanFileName is the name a download of an attachment gets. It removes
// control characters, bidirectional controls, and path separators, trims
// spaces and dots from both ends, falls back to attachment-<position>, gives
// a message .eml, and shortens the name to 200 bytes, keeping the extension.
func CleanFileName(name *string, position string, kind AttachmentKind) string {
	cleaned := ""
	if name != nil {
		cleaned = strings.Trim(strings.Map(keptRune, validUTF8(*name)), " .")
	}
	if cleaned == "" {
		cleaned = "attachment-" + position
	}
	if kind == KindMessage && !strings.HasSuffix(strings.ToLower(cleaned), ".eml") {
		cleaned += ".eml"
	}
	return shortenKeepingExtension(cleaned, maxFileName)
}

// keptRune drops C0 and C1 controls, the bidirectional controls that can
// make a name read as another, and path separators.
func keptRune(r rune) rune {
	switch {
	case unicode.IsControl(r),
		r == '\u061c', r == '\u200e', r == '\u200f',
		r >= '\u202a' && r <= '\u202e',
		r >= '\u2066' && r <= '\u2069',
		r == '/', r == '\\':
		return -1
	}
	return r
}

// shortenKeepingExtension cuts name to limit bytes on a character boundary.
// The extension, from the last dot, stays when it leaves room for at least
// one byte before it.
func shortenKeepingExtension(name string, limit int) string {
	if len(name) <= limit {
		return name
	}
	extension := ""
	if dot := strings.LastIndexByte(name, '.'); dot > 0 && len(name)-dot < limit {
		name, extension = name[:dot], name[dot:]
	}
	stem, _ := cutBytes(name, limit-len(extension))
	return stem + extension
}
