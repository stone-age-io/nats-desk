// Package applog picks where this process's log goes.
//
// The Windows binary is linked with -H=windowsgui so that double-clicking it
// does not flash up a console. That subsystem gives the process no standard
// handles at all, so slog writing to os.Stderr writes into a void - and the
// one build where a user has no terminal to read is exactly the build whose
// diagnostics they most need. So: stderr when there is a real stderr, a file
// otherwise.
//
// The file is appended to, not truncated per run, because runs overlap: every
// double-click of an already-running copy starts a second process that lives
// just long enough to find the port taken and exit, and truncating on open
// would wipe the log of the instance actually doing the work. Growth is bound
// by a size check at open instead - crude, but the alternative is a rotation
// policy for a file that holds a few lines a day.
package applog

import (
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/stone-age-io/nats-desk/internal/appdir"
)

const fileName = "nats-desk.log"

// maxLogBytes is where the log is started over. Reached only by a long run at
// -v; at the default level this is years of ordinary use.
const maxLogBytes = 1 << 20

// path is the log file in use, or "" when logging to stderr. Process-global
// because "where is this process's log" is a process-global fact, and the
// settings panel asks for it from a long way away.
var path string

// Path returns the log file, or "" when the log is going to stderr.
func Path() string { return path }

// Setup returns the logger for the process and records the destination.
func Setup(debug bool) *slog.Logger {
	level := slog.LevelInfo
	if debug {
		level = slog.LevelDebug
	}

	w, p := destination()
	path = p
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: level}))
}

func destination() (io.Writer, string) {
	// Stat rather than a build tag: a -H=windowsgui binary has an invalid
	// stderr handle whether it was launched from Explorer or from a prompt,
	// while `go run` in `make dev` builds a console binary from the same
	// source and must keep printing to the terminal.
	if os.Stderr != nil {
		if _, err := os.Stderr.Stat(); err == nil {
			return os.Stderr, ""
		}
	}

	dir, err := appdir.Cache()
	if err != nil {
		return io.Discard, ""
	}
	p := filepath.Join(dir, fileName)
	flags := os.O_CREATE | os.O_WRONLY | os.O_APPEND
	if fi, err := os.Stat(p); err == nil && fi.Size() > maxLogBytes {
		flags = os.O_CREATE | os.O_WRONLY | os.O_TRUNC
	}
	f, err := os.OpenFile(p, flags, 0o600)
	if err != nil {
		return io.Discard, ""
	}
	return f, p
}
