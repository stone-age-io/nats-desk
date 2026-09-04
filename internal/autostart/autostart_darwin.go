package autostart

import (
	"fmt"
	"os"
	"path/filepath"
)

const supported = true

const label = "io.stone-age.nats-desk"

// A LaunchAgent under the user's own Library, not /Library, so it runs as the
// user and needs no administrator rights.
//
// No launchctl call after writing it. launchd reads LaunchAgents at login,
// which is precisely when this is meant to take effect - loading it now would
// start a second copy on top of the one the user is already looking at.
const plist = `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>%s</string>
	<key>ProgramArguments</key>
	<array>
		<string>%s</string>
		<string>-no-browser</string>
	</array>
	<key>RunAtLoad</key>
	<true/>
</dict>
</plist>
`

func path() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "LaunchAgents", label+".plist"), nil
}

func entry() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return fmt.Sprintf(plist, label, exe), nil
}

func read() (string, error) { return readFile(path) }

func write(v string) error { return writeFile(path, v) }

func remove() error { return removeFile(path) }
