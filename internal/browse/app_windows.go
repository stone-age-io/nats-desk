package browse

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

// appCmd finds a Chromium browser to open an --app window with.
//
// The user's actual default browser is tried first, and that ordering is the
// whole reason this is not a two-line path scan. Edge is installed on every
// Windows machine, so a scan in any fixed order hands a Chrome user an Edge
// window - a different profile, none of their extensions, and an installed
// PWA that lives somewhere else. Better to open the browser they chose.
func appCmd(url string) *exec.Cmd {
	if exe, ok := defaultBrowser(); ok && isChromium(strings.ToLower(filepath.Base(exe))) {
		return exec.Command(exe, "--app="+url)
	}
	for _, p := range candidates() {
		if _, err := os.Stat(p); err == nil {
			return exec.Command(p, "--app="+url)
		}
	}
	return nil
}

// defaultBrowser resolves the http handler the user picked, by following the
// same two registry hops Explorer does: the UserChoice ProgId, then that
// ProgId's open command.
func defaultBrowser() (string, bool) {
	k, err := registry.OpenKey(registry.CURRENT_USER,
		`Software\Microsoft\Windows\Shell\Associations\UrlAssociations\http\UserChoice`,
		registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer k.Close()

	progID, _, err := k.GetStringValue("ProgId")
	if err != nil || progID == "" {
		return "", false
	}

	c, err := registry.OpenKey(registry.CLASSES_ROOT, progID+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", false
	}
	defer c.Close()

	cmd, _, err := c.GetStringValue("")
	if err != nil {
		return "", false
	}
	return exeFromCommand(cmd)
}

// exeFromCommand pulls the program out of a registry open command, which
// looks like `"C:\...\chrome.exe" --single-argument %1` - quoted in practice,
// but the unquoted form is legal and appears in the wild, so both are read.
func exeFromCommand(cmd string) (string, bool) {
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", false
	}
	if cmd[0] == '"' {
		if end := strings.IndexByte(cmd[1:], '"'); end >= 0 {
			return cmd[1 : end+1], true
		}
		return "", false
	}
	if i := strings.IndexByte(cmd, ' '); i >= 0 {
		cmd = cmd[:i]
	}
	return cmd, true
}

// candidates is the fallback scan for when the default browser is Firefox,
// is unset, or could not be read. Per-user installs under LocalAppData are
// included because Chrome installs there without administrator rights.
func candidates() []string {
	var out []string
	for _, base := range []string{
		os.Getenv("ProgramFiles"),
		os.Getenv("ProgramFiles(x86)"),
		os.Getenv("LocalAppData"),
	} {
		if base == "" {
			continue
		}
		out = append(out,
			filepath.Join(base, "Google", "Chrome", "Application", "chrome.exe"),
			filepath.Join(base, "Microsoft", "Edge", "Application", "msedge.exe"),
			filepath.Join(base, "BraveSoftware", "Brave-Browser", "Application", "brave.exe"),
		)
	}
	return out
}
