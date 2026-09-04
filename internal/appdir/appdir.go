// Package appdir resolves the two per-user directories nats-desk writes to.
//
// The split is the platform convention - %APPDATA% against %LOCALAPPDATA%,
// ~/.config against ~/.cache - and it is load-bearing here rather than
// decorative. Config holds the one thing that must survive a restart, the
// session token, and deleting it is how a user revokes every browser session
// at once. Cache holds the log, which can be deleted at any time by the user
// or the OS with no consequence beyond losing the last run's diagnostics.
package appdir

import (
	"os"
	"path/filepath"
)

const dirName = "nats-desk"

// Config is where durable per-user state lives. Created if missing.
func Config() (string, error) {
	return ensure(os.UserConfigDir())
}

// Cache is where disposable per-user state lives. Created if missing.
func Cache() (string, error) {
	return ensure(os.UserCacheDir())
}

// ensure takes the (dir, err) pair straight from os.User*Dir so callers stay
// one line. 0700 because the token lives here; on Windows the mode is not
// enforced and the ACL inherited from the user profile is what protects it.
func ensure(base string, err error) (string, error) {
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, dirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
