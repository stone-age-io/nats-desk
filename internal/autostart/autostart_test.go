package autostart

import (
	"os"
	"runtime"
	"strings"
	"testing"
)

// Nothing here writes to the real store. Enabling autostart on the machine
// running the tests would be a side effect no test suite is entitled to, and
// the registry round-trip is verified by driving the real app instead.

// Sync compares the stored entry against a freshly built one and rewrites on
// any difference, so an entry that is not byte-identical between two calls
// would rewrite the registry - or the plist, or the desktop file - on every
// single start, forever, with nothing to show for it.
func TestEntryIsStable(t *testing.T) {
	first, err := entry()
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	second, err := entry()
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	if first != second {
		t.Errorf("entry is not stable across calls:\n%q\n%q", first, second)
	}
}

func TestEntryNamesThisExecutable(t *testing.T) {
	exe, err := os.Executable()
	if err != nil {
		t.Skip("no executable path on this platform")
	}

	got, err := entry()
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	if !strings.Contains(got, exe) {
		t.Errorf("entry does not name the running executable\n got: %q\nwant it to contain: %q", got, exe)
	}
	// Without this the app opens a window at every sign-in, which is the
	// opposite of what someone turning autostart on is asking for.
	if !strings.Contains(got, "-no-browser") {
		t.Errorf("entry does not pass -no-browser: %q", got)
	}
}

// "C:\Program Files\..." is the ordinary case on Windows, and an unquoted
// command line splits it at the space into a program that does not exist.
func TestWindowsEntryIsQuoted(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("windows only")
	}
	got, err := entry()
	if err != nil {
		t.Fatalf("entry: %v", err)
	}
	if !strings.HasPrefix(got, `"`) {
		t.Errorf("entry does not quote the executable path: %q", got)
	}
	if strings.Count(got, `"`) != 2 {
		t.Errorf("entry should carry exactly one quoted path: %q", got)
	}
}
