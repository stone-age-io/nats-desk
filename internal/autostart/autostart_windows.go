package autostart

import (
	"errors"
	"os"

	"golang.org/x/sys/windows/registry"
)

const supported = true

// The Run key rather than a shortcut in the Startup folder. A .lnk has to be
// built through COM, which means cgo or a hand-rolled Shell Link binary
// format; this is a string. Both need no administrator rights and both run as
// the signed-in user, which is the property that matters.
const (
	runKey    = `Software\Microsoft\Windows\CurrentVersion\Run`
	valueName = "nats-desk"
)

// entry is the command line to store. -no-browser because at sign-in the user
// asked for the port to be listening later, not for a window now.
func entry() (string, error) {
	exe, err := os.Executable()
	if err != nil {
		return "", err
	}
	return `"` + exe + `" -no-browser`, nil
}

func read() (string, error) {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.QUERY_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return "", nil
		}
		return "", err
	}
	defer k.Close()

	v, _, err := k.GetStringValue(valueName)
	if errors.Is(err, registry.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return v, nil
}

func write(v string) error {
	k, _, err := registry.CreateKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	return k.SetStringValue(valueName, v)
}

func remove() error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		if errors.Is(err, registry.ErrNotExist) {
			return nil
		}
		return err
	}
	defer k.Close()

	if err := k.DeleteValue(valueName); err != nil && !errors.Is(err, registry.ErrNotExist) {
		return err
	}
	return nil
}
