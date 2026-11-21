package web

import (
	"net/http"
	"strings"
)

const apiPrefix = "/api/"

// withHeaders sets the headers every response carries, before anything else
// runs, so a rejected request carries them too. The marker tells the client
// the answer came from MailArkiv and not a proxy. frame-ancestors has to be a
// header, because a <meta> policy can't carry it. A handler replaces a policy
// with replacePolicies, so the response still carries one of each.
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

// withCrossOriginCheck refuses a state-changing request from another site
// before any route runs, so it never reads a body or changes the archive. GET
// and HEAD always pass, and no route changes anything on them.
func withCrossOriginCheck(next http.Handler) http.Handler {
	protection := http.NewCrossOriginProtection()
	protection.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		writeError(w, http.StatusForbidden, "cross_origin")
	}))
	return protection.Handler(next)
}

// replacePolicies replaces the Content-Security-Policy withHeaders set and,
// unless referrer is empty, the Referrer-Policy. A browser applies every
// Content-Security-Policy it gets, so adding one beside the global policy
// would only narrow it.
func replacePolicies(w http.ResponseWriter, contentSecurity, referrer string) {
	header := w.Header()
	header.Set("Content-Security-Policy", contentSecurity)
	if referrer != "" {
		header.Set("Referrer-Policy", referrer)
	}
}
