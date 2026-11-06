package web

import (
	"net/http"
	"strings"
)

const apiPrefix = "/api/"

// withHeaders sets the headers every response carries, before anything else
// runs, so a rejected request carries them too. The marker tells the client the
// answer came from MailArkiv and not a proxy (FR-033). frame-ancestors has to be
// a header, because a <meta> policy can't carry it (FR-007).
func withHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("MailArkiv-Response", "1")
		header.Set("Content-Security-Policy", "frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		header.Set("X-Content-Type-Options", "nosniff")
		header.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, apiPrefix) {
			header.Set("Cache-Control", "no-store")
		}
		next.ServeHTTP(w, r)
	})
}
