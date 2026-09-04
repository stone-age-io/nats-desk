//go:build !windows && !darwin

package browse

import "os/exec"

// appCmd takes the first Chromium browser on PATH.
//
// No default-browser lookup: xdg-settings shells out to a desktop-environment
// specific helper that may not exist, and the answer is frequently a .desktop
// file that then has to be parsed to find the binary. A PATH scan is honest
// about what it can do.
func appCmd(url string) *exec.Cmd {
	for _, name := range []string{
		"google-chrome", "google-chrome-stable", "chromium", "chromium-browser",
		"microsoft-edge", "microsoft-edge-stable", "brave-browser", "vivaldi",
	} {
		p, err := exec.LookPath(name)
		if err != nil {
			continue
		}
		return exec.Command(p, "--app="+url)
	}
	return nil
}
