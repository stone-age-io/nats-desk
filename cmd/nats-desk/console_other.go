//go:build !windows

package main

// detachConsole is Windows-only. Elsewhere a program started from a desktop
// launcher is not handed a terminal window that it then has to get rid of.
func detachConsole() bool { return false }
