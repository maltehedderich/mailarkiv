package mailparse

import (
	"net/url"
	"strings"

	"golang.org/x/net/html"
)

// AttachmentKind is an attachment kind's wire word.
type AttachmentKind string

const (
	KindFile          AttachmentKind = "file"
	KindMessage       AttachmentKind = "message"
	KindEmbeddedImage AttachmentKind = "embedded_image"
)

// Attachment is any part other than the displayed bodies. Containers aren't
// listed.
type Attachment struct {
	Position string  `json:"position"`
	Name     *string `json:"name,omitempty"`
	// DeclaredType is "" when the part declares none.
	DeclaredType string         `json:"declared_type,omitempty"`
	Size         int64          `json:"size"`
	Damaged      bool           `json:"damaged,omitempty"`
	Kind         AttachmentKind `json:"kind"`
}

func attachments(leaves []leaf, plain, htmlLeaf *leaf, htmlBody *Body) []Attachment {
	var referenced map[string]bool
	if htmlBody != nil {
		referenced = contentIDReferences(htmlBody.Content)
	}
	var list []Attachment
	for i := range leaves {
		item := &leaves[i]
		if item == plain || item == htmlLeaf {
			continue
		}
		declared := declaredType(item.part)
		kind := KindFile
		switch {
		case declared == "message/rfc822":
			kind = KindMessage
		case strings.HasPrefix(declared, "image/") && item.part.ContentID != "" && referenced[item.part.ContentID]:
			kind = KindEmbeddedImage
		}
		list = append(list, Attachment{
			Position:     item.position,
			Name:         fileName(item.part),
			DeclaredType: declared,
			Size:         int64(len(item.part.Content)),
			Damaged:      damaged(item.part),
			Kind:         kind,
		})
	}
	return list
}

// contentIDReferences collects the Content-IDs an HTML body references
// through cid: addresses (RFC 2392), both as written and unescaped.
func contentIDReferences(source string) map[string]bool {
	referenced := make(map[string]bool)
	tokenizer := html.NewTokenizer(strings.NewReader(source))
	for {
		switch tokenizer.Next() {
		case html.ErrorToken:
			return referenced
		case html.StartTagToken, html.SelfClosingTagToken:
			for {
				_, value, more := tokenizer.TagAttr()
				if id, ok := cutPrefixFold(string(value), "cid:"); ok && id != "" {
					referenced[id] = true
					if unescaped, err := url.PathUnescape(id); err == nil {
						referenced[unescaped] = true
					}
				}
				if !more {
					break
				}
			}
		}
	}
}

func cutPrefixFold(s, prefix string) (string, bool) {
	if len(s) < len(prefix) || !strings.EqualFold(s[:len(prefix)], prefix) {
		return s, false
	}
	return s[len(prefix):], true
}
