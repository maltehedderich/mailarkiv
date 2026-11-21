package mailparse

import (
	"bytes"
	"encoding/base64"
	"net/url"
	"strings"
)

// MaxEmbeddedImages is the most decoded bytes of embedded images one preview
// shows.
const MaxEmbeddedImages = 10_000_000

// imagePlacer turns cid: references into data: addresses, in the order the
// rewrite meets them. Only PNG, JPEG, GIF, and WebP show, typed by their
// first bytes, because the declared type is the sender's claim. A part with
// an encoding error doesn't show. An image that would bring the bytes shown
// past MaxEmbeddedImages is left out, and one shown twice counts once.
type imagePlacer struct {
	// byID holds the image/* parts by Content-ID. The first part wins when
	// two share one.
	byID    map[string]*leaf
	placed  map[*leaf]string
	shown   int
	leftOut bool
}

func newImagePlacer(leaves []leaf) *imagePlacer {
	p := &imagePlacer{byID: make(map[string]*leaf), placed: make(map[*leaf]string)}
	for i := range leaves {
		part := leaves[i].part
		if part.ContentID == "" || !strings.HasPrefix(declaredType(part), "image/") {
			continue
		}
		if _, ok := p.byID[part.ContentID]; !ok {
			p.byID[part.ContentID] = &leaves[i]
		}
	}
	return p
}

// place returns the data: address for the image a cid: reference names, or
// false when it doesn't show. A nil placer has no images.
func (p *imagePlacer) place(id string) (string, bool) {
	if p == nil {
		return "", false
	}
	image := p.find(id)
	if image == nil {
		return "", false
	}
	if source, ok := p.placed[image]; ok {
		return source, true
	}
	content := image.part.Content
	mediaType := signatureType(content)
	if mediaType == "" || damaged(image.part) {
		return "", false
	}
	if p.shown+len(content) > MaxEmbeddedImages {
		p.leftOut = true
		return "", false
	}
	p.shown += len(content)
	source := "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(content)
	p.placed[image] = source
	return source, true
}

// find looks a reference up as written, then unescaped (RFC 2392).
func (p *imagePlacer) find(id string) *leaf {
	if image, ok := p.byID[id]; ok {
		return image
	}
	if unescaped, err := url.PathUnescape(id); err == nil {
		return p.byID[unescaped]
	}
	return nil
}

// signatureType names the image format content starts with, or "" for any
// format the preview doesn't show.
func signatureType(content []byte) string {
	switch {
	case bytes.HasPrefix(content, []byte("\x89PNG\r\n\x1a\n")):
		return "image/png"
	case bytes.HasPrefix(content, []byte("\xff\xd8\xff")):
		return "image/jpeg"
	case bytes.HasPrefix(content, []byte("GIF87a")), bytes.HasPrefix(content, []byte("GIF89a")):
		return "image/gif"
	case len(content) >= 12 && bytes.Equal(content[0:4], []byte("RIFF")) && bytes.Equal(content[8:12], []byte("WEBP")):
		return "image/webp"
	}
	return ""
}
