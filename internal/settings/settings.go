// Package settings holds the preferences that belong to the process rather
// than to any one window.
//
// Almost everything the UI remembers - profiles, templates, layout - lives in
// the browser's storage, keyed by origin. What is here is the exception
// because the backend is what acts on it, and every window has to get the
// same answer: whether to quit once they have all closed cannot be decided by
// one of them, and a different browser would not share its storage anyway.
//
// It lives beside the session token in the config directory. A file that is
// missing or unreadable means the defaults, never a failure to start.
package settings

import (
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"

	"github.com/stone-age-io/nats-desk/internal/appdir"
)

const fileName = "settings.json"

// Settings is the stored form. The zero value is the default for everything,
// so a field added later reads as off from an older file.
type Settings struct {
	// QuitOnClose stops the process shortly after its last window closes,
	// rather than leaving it running for the next one.
	QuitOnClose bool `json:"quitOnClose"`
}

// mu makes Update a read-modify-write that two windows cannot interleave.
var mu sync.Mutex

// Path is the settings file, or "" if the config directory cannot be
// resolved on this system.
func Path() string {
	dir, err := appdir.Config()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, fileName)
}

// Load returns the stored settings. A file that does not exist is the
// defaults with no error; one that exists and cannot be read or parsed is the
// defaults with the error, which the caller logs and then carries on.
func Load(path string) (Settings, error) {
	var s Settings
	if path == "" {
		return s, nil
	}
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal(b, &s); err != nil {
		return Settings{}, err
	}
	return s, nil
}

// Update applies fn to the stored settings and writes the result. A file
// that could not be parsed is replaced rather than refused: the user is
// setting a value through the UI, and that is a better source than a broken
// file they cannot see.
func Update(path string, fn func(*Settings)) error {
	if path == "" {
		return errors.New("no config directory to store settings in")
	}
	mu.Lock()
	defer mu.Unlock()

	s, _ := Load(path)
	fn(&s)
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(b, '\n'), 0o600)
}
