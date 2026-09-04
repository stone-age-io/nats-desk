//go:build !windows && !darwin

package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const supported = true

// The XDG autostart directory, read by every desktop environment that
// implements the spec. On a machine with no desktop session this file is
// inert rather than wrong, which is the right failure: a headless box has
// nobody to sign in and open a browser window either.
const desktopEntry = `[Desktop Entry]
Type=Application
Name=nats-desk
Comment=Local NATS client
Exec="%s" -no-browser
Terminal=false
X-GNOME-Autostart-enabled=true
`

func path() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, "autostart", "nats-desk.desktop"), nil
}

func entry() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(desktopEntry, exe), nil
}

func read() (string, error) { return readFile(path) }

func write(v string) error { return writeFile(path, v) }

func remove() error { return removeFile(path) }
