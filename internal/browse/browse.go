// Package browse opens a URL in the user's default browser.
//
// This is a handful of exec calls, so it is inlined rather than taken as a
// dependency. github.com/pkg/browser does the same thing and is fine, but it
// last shipped in 2024 and this is not enough code to outsource.
package browse

import (
	"fmt"
	"os/exec"
	"runtime"
)

// Open launches url in the default browser and returns without waiting.
func Open(url string) error {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		// rundll32 rather than `cmd /c start`: start treats a leading quoted
		// argument as a window title, and mangles URLs containing &, which
		// ours does - the startup URL carries ?token=.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("open browser: %w", err)
	}
	// Release the child rather than waiting: the browser outlives us, and on
	// some desktops the launcher process does too.
	return cmd.Process.Release()
}
