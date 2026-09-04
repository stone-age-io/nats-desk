package browse

import "os/exec"

// OpenApp opens url in a chromeless application window - Chromium's --app
// mode - falling back to an ordinary tab where no Chromium browser can be
// found.
//
// This is what makes the thing feel like a desktop app without being one. An
// --app window has no address bar, no tab strip and its own taskbar button
// with the site's icon, which is the same window an installed PWA gets. The
// difference that matters is who starts it: this window is opened by the
// binary, so the process is running by definition and the startup token
// handoff happens on the way in. A PWA shortcut only navigates, so it lands on
// a connection error whenever nothing is already listening.
//
// Firefox has no equivalent flag - -ssb was never shipped - so a Firefox user
// gets a normal tab, which works exactly as it always did.
func OpenApp(url string) error {
	cmd := appCmd(url)
	if cmd == nil {
		return Open(url)
	}
	if err := startDetached(cmd); err != nil {
		// A browser we located and then could not start is worth one more
		// try through the OS handler rather than a dead end for the user.
		return Open(url)
	}
	return nil
}

// isChromium reports whether an executable name is a browser that understands
// --app. Matched on the file name because the install path varies by channel,
// by architecture and by whether it was a per-user install.
func isChromium(name string) bool {
	switch name {
	case "chrome.exe", "msedge.exe", "brave.exe", "vivaldi.exe", "opera.exe", "chromium.exe",
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"microsoft-edge", "microsoft-edge-stable", "brave-browser", "vivaldi":
		return true
	}
	return false
}

// startDetached runs cmd without waiting for it. The browser outlives us -
// under autostart it outlives several of us - so nothing may hold onto it.
func startDetached(cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}
	return cmd.Process.Release()
}
