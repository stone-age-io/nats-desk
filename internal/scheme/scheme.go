// Package scheme registers nats-desk as the handler for natsdesk:// URLs.
//
// It exists for one dead end: an installed PWA whose window is open while the
// backend is not running. A page cannot start a process, but it can navigate,
// and a URL scheme is what turns a navigation into a process launch. The
// service worker's offline page is the only thing that uses this, and the
// registration is per-user - HKCU on Windows, the user's own applications
// directory elsewhere - so it needs no administrator rights.
package scheme

import "strings"

// Name is the URL scheme. Changing it orphans any offline page a browser has
// already cached, so it does not change.
const Name = "natsdesk"

// Verbs the offline page uses.
const (
	// ActionStart just gets the port listening. The window that navigated
	// here is already open and is polling for us, so it must not be given a
	// second one.
	ActionStart = "start"

	// ActionOpen additionally opens a fresh app window carrying the startup
	// token. This is the recovery path for the case ActionStart cannot fix:
	// a browser that has lost its session cookie, where reloading in place
	// would only reach the app shell and then fail every API call.
	ActionOpen = "open"
)

// Action returns the verb from a natsdesk:// argument. ok is false for
// anything that is not one of our URLs, which is every ordinary invocation.
//
// A bare "natsdesk://" reports ActionStart: a scheme with no verb still means
// "make it run", and that is the safer of the two to guess at.
func Action(arg string) (string, bool) {
	prefix := Name + "://"
	if len(arg) < len(prefix) || !strings.EqualFold(arg[:len(prefix)], prefix) {
		return "", false
	}

	rest := arg[len(prefix):]
	if i := strings.IndexAny(rest, "?#"); i >= 0 {
		rest = rest[:i]
	}
	// Browsers normalise natsdesk://open into natsdesk://open/ on the way
	// out, so the trailing slash is the common form rather than the odd one.
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return ActionStart, true
	}
	return strings.ToLower(rest), true
}

// Register makes this executable the handler for the scheme. It is called on
// every normal start: the registration names an absolute path, so a moved or
// re-downloaded binary has to be able to take it over, and rewriting a value
// that is already correct is cheaper than explaining to a user why the button
// on the offline page stopped working.
func Register() error { return register() }
