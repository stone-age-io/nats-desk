// Package autostart turns "start nats-desk when I sign in" on and off.
//
// It exists because a PWA cannot start a backend. An installed app's shortcut
// only navigates to http://127.0.0.1:4111, so if nothing is listening the
// window opens on a connection error, and there is nothing in that window a
// user can press to fix it. Something has to own the process lifecycle, and
// sign-in is the only moment that reliably comes before the click.
//
// Each platform records the same fact somewhere different, but always as a
// string we can regenerate and compare - a registry value, a plist, a desktop
// entry. That is what lets Sync notice the binary has moved.
//
// Deliberately not a service. A service runs as another account, and every
// credential this app opens NATS with - .creds files, nats CLI contexts -
// lives in the user's own profile. It would also mean an installer, admin
// rights, and one shared port for every user signed in to the machine.
package autostart

import "errors"

// ErrUnsupported is returned by Enable and Disable where there is no
// implementation. Enabled reports false with no error there instead, so the
// UI can ask "is this on?" without knowing which platform it is on.
var ErrUnsupported = errors.New("autostart is not supported on this platform")

// Supported reports whether this platform can start the app at sign-in.
func Supported() bool { return supported }

// Enabled reports whether an autostart entry exists.
func Enabled() (bool, error) {
	if !supported {
		return false, nil
	}
	cur, err := read()
	if err != nil {
		return false, err
	}
	return cur != "", nil
}

// Enable records the current executable to be started at sign-in.
func Enable() error {
	if !supported {
		return ErrUnsupported
	}
	want, err := entry()
	if err != nil {
		return err
	}
	return write(want)
}

// Disable removes the entry. Removing something already absent is a success.
func Disable() error {
	if !supported {
		return ErrUnsupported
	}
	return remove()
}

// Sync rewrites an entry that points at a different executable than the one
// running - the binary was moved, renamed, or replaced by a new download.
// Without it autostart would keep naming a path that no longer exists and
// simply stop happening, with nothing anywhere saying why. It does nothing
// when autostart is off, so it is safe to call on every start.
func Sync() error {
	if !supported {
		return nil
	}
	cur, err := read()
	if err != nil || cur == "" {
		return err
	}
	want, err := entry()
	if err != nil {
		return err
	}
	if cur == want {
		return nil
	}
	return write(want)
}
