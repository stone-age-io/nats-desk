package scheme

import (
	"os"

	"golang.org/x/sys/windows/registry"
)

// HKCU\Software\Classes rather than HKCR: the same keys, but the per-user
// half of the merged view, so no administrator rights are needed and one
// user's registration cannot affect another's.
const classKey = `Software\Classes\` + Name

func register() error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// %1 is the whole URL the browser navigated to, quoted because a path
	// with a space in it is the normal case on Windows.
	command := `"` + exe + `" "%1"`

	if current, err := readCommand(); err == nil && current == command {
		return nil
	}

	k, _, err := registry.CreateKey(registry.CURRENT_USER, classKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()

	if err := k.SetStringValue("", "URL:nats-desk"); err != nil {
		return err
	}
	// An empty value under this exact name is what marks a class as a URL
	// protocol. Its content is never read; its presence is the whole signal.
	if err := k.SetStringValue("URL Protocol", ""); err != nil {
		return err
	}

	c, _, err := registry.CreateKey(registry.CURRENT_USER, classKey+`\shell\open\command`, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer c.Close()
	return c.SetStringValue("", command)
}

func readCommand() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, classKey+`\shell\open\command`, registry.QUERY_VALUE)
	if err != nil {
		return "", err
	}
	defer k.Close()

	v, _, err := k.GetStringValue("")
	return v, err
}
