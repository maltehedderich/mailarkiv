package mailparse

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

// Each starts with its format's signature. The rest stands in for the image.
var (
	pngBytes  = []byte("\x89PNG\r\n\x1a\nsynthetic png")
	jpegBytes = []byte("\xff\xd8\xff\xe0synthetic jpeg")
	gifBytes  = []byte("GIF89asynthetic gif")
	webpBytes = []byte("RIFF\x10\x00\x00\x00WEBPVP8 synthetic")
	svgBytes  = []byte(`<svg xmlns="http://www.w3.org/2000/svg"><rect width="1" height="1"/></svg>`)
	tiffBytes = []byte("II*\x00\x08\x00\x00\x00synthetic tiff")
)

// inlinePart is one part after the HTML body of a multipart/related message.
type inlinePart struct {
	contentID, declared string
	content             []byte
	// encoded, if set, is the part's base64 as sent, in place of content's.
	encoded string
}

// related builds a multipart/related message with the HTML body first.
func related(body string, parts ...inlinePart) []byte {
	var b strings.Builder
	b.WriteString("From: anna@example.test\r\nSubject: Bilder\r\nMIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: multipart/related; boundary=\"rel\"\r\n\r\n")
	b.WriteString("--rel\r\nContent-Type: text/html; charset=utf-8\r\n\r\n" + body + "\r\n")
	for _, part := range parts {
		encoded := part.encoded
		if encoded == "" {
			encoded = base64.StdEncoding.EncodeToString(part.content)
		}
		fmt.Fprintf(&b, "--rel\r\nContent-Type: %s\r\nContent-ID: <%s>\r\nContent-Transfer-Encoding: base64\r\n\r\n", part.declared, part.contentID)
		for len(encoded) > 76 {
			b.WriteString(encoded[:76] + "\r\n")
			encoded = encoded[76:]
		}
		b.WriteString(encoded + "\r\n")
	}
	b.WriteString("--rel--\r\n")
	return []byte(b.String())
}

func dataURL(mediaType string, content []byte) string {
	return "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(content)
}

// imageSources lists each img element's src in document order, with "" for
// one that has none.
func imageSources(t *testing.T, rendered string) []string {
	t.Helper()
	doc, err := html.Parse(strings.NewReader(rendered))
	if err != nil {
		t.Fatal(err)
	}
	var sources []string
	var visit func(*html.Node)
	visit = func(n *html.Node) {
		if n.Type == html.ElementNode && n.Data == "img" {
			source := ""
			for _, attribute := range n.Attr {
				if attribute.Key == "src" {
					source = attribute.Val
				}
			}
			sources = append(sources, source)
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			visit(c)
		}
	}
	visit(doc)
	return sources
}

func readPreview(t *testing.T, original []byte) *Preview {
	t.Helper()
	_, preview := ReadView(original)
	if preview == nil {
		t.Fatal("ReadView gave no preview for a message with an HTML body")
	}
	return preview
}

func TestEmbeddedImages(t *testing.T) {
	t.Run("typed by the signature", func(t *testing.T) {
		original := related(
			`<img src="cid:png@example.test"><img src="cid:jpeg@example.test"><img src="cid:gif@example.test"><img src="cid:webp@example.test">`,
			// The declared types are wrong on purpose: the bytes decide.
			inlinePart{contentID: "png@example.test", declared: "image/jpeg", content: pngBytes},
			inlinePart{contentID: "jpeg@example.test", declared: "image/png", content: jpegBytes},
			inlinePart{contentID: "gif@example.test", declared: "image/gif", content: gifBytes},
			inlinePart{contentID: "webp@example.test", declared: "image/webp", content: webpBytes},
		)
		preview := readPreview(t, original)
		want := []string{
			dataURL("image/png", pngBytes), dataURL("image/jpeg", jpegBytes),
			dataURL("image/gif", gifBytes), dataURL("image/webp", webpBytes),
		}
		if got := imageSources(t, preview.HTML); !reflect.DeepEqual(got, want) {
			t.Errorf("sources %q, want %q", got, want)
		}
		if preview.EmbeddedImagesLeftOut || preview.RemoteImages {
			t.Errorf("left out %t, remote %t, want neither", preview.EmbeddedImagesLeftOut, preview.RemoteImages)
		}
	})

	t.Run("formats that don't show", func(t *testing.T) {
		original := related(
			`<img src="cid:svg@example.test" alt="SVG"><img src="cid:fake@example.test" alt="SVG as PNG"><img src="cid:tiff@example.test" alt="TIFF"><img src="cid:broken@example.test" alt="Kaputt"><p>Danach</p>`,
			inlinePart{contentID: "svg@example.test", declared: "image/svg+xml", content: svgBytes},
			inlinePart{contentID: "fake@example.test", declared: "image/png", content: svgBytes},
			inlinePart{contentID: "tiff@example.test", declared: "image/tiff", content: tiffBytes},
			// A PNG with a character outside the base64 alphabet.
			inlinePart{contentID: "broken@example.test", declared: "image/png", encoded: "iVBORw0K*GgpzeW50aGV0aWMgcG5n"},
		)
		preview := readPreview(t, original)
		if got, want := imageSources(t, preview.HTML), []string{"", "", "", ""}; !reflect.DeepEqual(got, want) {
			t.Errorf("sources %q, want none", got)
		}
		if !strings.Contains(preview.HTML, "<p>Danach</p>") {
			t.Errorf("the rest didn't render: %s", preview.HTML)
		}
		if preview.EmbeddedImagesLeftOut {
			t.Error("left out is set, but no image was left out for the limit")
		}
	})

	t.Run("a missing part", func(t *testing.T) {
		original := related(
			`<img src="cid:missing@example.test" alt="Fehlt"><img src="cid:present@example.test" alt="Da"><p>Danach</p>`,
			inlinePart{contentID: "present@example.test", declared: "image/png", content: pngBytes},
		)
		preview := readPreview(t, original)
		want := []string{"", dataURL("image/png", pngBytes)}
		if got := imageSources(t, preview.HTML); !reflect.DeepEqual(got, want) {
			t.Errorf("sources %q, want %q", got, want)
		}
		if !strings.Contains(preview.HTML, "<p>Danach</p>") {
			t.Errorf("the rest didn't render: %s", preview.HTML)
		}
	})

	t.Run("an escaped reference", func(t *testing.T) {
		// RFC 2392 addresses are URL-encoded, so %40 stands for @.
		original := related(`<img src="CID:logo%40example.test">`,
			inlinePart{contentID: "logo@example.test", declared: "image/png", content: pngBytes})
		want := []string{dataURL("image/png", pngBytes)}
		if got := imageSources(t, readPreview(t, original).HTML); !reflect.DeepEqual(got, want) {
			t.Errorf("sources %q, want %q", got, want)
		}
	})

	t.Run("the 10,000,000-byte limit", func(t *testing.T) {
		// Each image is 4,000,000 bytes. The first two show 8,000,000 bytes,
		// the third would bring that to 12,000,000, and the 1,000,000-byte
		// fourth still fits.
		big := func(fill byte) []byte {
			return append(append([]byte(nil), pngBytes...), bytes.Repeat([]byte{fill}, 4_000_000-len(pngBytes))...)
		}
		first, second, third := big('a'), big('b'), big('c')
		small := append(append([]byte(nil), gifBytes...), bytes.Repeat([]byte{'d'}, 1_000_000-len(gifBytes))...)
		original := related(
			`<img src="cid:1@example.test"><img src="cid:2@example.test"><img src="cid:1@example.test"><img src="cid:3@example.test"><img src="cid:4@example.test">`,
			inlinePart{contentID: "1@example.test", declared: "image/png", content: first},
			inlinePart{contentID: "2@example.test", declared: "image/png", content: second},
			inlinePart{contentID: "3@example.test", declared: "image/png", content: third},
			inlinePart{contentID: "4@example.test", declared: "image/gif", content: small},
		)
		preview := readPreview(t, original)
		// The first image shows twice and counts once.
		want := []string{
			dataURL("image/png", first), dataURL("image/png", second), dataURL("image/png", first),
			"", dataURL("image/gif", small),
		}
		got := imageSources(t, preview.HTML)
		if len(got) != len(want) {
			t.Fatalf("%d images, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("image %d has a source of %d bytes, want %d", i+1, len(got[i]), len(want[i]))
			}
		}
		if !preview.EmbeddedImagesLeftOut {
			t.Error("left out isn't set")
		}
	})

	t.Run("every embedded image stays an attachment", func(t *testing.T) {
		original := related(`<img src="cid:png@example.test"><img src="cid:svg@example.test">`,
			inlinePart{contentID: "png@example.test", declared: "image/png", content: pngBytes},
			inlinePart{contentID: "svg@example.test", declared: "image/svg+xml", content: svgBytes},
		)
		message, _ := ReadView(original)
		var kinds []AttachmentKind
		for _, attachment := range message.Attachments {
			kinds = append(kinds, attachment.Kind)
		}
		if want := []AttachmentKind{KindEmbeddedImage, KindEmbeddedImage}; !reflect.DeepEqual(kinds, want) {
			t.Errorf("attachment kinds %q, want %q", kinds, want)
		}
	})

	t.Run("no HTML body", func(t *testing.T) {
		if _, preview := ReadView([]byte("From: anna@example.test\r\n\r\nNur Text\r\n")); preview != nil {
			t.Errorf("preview %+v for a message without HTML", preview)
		}
	})
}
