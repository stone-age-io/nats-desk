//go:build !windows && !darwin

package scheme

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
)

const desktopFile = "nats-desk-url.desktop"

// NoDisplay because this entry is a scheme handler, not something anyone
// should find in an application menu and launch with no URL to open.
const desktopEntry = `[Desktop Entry]
Type=Application
Name=nats-desk
Exec="%s" %%u
Terminal=false
NoDisplay=true
MimeType=x-scheme-handler/%s;
`

func register() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return err
	}

	dir := filepath.Join(home, ".local", "share", "applications")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	content := fmt.Sprintf(desktopEntry, exe, Name)
	path := filepath.Join(dir, desktopFile)

	if current, err := os.ReadFile(path); err == nil && string(current) == content {
		return nil
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return err
	}

	// Both of these are best effort. The desktop file is what carries the
	// association; these two only make the desktop environment notice it
	// without a re-login, and neither command exists on a minimal install.
	if p, err := exec.LookPath("update-desktop-database"); err == nil {
		_ = exec.Command(p, dir).Run()
	}
	if p, err := exec.LookPath("xdg-mime"); err == nil {
		_ = exec.Command(p, "default", desktopFile, "x-scheme-handler/"+Name).Run()
	}
	return nil
}
