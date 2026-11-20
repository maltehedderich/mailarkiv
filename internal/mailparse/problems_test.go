package mailparse

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jhillyerd/enmime/v2"
)

func TestProblemKinds(t *testing.T) {
	want := map[string]Kind{
		enmime.ErrorCharsetConversion:  UnknownCharset,
		enmime.ErrorCharsetDeclaration: UnknownCharset,
		enmime.ErrorMalformedBase64:    InvalidEncoding,
		enmime.ErrorContentEncoding:    InvalidEncoding,
		enmime.ErrorMalformedHeader:    MalformedHeader,
		enmime.ErrorMissingContentType: MalformedHeader,
		enmime.ErrorMissingBoundary:    MalformedStructure,
		enmime.ErrorMalformedChildPart: MalformedStructure,
		enmime.ErrorDataHasBoundary:    MalformedStructure,
		enmime.ErrorPlainTextFromHTML:  MalformedStructure,
		enmime.ErrorMissingRecipient:   MalformedStructure,
	}
	for name, kind := range want {
		if got := kindOf(name); got != kind {
			t.Errorf("kindOf(%q) = %q, want %q", name, got, kind)
		}
	}
	if got := kindOf("A name enmime might add"); got != MalformedStructure {
		t.Errorf("an unknown name gives %q, want %q", got, MalformedStructure)
	}

	// A name added in an enmime upgrade must get a decision here, not fall
	// through to malformed structure unseen.
	defined := enmimeErrorNames(t)
	for _, name := range defined {
		if _, ok := want[name]; !ok {
			t.Errorf("enmime defines the error name %q, which this test doesn't map", name)
		}
	}
	if len(defined) != len(want) {
		t.Errorf("enmime defines %d error names, and this test maps %d", len(defined), len(want))
	}
}

// enmimeErrorNames reads the Error constants from the files go list builds
// into enmime.
func enmimeErrorNames(t *testing.T) []string {
	t.Helper()
	out, err := exec.Command("go", "list", "-json", "github.com/jhillyerd/enmime/v2").Output()
	if err != nil {
		t.Fatalf("find enmime's source: %v", err)
	}
	var listed struct {
		Dir     string
		GoFiles []string
	}
	if err := json.Unmarshal(out, &listed); err != nil {
		t.Fatalf("read go list's answer: %v", err)
	}
	files := token.NewFileSet()
	var names []string
	for _, name := range listed.GoFiles {
		file, err := parser.ParseFile(files, filepath.Join(listed.Dir, name), nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		for _, decl := range file.Decls {
			general, ok := decl.(*ast.GenDecl)
			if !ok || general.Tok != token.CONST {
				continue
			}
			for _, spec := range general.Specs {
				value := spec.(*ast.ValueSpec)
				for i, ident := range value.Names {
					if !strings.HasPrefix(ident.Name, "Error") || i >= len(value.Values) {
						continue
					}
					literal, ok := value.Values[i].(*ast.BasicLit)
					if !ok || literal.Kind != token.STRING {
						continue
					}
					name, err := strconv.Unquote(literal.Value)
					if err != nil {
						t.Fatal(err)
					}
					names = append(names, name)
				}
			}
		}
	}
	if len(names) == 0 {
		t.Fatalf("found no error names in %s", listed.Dir)
	}
	return names
}

func TestKindWords(t *testing.T) {
	words := map[Kind]string{
		UnknownCharset:     "unknown_charset",
		InvalidEncoding:    "invalid_encoding",
		MalformedHeader:    "malformed_header",
		MalformedStructure: "malformed_structure",
		CutShort:           "cut_short",
		TooManyParts:       "too_many_parts",
		NestedTooDeeply:    "nested_too_deeply",
		BodyCut:            "body_cut",
		TookTooLong:        "took_too_long",
	}
	for kind, want := range words {
		if string(kind) != want {
			t.Errorf("kind %q, want %q", kind, want)
		}
	}
}

func crlf(lines ...string) []byte { return []byte(strings.Join(lines, "\r\n")) }

func TestProblems(t *testing.T) {
	t.Run("position and declared type only", func(t *testing.T) {
		got := Read(crlf(
			"From: anna@example.test",
			`Content-Type: multipart/mixed; boundary="b"`,
			"",
			"--b",
			"Content-Type: text/plain",
			"",
			"Text",
			"--b",
			`Content-Type: Application/PDF; name="a.pdf"`,
			"Content-Transfer-Encoding: base64",
			"",
			"!!!!@@@@ not base64 ####",
			"--b--",
			"",
		))
		// enmime reports several Malformed Base64 errors for this part. One
		// problem per kind and part remains, without enmime's detail text.
		want := []Problem{{Position: "2", DeclaredType: "application/pdf", Kind: InvalidEncoding}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
		if len(got.Attachments) != 1 || !got.Attachments[0].Damaged {
			t.Errorf("attachments %s, want one damaged attachment", describe(got.Attachments))
		}
	})

	t.Run("a header enmime can't read", func(t *testing.T) {
		got := Read(crlf("This line is not a header", "From: anna@example.test", "", "Body"))
		want := Message{Unreadable: true, Problems: []Problem{{Position: "", Kind: MalformedHeader}}}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("Read gave %s, want %s", describe(got), describe(want))
		}
	})

	t.Run("From isn't an address list", func(t *testing.T) {
		got := Read(crlf("From: =?UTF-8?Q?J=C3=BCrgen_vom_Empfang?=", "Subject: Hallo", "", "Body"))
		if got.From != nil || got.FromText == nil || *got.FromText != "Jürgen vom Empfang" {
			t.Errorf("From %s and FromText %s, want no addresses and the decoded text",
				describe(got.From), describe(got.FromText))
		}
		want := []Problem{{Position: "", Kind: MalformedHeader}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
		summary := got.Summary()
		if summary.SenderAddress != nil || summary.SenderName == nil || *summary.SenderName != "Jürgen vom Empfang" {
			t.Errorf("summary %s, want no address and the decoded text as the name", describe(summary))
		}
	})

	t.Run("an unknown character set", func(t *testing.T) {
		got := Read(crlf("From: anna@example.test", "Content-Type: text/plain; charset=x-unknown", "", "K\xf6ln"))
		if got.Plain == nil || got.Plain.Content != "K�ln" {
			t.Errorf("plain body %s, want a replacement character", describe(got.Plain))
		}
		want := []Problem{{Position: "1", DeclaredType: "text/plain", Kind: UnknownCharset}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
	})

	t.Run("a structure problem on the whole message", func(t *testing.T) {
		got := Read(crlf(
			"From: anna@example.test",
			`Content-Type: multipart/mixed; boundary="b"`,
			"",
			"--b",
			"Content-Type: text/plain",
			"",
			"Never closed",
		))
		want := []Problem{{Position: "", DeclaredType: "multipart/mixed", Kind: MalformedStructure}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
	})

	t.Run("a part without a type", func(t *testing.T) {
		got := Read(crlf(
			"From: anna@example.test",
			`Content-Type: multipart/mixed; boundary="b"`,
			"",
			"--b",
			"",
			"No header",
			"--b--",
			"",
		))
		want := []Problem{{Position: "1", Kind: MalformedHeader}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
	})

	t.Run("a body over the size limit", func(t *testing.T) {
		// 1,999,998 bytes, then a 3-byte character that ends at 2,000,001.
		body := strings.Repeat("a", 1_999_998) + "€"
		got := Read(crlf("From: anna@example.test", "Content-Type: text/plain; charset=utf-8", "", body))
		if got.Plain == nil {
			t.Fatal("no plain body")
		}
		if n := len(got.Plain.Content); n != 1_999_998 || !utf8.ValidString(got.Plain.Content) {
			t.Errorf("body of %d bytes, want 1,999,998 bytes of whole characters", n)
		}
		want := []Problem{{Position: "1", DeclaredType: "text/plain", Kind: BodyCut}}
		if !reflect.DeepEqual(got.Problems, want) {
			t.Errorf("problems %s, want %s", describe(got.Problems), describe(want))
		}
	})

	t.Run("a body at the size limit", func(t *testing.T) {
		body := strings.Repeat("a", 1_999_997) + "€"
		got := Read(crlf("From: anna@example.test", "Content-Type: text/plain; charset=utf-8", "", body))
		if got.Plain == nil || len(got.Plain.Content) != 2_000_000 || got.Problems != nil {
			t.Errorf("a body of exactly 2,000,000 bytes changed, with problems %s", describe(got.Problems))
		}
	})
}
