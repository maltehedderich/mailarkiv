package mailparse

import (
	"bytes"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/jhillyerd/enmime/v2"
)

// flatOptions vary how a flat message writes its structure, none of which may
// change how its parts count.
type flatOptions struct {
	eol      string
	quoted   bool
	preamble bool
	epilogue bool
}

// flatMessage is a multipart/mixed message of n text parts, numbered 1 to n.
func flatMessage(n int, o flatOptions) []byte {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + o.eol) }
	line("From: anna@example.test")
	if o.quoted {
		line(`Content-Type: multipart/mixed; boundary="flat"`)
	} else {
		line("Content-Type: multipart/mixed; boundary=flat")
	}
	line("")
	if o.preamble {
		line("This is a multi-part message in MIME format.")
		line("--flat-but-not-the-boundary")
	}
	for i := 1; i <= n; i++ {
		line("--flat")
		line("Content-Type: text/plain")
		line("")
		line(fmt.Sprintf("Teil %d", i))
	}
	line("--flat--")
	if o.epilogue {
		line("--flat")
		line("Content-Type: text/plain")
		line("")
		line("Nach dem Ende")
	}
	return []byte(b.String())
}

// nestedMessage nests multiparts so that its one text part sits levels deep:
// the top's part 1 is a multipart, its part 1.1 is another, and so on.
func nestedMessage(levels int, eol string) []byte {
	var b strings.Builder
	line := func(s string) { b.WriteString(s + eol) }
	line("From: anna@example.test")
	line(`Content-Type: multipart/mixed; boundary="n0"`)
	line("")
	for depth := 1; depth < levels; depth++ {
		line(fmt.Sprintf("--n%d", depth-1))
		line(fmt.Sprintf(`Content-Type: multipart/mixed; boundary="n%d"`, depth))
		line("")
	}
	line(fmt.Sprintf("--n%d", levels-1))
	line("Content-Type: text/plain")
	line("")
	line("Ganz unten")
	for depth := levels - 1; depth >= 0; depth-- {
		line(fmt.Sprintf("--n%d--", depth))
	}
	return []byte(b.String())
}

// depthPosition is the part number of nestedMessage's part at depth.
func depthPosition(depth int) string {
	return "1" + strings.Repeat(".1", depth-1)
}

// nthLine returns the offset of the nth line that equals line, counting from 1.
func nthLine(t *testing.T, input []byte, line string, n int, eol string) int {
	t.Helper()
	offset := 0
	for range n {
		i := bytes.Index(input[offset:], []byte(eol+line+eol))
		if i < 0 {
			t.Fatalf("the input has fewer than %d lines %q", n, line)
		}
		offset += i + len(eol)
	}
	return offset
}

func closers(eol string, boundaries ...string) string {
	var b strings.Builder
	for _, boundary := range boundaries {
		b.WriteString("--" + boundary + "--" + eol)
	}
	return b.String()
}

type scanCase struct {
	name         string
	input        []byte
	wantOutput   []byte
	wantProblems []Problem
}

func runScanCases(t *testing.T, cases []scanCase) {
	t.Helper()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			before := bytes.Clone(c.input)
			output, problems := scanParts(c.input)
			if !bytes.Equal(c.input, before) {
				t.Fatal("scanParts changed its input")
			}
			if !bytes.Equal(output, c.wantOutput) {
				t.Errorf("output differs from the expected one:\n%s", describeCut(output, c.wantOutput))
			}
			if !reflect.DeepEqual(problems, c.wantProblems) {
				t.Errorf("problems %s, want %s", describe(problems), describe(c.wantProblems))
			}
			if len(output) > 0 && !bytes.Equal(output, c.input) {
				// A cut is a copy, so writing to it leaves the input alone.
				output[0] ^= 0xff
				if !bytes.Equal(c.input, before) {
					t.Error("the output shares memory with the input")
				}
			}
		})
	}
}

// describeCut shows where two outputs part.
func describeCut(got, want []byte) string {
	i := 0
	for i < len(got) && i < len(want) && got[i] == want[i] {
		i++
	}
	tail := func(b []byte) string { return fmt.Sprintf("%q", b[i:min(len(b), i+80)]) }
	return fmt.Sprintf("at byte %d of %d (want %d): got %s, want %s", i, len(got), len(want), tail(got), tail(want))
}

func TestBoundaryScanner(t *testing.T) {
	var cases []scanCase
	for _, o := range []flatOptions{
		{eol: "\r\n", quoted: true},
		{eol: "\n", quoted: true},
		{eol: "\n"},
		{eol: "\r\n", quoted: true, preamble: true, epilogue: true},
	} {
		label := fmt.Sprintf("eol %q, quoted %t, preamble %t, epilogue %t", o.eol, o.quoted, o.preamble, o.epilogue)
		fits := flatMessage(1000, o)
		cases = append(cases, scanCase{name: "1,000 flat parts, " + label, input: fits, wantOutput: fits})

		over := flatMessage(1001, o)
		cut := nthLine(t, over, "--flat", 1001, o.eol)
		cases = append(cases, scanCase{
			name:         "1,001 flat parts, " + label,
			input:        over,
			wantOutput:   []byte(string(over[:cut]) + closers(o.eol, "flat")),
			wantProblems: []Problem{{Position: "1001", DeclaredType: "text/plain", Kind: TooManyParts}},
		})
	}

	// A multipart is a part too: 1 holds 1.1 to 1.999, so 2 is the 1,001st.
	var b strings.Builder
	b.WriteString("From: anna@example.test\nContent-Type: multipart/mixed; boundary=top\n\n")
	b.WriteString("--top\nContent-Type: multipart/alternative; boundary=inner\n\n")
	for range 999 {
		b.WriteString("--inner\nContent-Type: text/plain\n\nText\n")
	}
	b.WriteString("--inner--\n--top\nContent-Type: Application/PDF; name=\"a.pdf\"\n\nPDF\n--top--\n")
	nested := []byte(b.String())
	cases = append(cases, scanCase{
		name:         "the 1,001st part after a multipart's parts",
		input:        nested,
		wantOutput:   []byte(string(nested[:nthLine(t, nested, "--top", 2, "\n")]) + closers("\n", "top")),
		wantProblems: []Problem{{Position: "2", DeclaredType: "application/pdf", Kind: TooManyParts}},
	})

	for _, eol := range []string{"\r\n", "\n"} {
		fits := nestedMessage(20, eol)
		cases = append(cases, scanCase{name: fmt.Sprintf("20 levels, eol %q", eol), input: fits, wantOutput: fits})

		deep := nestedMessage(21, eol)
		var open []string
		for depth := 20; depth >= 0; depth-- {
			open = append(open, fmt.Sprintf("n%d", depth))
		}
		cases = append(cases, scanCase{
			name:         fmt.Sprintf("21 levels, eol %q", eol),
			input:        deep,
			wantOutput:   []byte(string(deep[:nthLine(t, deep, "--n20", 1, eol)]) + closers(eol, open...)),
			wantProblems: []Problem{{Position: depthPosition(21), DeclaredType: "text/plain", Kind: NestedTooDeeply}},
		})
	}

	openEnd := "From: anna@example.test\r\nContent-Type: multipart/mixed; boundary=\"o\"\r\n\r\n" +
		"--o\r\nContent-Type: multipart/alternative; boundary=\"i\"\r\n\r\n--i\r\nContent-Type: text/plain\r\n\r\nText\r\n"
	cases = append(cases,
		scanCase{
			name:         "the end with two boundaries open",
			input:        []byte(openEnd),
			wantOutput:   []byte(openEnd + closers("\r\n", "i", "o")),
			wantProblems: []Problem{{Position: "", DeclaredType: "multipart/mixed", Kind: CutShort}},
		},
		scanCase{
			name:         "the end in the middle of a line",
			input:        []byte(strings.TrimSuffix(openEnd, "\r\n")),
			wantOutput:   []byte(openEnd + closers("\r\n", "i", "o")),
			wantProblems: []Problem{{Position: "", DeclaredType: "multipart/mixed", Kind: CutShort}},
		},
	)

	// An attached message is one part, whatever it holds.
	attached := "From: anna@example.test\nContent-Type: multipart/mixed; boundary=outer\n\n" +
		"--outer\nContent-Type: text/plain\n\nSiehe Anhang\n" +
		"--outer\nContent-Type: message/rfc822\n\n" + string(flatMessage(1500, flatOptions{eol: "\n"})) +
		"--outer\nContent-Type: message/rfc822\n\n" + string(nestedMessage(30, "\n")) +
		"--outer--\n"
	cases = append(cases, scanCase{name: "attached messages aren't entered", input: []byte(attached), wantOutput: []byte(attached)})

	single := []byte("From: anna@example.test\nContent-Type: text/plain\n\n--flat\n--flat--\n")
	cases = append(cases, scanCase{name: "a message that isn't multipart", input: single, wantOutput: single})

	// A closer there would land in the header, which enmime then can't read.
	headerOnly := []byte("From: anna@example.test\nContent-Type: multipart/mixed; boundary=0")
	cases = append(cases, scanCase{name: "the end inside the message's header", input: headerOnly, wantOutput: headerOnly})

	runScanCases(t, cases)
}

func FuzzBoundaryScanner(f *testing.F) {
	f.Add(flatMessage(3, flatOptions{eol: "\r\n", quoted: true, preamble: true, epilogue: true}))
	f.Add(nestedMessage(4, "\n"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=a\n\n--a\nContent-Type: multipart/mixed; boundary=b\n\n--b\n\nx\n--a\n\ny"))
	f.Add([]byte("Content-Type: multipart/mixed\n\n-- \nContent-Type: multipart/related; boundary=r\n\n--r\n"))
	f.Add([]byte("Content-Type: multipart/mixed; boundary=a\n\n--a\nContent-Type: message/rfc822\n\nContent-Type: multipart/mixed; boundary=a\n\n--a--\n"))
	f.Fuzz(func(t *testing.T, input []byte) {
		before := bytes.Clone(input)
		output, problems := scanParts(input)
		if !bytes.Equal(input, before) {
			t.Fatal("scanParts changed its input")
		}
		if len(problems) == 0 && !bytes.Equal(output, input) {
			t.Fatal("scanParts changed the input without reporting a problem")
		}
		if _, err := newParser().ReadParts(bytes.NewReader(input)); err != nil {
			if _, tooMany := errors.AsType[*enmime.TooManyPartsError](err); !tooMany {
				// The header can't be read, so the message is unreadable either way.
				return
			}
		}
		if _, err := newParser().ReadParts(bytes.NewReader(output)); err != nil {
			t.Fatalf("enmime can't parse the scanner's output: %v", err)
		}
	})
}
