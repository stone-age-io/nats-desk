package server

import (
	"encoding/base64"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func quietLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// The whole point of persisting the token is that a browser stays
// authenticated across a restart. If a second call to resolveToken ever mints
// a fresh token, every open window silently starts failing with 401 and the
// only visible symptom is an app that stopped working overnight.
func TestResolveTokenIsStableAcrossRestarts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "token")

	first := resolveToken(path, quietLogger())
	if first == "" {
		t.Fatal("resolveToken returned an empty token")
	}
	if got := resolveToken(path, quietLogger()); got != first {
		t.Errorf("token changed across restarts: %q then %q", first, got)
	}
	if got := ReadToken(path); got != first {
		t.Errorf("ReadToken = %q, want %q", got, first)
	}
}

// A token file that is not a well-formed token must be replaced rather than
// used. Trusting a truncated or hand-edited file would weaken the token to
// whatever is in it - in the empty case, to nothing at all.
func TestReadTokenRejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	tests := []struct {
		name    string
		content string
	}{
		{"empty", ""},
		{"whitespace", "   \n"},
		{"truncated", base64.RawURLEncoding.EncodeToString(make([]byte, 8))},
		{"too long", base64.RawURLEncoding.EncodeToString(make([]byte, 64))},
		{"not base64", strings.Repeat("!", 43)},

		// Go's base64 decoder ignores embedded newlines, so this decodes to
		// exactly 32 bytes and would be accepted on a decode check alone. It
		// must not be: the newline is stripped out of the cookie by
		// http.SetCookie, so the stored token could never match one again.
		{"embedded newline", base64.RawURLEncoding.EncodeToString(make([]byte, 32)) + "\nAAAA"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := filepath.Join(dir, tt.name)
			if err := os.WriteFile(path, []byte(tt.content), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := ReadToken(path); got != "" {
				t.Errorf("ReadToken accepted %q, returning %q", tt.content, got)
			}

			// And the bad file must be replaced by a usable one, not
			// inherited: a process that cannot mint a token cannot serve.
			tok := resolveToken(path, quietLogger())
			if ReadToken(path) != tok {
				t.Errorf("resolveToken did not store its replacement token")
			}
		})
	}
}

func TestReadTokenMissingFile(t *testing.T) {
	if got := ReadToken(filepath.Join(t.TempDir(), "absent")); got != "" {
		t.Errorf("ReadToken on a missing file = %q, want empty", got)
	}
	if got := ReadToken(""); got != "" {
		t.Errorf("ReadToken(\"\") = %q, want empty", got)
	}
}

// An unwritable path must not stop the app starting. It costs the convenience
// and nothing else, so it is a warning rather than a failure.
func TestResolveTokenSurvivesUnwritablePath(t *testing.T) {
	// A path whose parent is a file, not a directory - unwritable on every
	// platform without needing permissions to differ between them.
	parent := filepath.Join(t.TempDir(), "notadir")
	if err := os.WriteFile(parent, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}

	tok := resolveToken(filepath.Join(parent, "token"), quietLogger())
	if tok == "" {
		t.Error("resolveToken returned empty rather than falling back to an in-memory token")
	}
}

// An empty TokenPath is how -dev asks for no persistence at all.
func TestResolveTokenWithoutPathIsEphemeral(t *testing.T) {
	if a, b := resolveToken("", quietLogger()), resolveToken("", quietLogger()); a == b {
		t.Error("resolveToken with no path returned the same token twice")
	}
}

// A trailing newline is the one malformation that must be forgiven rather
// than rejected: any editor opening this file adds one, and treating it as a
// bad token would silently mint a new one and log every browser out.
func TestReadTokenTrimsTrailingNewline(t *testing.T) {
	want := base64.RawURLEncoding.EncodeToString(make([]byte, tokenBytes))
	path := filepath.Join(t.TempDir(), "token")

	for _, suffix := range []string{"\n", "\r\n", "  \n", "\n\n"} {
		if err := os.WriteFile(path, []byte(want+suffix), 0o600); err != nil {
			t.Fatal(err)
		}
		if got := ReadToken(path); got != want {
			t.Errorf("ReadToken with %q suffix = %q, want %q", suffix, got, want)
		}
	}
}
