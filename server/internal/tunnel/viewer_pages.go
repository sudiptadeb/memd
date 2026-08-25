package tunnel

import (
	"fmt"
	"net/http"
)

// Small server-rendered status pages for the viewer surfaces. The management
// UI itself lives in the dashboard SPA (its termulaa section, which GET /rc
// redirects into); what remains here are the minimal answers the viewer needs
// before a terminal can be proxied: sign in, pair, or agent offline.

// serveSignInPage answers an unauthenticated path-mode viewer request
// (/rc/t/<agent>/) with a pointer to memd's login rather than a bare 401.
func serveSignInPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusUnauthorized)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>memd · sign in</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 32em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">Sign in required</h1>
<p>This remote terminal needs a memd login.
<a href="/">Sign in</a>, then reload this page.</p>
</body></html>
`))
}

// The pages below are served on the viewer surface (view host or /rc/t/ path
// mode), which SplitViewer routes outside memd's global security headers, so
// they carry their own restrictive per-response policy.

func viewerPageHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("Content-Type", "text/html; charset=utf-8")
	h.Set("Cache-Control", "no-store")
	h.Set("Content-Security-Policy",
		"default-src 'none'; style-src 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
	h.Set("X-Content-Type-Options", "nosniff")
	h.Set("Referrer-Policy", "same-origin")
}

// servePairPage is the view host's answer to a browser with no valid pairing
// cookie.
func servePairPage(w http.ResponseWriter, status int) {
	viewerPageHeaders(w)
	w.WriteHeader(status)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>Pair this browser</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 34em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">Pair this browser</h1>
<p>This terminal needs a valid tunnel token. Open the pairing link from your
memd <code>/rc</code> page (it looks like <code>/?t=&lt;token&gt;</code>), or
mint a fresh token there if yours has expired.</p>
</body></html>
`))
}

// serveAgentOffline is shown when the token is valid but the agent has no
// live tunnel.
func serveAgentOffline(w http.ResponseWriter) {
	serveStatusPage(w, http.StatusServiceUnavailable, "Agent offline",
		"No tunnel from this agent is currently connected. Start "+
			"<code>termulaa -rc</code> on the target machine, then reload.")
}

func serveStatusPage(w http.ResponseWriter, status int, title, body string) {
	viewerPageHeaders(w)
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html lang="en"><head><meta charset="utf-8"><title>%s</title></head>
<body style="font-family: system-ui, sans-serif; max-width: 34em; margin: 4em auto;">
<h1 style="font-size: 1.3em;">%s</h1>
<p>%s</p>
</body></html>
`, title, title, body)
}
