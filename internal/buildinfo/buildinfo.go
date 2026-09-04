// Package buildinfo carries the version stamped in by the linker.
//
// It is its own package so that anything can read the version without a
// parameter threaded down from main - the settings panel wants it, and it is
// reached through two layers of wiring from there.
package buildinfo

// Version is set with -X at link time. See LDFLAGS in the Makefile.
var Version = "dev"
