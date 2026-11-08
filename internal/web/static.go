// Package web is the HTTP adapter.
package web

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

const (
	fallbackPage = "app.html"
	buildPrefix  = "/_app/"
)

// Static serves the built interface from files, which holds app.html at its root.
//
// A missing file under /_app/ is a real 404, because a client asking for one
// expects a script or a stylesheet. Any other unknown path gets app.html, which
// then routes in the browser.
func Static(files fs.FS) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}

		name := strings.TrimPrefix(r.URL.Path, "/")
		switch {
		case isFile(files, name):
			// Go has no type for this extension, so it would sniff text/plain.
			if path.Ext(name) == ".webmanifest" {
				w.Header().Set("Content-Type", "application/manifest+json")
			}
			http.ServeFileFS(w, r, files, name)
		case strings.HasPrefix(r.URL.Path, buildPrefix):
			http.NotFound(w, r)
		default:
			http.ServeFileFS(w, r, files, fallbackPage)
		}
	})
}

func isFile(files fs.FS, name string) bool {
	info, err := fs.Stat(files, name)
	return err == nil && !info.IsDir()
}
