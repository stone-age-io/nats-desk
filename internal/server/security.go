package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
)

// The backend opens NATS connections using whatever credentials the user has
// saved, so an unauthenticated port here is a credential-exfiltration
// primitive for any other process on the machine. Two independent guards:
//
//   - a token, so a local process cannot call the API just by knowing the port
//   - a Host allowlist, so a hostile web page cannot reach us by resolving its
//     own domain to 127.0.0.1 (DNS rebinding - the browser happily sends that
//     request, and SameSite alone does not stop it)
//
// Neither is sufficient alone. The token stops process-to-process access; the
// Host check stops browser-mediated access from another origin.

const sessionCookie = "nats_desk_session"

// newToken returns a URL-safe random token. Panics on failure: if the system
// CSPRNG is unavailable we must not fall back to anything weaker.
func newToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("nats-desk: cannot read random bytes for session token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// allowedHost reports whether r.Host is one we will answer on.
//
// Matched against the literal host:port we are listening on, not a suffix or
// pattern - "127.0.0.1:4111.evil.com" must not pass.
func (s *Server) allowedHost(host string) bool {
	for _, h := range s.hosts {
		if host == h {
			return true
		}
	}
	return false
}

// checkHost rejects any request whose Host header we do not recognise.
// Applied to everything, including static assets, because the rebinding
// attack does not care which path it lands on.
func (s *Server) checkHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.allowedHost(r.Host) {
			http.Error(w, "bad host", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// claimToken handles the one-time handoff: the URL we hand the browser at
// startup carries ?token=..., which is exchanged for a cookie and then
// redirected away so the token does not linger in history, titles or logs.
//
// Returns true if it handled the request.
func (s *Server) claimToken(w http.ResponseWriter, r *http.Request) bool {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		return false
	}

	// Constant time: the comparison is cheap to trigger repeatedly.
	if subtle.ConstantTimeCompare([]byte(tok), []byte(s.token)) != 1 {
		http.Error(w, "invalid token", http.StatusForbidden)
		return true
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    s.token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		// No Secure flag: we are plain http on loopback, which browsers
		// already treat as a secure context. Setting it would stop the
		// cookie being stored at all.
	})

	q := r.URL.Query()
	q.Del("token")
	dest := r.URL.Path
	if enc := q.Encode(); enc != "" {
		dest += "?" + enc
	}
	http.Redirect(w, r, dest, http.StatusFound)
	return true
}

// authed reports whether the request carries our session cookie.
func (s *Server) authed(r *http.Request) bool {
	if s.dev {
		return true
	}
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(s.token)) == 1
}

// requireAuth gates the API and WebSocket. Static assets are deliberately
// left open: they are not secret, and serving index.html is what lets the
// app render the "relaunch nats-desk" message when the session has gone.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.authed(r) {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
