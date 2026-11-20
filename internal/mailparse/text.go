package mailparse

import (
	"strings"
	"unicode/utf8"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Bounds on what Read keeps.
const (
	// MaxBody is the most bytes kept of a displayed body.
	MaxBody         = 2_000_000
	maxSenderName   = 500
	maxAddress      = 320
	maxSubject      = 1_000
	maxDateHeader   = 200
	maxDeclaredType = 255
)

// HTMLText reads the text out of an HTML body for the index. It skips
// script, style, and head, and puts a space at each block element so words
// on either side don't run together. Runs of white space become one space.
func HTMLText(source string) string {
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	var out strings.Builder
	inHead, inScript, inStyle := false, 0, 0
	for {
		token := tokenizer.Next()
		switch token {
		case html.ErrorToken:
			return strings.Join(strings.Fields(out.String()), " ")
		case html.TextToken:
			if !inHead && inScript == 0 && inStyle == 0 {
				out.Write(tokenizer.Text())
			}
		case html.StartTagToken, html.SelfClosingTagToken, html.EndTagToken:
			name, _ := tokenizer.TagName()
			tag := atom.Lookup(name)
			start := token == html.StartTagToken
			end := token == html.EndTagToken
			switch tag {
			case atom.Head:
				inHead = start
			case atom.Body:
				// A head that was never closed ends where the body starts.
				inHead = false
			case atom.Script:
				inScript = count(inScript, start, end)
			case atom.Style:
				inStyle = count(inStyle, start, end)
			}
			if blockElements[tag] {
				out.WriteByte(' ')
			}
		}
	}
}

func count(depth int, start, end bool) int {
	switch {
	case start:
		return depth + 1
	case end && depth > 0:
		return depth - 1
	}
	return depth
}

var blockElements = map[atom.Atom]bool{
	atom.Address: true, atom.Article: true, atom.Aside: true, atom.Blockquote: true,
	atom.Body: true, atom.Br: true, atom.Caption: true, atom.Center: true, atom.Dd: true,
	atom.Details: true, atom.Div: true, atom.Dl: true, atom.Dt: true, atom.Fieldset: true,
	atom.Figcaption: true, atom.Figure: true, atom.Footer: true, atom.Form: true,
	atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Header: true, atom.Hr: true, atom.Html: true, atom.Legend: true, atom.Li: true,
	atom.Main: true, atom.Nav: true, atom.Ol: true, atom.Option: true, atom.P: true,
	atom.Pre: true, atom.Section: true, atom.Summary: true, atom.Table: true, atom.Tbody: true,
	atom.Td: true, atom.Tfoot: true, atom.Th: true, atom.Thead: true, atom.Tr: true, atom.Ul: true,
}

// validUTF8 replaces each run of invalid UTF-8 with U+FFFD, which is what an
// unknown character set leaves behind.
func validUTF8(s string) string {
	return strings.ToValidUTF8(s, "�")
}

// cutBytes shortens valid UTF-8 to at most limit bytes, ending on a whole
// character, and reports whether it cut anything.
func cutBytes(s string, limit int) (string, bool) {
	if len(s) <= limit {
		return s, false
	}
	end := limit
	for end > 0 && !utf8.RuneStart(s[end]) {
		end--
	}
	return s[:end], true
}

// cutRunes shortens s to at most limit characters.
func cutRunes(s string, limit int) string {
	count := 0
	for i := range s {
		if count == limit {
			return s[:i]
		}
		count++
	}
	return s
}
