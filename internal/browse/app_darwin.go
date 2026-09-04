package browse

import (
	"os"
	"os/exec"
	"path/filepath"
)

// appCmd opens an --app window through `open -na`, which is how a bundled
// macOS application takes command line flags at all: -n for a new instance,
// -a to name the bundle, and everything after --args going to the binary
// inside it.
//
// No default-browser lookup here. Reading LSHandlers out of the launch
// services plist is a private format that has changed between releases, and
// the payoff on macOS is small - a Mac with Chrome installed is a Mac whose
// owner chose to install Chrome.
func appCmd(url string) *exec.Cmd {
	for _, app := range []string{"Google Chrome", "Microsoft Edge", "Brave Browser", "Vivaldi", "Chromium"} {
		if _, err := os.Stat(filepath.Join("/Applications", app+".app")); err != nil {
			continue
		}
		return exec.Command("open", "-na", app, "--args", "--app="+url)
	}
	return nil
}
