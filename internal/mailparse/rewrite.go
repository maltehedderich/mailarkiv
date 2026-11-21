package mailparse

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// Preview is an HTML body rewritten for the sandboxed preview frame. The
// frame's policy is the barrier. The rewrite closes what the policy leaves
// open: links navigate the frame, and link and meta elements still act once
// images from the internet are allowed.
type Preview struct {
	HTML string `json:"html"`
	// RemoteImages is true when the HTML references http: or https: images.
	RemoteImages bool `json:"remote_images"`
	// EmbeddedImagesLeftOut is true when an embedded image wasn't placed
	// because it would have passed MaxEmbeddedImages.
	EmbeddedImagesLeftOut bool `json:"embedded_images_left_out"`
}

// removedElements go with everything inside them.
var removedElements = map[string]bool{"link": true, "meta": true, "base": true}

// rewrite parses source as the frame does, with scripting off, so a
// noscript's content is markup and the links in it are rewritten too. It
// places embedded images through images, which may be nil, and renders the
// changed tree.
func rewrite(source string, images *imagePlacer) Preview {
	// Parsing reads from a strings.Reader, which can't fail.
	doc, _ := html.ParseWithOptions(strings.NewReader(source), html.ParseOptionEnableScripting(false))
	r := rewriter{images: images}
	r.children(doc)
	var out strings.Builder
	// Rendering into a strings.Builder fails only for a tree the parser
	// can't make.
	_ = html.Render(&out, doc)
	preview := Preview{HTML: out.String(), RemoteImages: r.remote}
	if images != nil {
		preview.EmbeddedImagesLeftOut = images.leftOut
	}
	return preview
}

type rewriter struct {
	images *imagePlacer
	remote bool
}

func (r *rewriter) children(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		switch c.Type {
		case html.CommentNode:
			// The preview doesn't show comments.
			n.RemoveChild(c)
		case html.TextNode:
			if n.Type == html.ElementNode && n.Data == "style" && hasRemoteURL(c.Data) {
				r.remote = true
			}
		case html.ElementNode:
			if removedElements[c.Data] {
				n.RemoveChild(c)
				break
			}
			r.element(c)
			r.children(c)
		}
		c = next
	}
}

func (r *rewriter) element(n *html.Node) {
	if n.Namespace == "" && n.Data == "noscript" {
		// Render writes a noscript's text unescaped, as a browser with
		// scripting on reads it. The frame reads it with scripting off, as
		// markup, so the element becomes one whose text is escaped.
		n.Data, n.DataAtom = "span", atom.Span
	}
	link := n.Data == "a" || n.Data == "area"
	image := n.Namespace == "" && n.Data == "img"
	kept := n.Attr[:0]
	for _, attribute := range n.Attr {
		switch {
		case attribute.Key == "ping" || attribute.Key == "referrerpolicy":
			continue
		// An SVG link's xlink:href arrives as href in the xlink namespace.
		case link && attribute.Key == "href":
			continue
		case attribute.Key == "srcset":
			r.remote = r.remote || srcsetIsRemote(attribute.Val)
		case attribute.Key == "background":
			r.remote = r.remote || isRemote(attribute.Val)
		case attribute.Key == "style":
			r.remote = r.remote || hasRemoteURL(attribute.Val)
		case image && attribute.Key == "src" && attribute.Namespace == "":
			if id, ok := cutPrefixFold(strings.TrimSpace(attribute.Val), "cid:"); ok {
				source, placed := r.images.place(id)
				if !placed {
					continue
				}
				attribute.Val = source
			}
			r.remote = r.remote || isRemote(attribute.Val)
		}
		kept = append(kept, attribute)
	}
	n.Attr = kept
}

// isRemote reports whether an address is http: or https:. Browsers drop
// tabs and line breaks inside an address and spaces around it.
func isRemote(address string) bool {
	address = strings.TrimSpace(strings.NewReplacer("\t", "", "\n", "", "\r", "").Replace(address))
	_, http := cutPrefixFold(address, "http:")
	_, https := cutPrefixFold(address, "https:")
	return http || https
}

// srcsetIsRemote checks each candidate in a srcset. A comma inside a data:
// address splits it wrongly, but no piece of one starts with http:.
func srcsetIsRemote(srcset string) bool {
	for candidate := range strings.SplitSeq(srcset, ",") {
		if address, _, _ := strings.Cut(strings.TrimSpace(candidate), " "); isRemote(address) {
			return true
		}
	}
	return false
}

var remoteURL = regexp.MustCompile(`(?i)url\(\s*['"]?\s*https?:`)

// hasRemoteURL reports whether CSS has a url( with an http: or https:
// address.
func hasRemoteURL(css string) bool {
	return remoteURL.MatchString(css)
}
