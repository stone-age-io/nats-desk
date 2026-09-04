package server

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/stone-age-io/nats-desk/internal/appdir"
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
//
// The token is persisted, and that is a deliberate weakening of the first
// guard - a token in a file is readable by anything running as this user,
// where a token held only in memory was not. It buys the thing the whole
// desktop story depends on: a browser that still holds a valid session after
// the process restarts. Without it an installed app, or simply a tab left
// open overnight, comes back to an app shell that 401s on every call, with no
// way to recover except finding the binary and running it again.
//
// The concession is smaller than it first looks. What the token guards is a
// process holding NATS credentials that are themselves files in the same user
// profile - .creds files and the nats CLI's own context store - so anything
// able to read the token could already read what it protects. The guard that
// is doing the real work here is the one against *other* users and against
// the browser, and neither is affected.

const sessionCookie = "nats_desk_session"

// tokenBytes is the entropy behind a token, before encoding. Also the length
// a stored token is checked against, so a truncated or edited file is treated
// as absent rather than used.
const tokenBytes = 32

// tokenFile lives in the config directory rather than the cache directory
// because deleting it is the documented way to revoke every session at once,
// and a path the OS may clear on its own is a poor place for that.
const tokenFile = "token"

// cookieLifetime is a year on purpose. The cookie's real lifetime control is
// the token file, and a short expiry here would produce exactly the bug this
// is meant to remove: an installed app that silently stops working one day
// with a 401 and no explanation.
const cookieLifetime = 365 * 24 * time.Hour

// newToken returns a URL-safe random token. Panics on failure: if the system
// CSPRNG is unavailable we must not fall back to anything weaker.
func newToken() string {
	b := make([]byte, tokenBytes)
	if _, err := rand.Read(b); err != nil {
		panic("nats-desk: cannot read random bytes for session token: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// TokenPath is where the session token is stored, or "" if the config
// directory cannot be resolved on this system.
func TokenPath() string {
	dir, err := appdir.Config()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, tokenFile)
}

// ReadToken returns the stored token, or "" if there is not a well-formed one.
//
// Exported because the second copy of the process needs it: when binding
// fails the port is already held, and the running instance's token is the
// only way that copy can hand a browser a URL that authenticates.
func ReadToken(path string) string {
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	// Trimmed because an editor will add a trailing newline to a file that
	// looks like text, and the length is then checked explicitly because
	// Go's base64 decoder *ignores* embedded newlines rather than rejecting
	// them. Without the length check a token with a newline in it decodes
	// happily to the right number of bytes and is accepted - and then never
	// matches, because http.SetCookie strips the newline back out of the
	// cookie it writes. The symptom is an app that is permanently
	// unauthenticated for no visible reason.
	tok := strings.TrimSpace(string(b))
	if len(tok) != base64.RawURLEncoding.EncodedLen(tokenBytes) {
		return ""
	}
	if _, err := base64.RawURLEncoding.DecodeString(tok); err != nil {
		return ""
	}
	return tok
}

// resolveToken reuses the stored token or mints and stores a new one. A
// failure to persist is not fatal: the process runs with an in-memory token,
// which is how it behaved before persistence existed. Only the convenience is
// lost, so it is logged and the app still starts.
func resolveToken(path string, log *slog.Logger) string {
	if path == "" {
		return newToken()
	}
	if tok := ReadToken(path); tok != "" {
		return tok
	}

	tok := newToken()
	// 0600 is not enforced on Windows, where the file is protected by the ACL
	// it inherits from the user's profile directory instead.
	if err := os.WriteFile(path, []byte(tok), 0o600); err != nil {
		log.Warn("could not store the session token; browsers will need a new one after each restart",
			"path", path, "err", err)
	}
	return tok
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
		// MaxAge, so this is a persistent cookie rather than a session one.
		// A session cookie is discarded when the browser closes, which would
		// undo the whole point of persisting the token: the installed app
		// would still come back unauthenticated every morning.
		MaxAge: int(cookieLifetime / time.Second),
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
