package mailparse

import "testing"

// document is how the rewrite renders a body that names no html, head, or
// body element: the parser adds them.
func document(body string) string {
	return "<html><head></head><body>" + body + "</body></html>"
}

func TestRewrite(t *testing.T) {
	tests := []struct {
		name, source, want string
	}{
		{
			name:   "link keeps everything but its address",
			source: `<a href="https://leak.example.test/" target="_top" title="Mehr">Mehr</a>`,
			want:   document(`<a target="_top" title="Mehr">Mehr</a>`),
		},
		{
			name:   "link targets",
			source: `<a href="https://leak.example.test/1">1</a><a href="https://leak.example.test/2" target="_self">2</a><a href="https://leak.example.test/3" target="_blank">3</a>`,
			want:   document(`<a>1</a><a target="_self">2</a><a target="_blank">3</a>`),
		},
		{
			name:   "image map area",
			source: `<map name="karte"><area shape="rect" coords="0,0,9,9" href="https://leak.example.test/area" alt="Feld"></map>`,
			want:   document(`<map name="karte"><area shape="rect" coords="0,0,9,9" alt="Feld"/></map>`),
		},
		{
			name:   "SVG links, with and without the xlink prefix",
			source: `<svg><a href="https://leak.example.test/svg"><text>Eins</text></a><a xlink:href="https://leak.example.test/xlink"><text>Zwei</text></a></svg>`,
			want:   document(`<svg><a><text>Eins</text></a><a><text>Zwei</text></a></svg>`),
		},
		{
			// With scripting off, as in the frame, a noscript's content is
			// markup, so the link inside is a link. The paragraph before it
			// puts it in the body: one first in a fragment opens in the head.
			name:   "link inside noscript",
			source: `<p>Vor</p><noscript><p><a href="https://leak.example.test/noscript">Ohne Skript</a></p></noscript>`,
			want:   document(`<p>Vor</p><span><p><a>Ohne Skript</a></p></span>`),
		},
		{
			// Rendered as noscript, this text would go out unescaped, and the
			// frame would read it as a link.
			name:   "markup written as text inside noscript",
			source: `<p>Vor</p><noscript>&lt;a href=https://leak.example.test/&gt;Klick&lt;/a&gt;</noscript>`,
			want:   document(`<p>Vor</p><span>&lt;a href=https://leak.example.test/&gt;Klick&lt;/a&gt;</span>`),
		},
		{
			name:   "noscript in the head",
			source: `<html><head><noscript><link rel="stylesheet" href="https://leak.example.test/s.css"></noscript></head><body>Text</body></html>`,
			want:   `<html><head><span></span></head><body>Text</body></html>`,
		},
		{
			name: "link, meta, and base in the head",
			source: `<html><head><base href="https://leak.example.test/" target="_top">` +
				`<meta http-equiv="refresh" content="0;url=https://leak.example.test/refresh">` +
				`<meta name="referrer" content="unsafe-url">` +
				`<meta charset="utf-8">` +
				`<link rel="prefetch" href="https://leak.example.test/prefetch">` +
				`<link rel="dns-prefetch" href="https://leak.example.test">` +
				`<title>Betreff</title></head><body><p>Text</p></body></html>`,
			want: `<html><head><title>Betreff</title></head><body><p>Text</p></body></html>`,
		},
		{
			name:   "link and meta in the body",
			source: `<p>Text</p><link rel="preload" href="https://leak.example.test/preload.js" as="script"><meta http-equiv="refresh" content="1">`,
			want:   document(`<p>Text</p>`),
		},
		{
			name: "ping and referrerpolicy",
			source: `<a href="https://leak.example.test/" ping="https://leak.example.test/ping">Ping</a>` +
				`<img src="https://leak.example.test/image.png" referrerpolicy="unsafe-url" alt="Bild">` +
				`<iframe src="https://leak.example.test/frame" referrerpolicy="unsafe-url"></iframe>`,
			want: document(`<a>Ping</a><img src="https://leak.example.test/image.png" alt="Bild"/>` +
				`<iframe src="https://leak.example.test/frame"></iframe>`),
		},
		{
			name:   "inline styles stay",
			source: `<p style="color: red; background: url(https://leak.example.test/b.png)">Rot</p><style>p { color: blue }</style>`,
			want:   document(`<p style="color: red; background: url(https://leak.example.test/b.png)">Rot</p><style>p { color: blue }</style>`),
		},
		{
			name:   "comments go",
			source: `<p>Vor<!-- <a href="https://leak.example.test/">versteckt</a> -->her</p>`,
			want:   document(`<p>Vorher</p>`),
		},
		{
			name:   "an image with an address keeps it",
			source: `<img src="https://leak.example.test/image.png" alt="Bild">`,
			want:   document(`<img src="https://leak.example.test/image.png" alt="Bild"/>`),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewrite(tt.source, nil).HTML; got != tt.want {
				t.Errorf("rewrite gave\n%s\nwant\n%s", got, tt.want)
			}
		})
	}
}

func TestRemoteImages(t *testing.T) {
	tests := []struct {
		name, source string
		want         bool
	}{
		{"https image", `<img src="https://images.example.org/a.png">`, true},
		{"http image", `<img src="http://images.example.org/a.png">`, true},
		{"scheme in capitals", `<img src="HTTPS://images.example.org/a.png">`, true},
		{"spaces around the address", `<img src="  https://images.example.org/a.png ">`, true},
		{"srcset", `<img srcset="https://images.example.org/a.png 2x">`, true},
		{"srcset after a data URL", `<picture><source srcset="data:image/gif;base64,R0lGODlh 1x, http://images.example.org/b.png 2x"><img alt="Bild"></picture>`, true},
		{"background attribute", `<table background="http://images.example.org/b.png"><tr><td>Zelle</td></tr></table>`, true},
		{"url in a style attribute", `<div style="background: url( 'https://images.example.org/b.png' )">Text</div>`, true},
		{"url in a style element", `<style>.kopf { background-image: url("http://images.example.org/b.png") }</style>`, true},
		{"embedded image", `<img src="cid:logo@example.test">`, false},
		{"data URL", `<img src="data:image/gif;base64,R0lGODlhAQABAAAAACw=">`, false},
		{"link to a page", `<a href="https://www.example.org/">Seite</a>`, false},
		{"frame", `<iframe src="https://www.example.org/"></iframe>`, false},
		{"style without an address", `<p style="color: red">Rot</p><style>p { margin: 0 }</style>`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := rewrite(tt.source, nil).RemoteImages; got != tt.want {
				t.Errorf("RemoteImages = %t, want %t", got, tt.want)
			}
		})
	}
}
