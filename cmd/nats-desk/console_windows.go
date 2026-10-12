package main

import (
	"os"
	"unsafe"

	"golang.org/x/sys/windows"
)

// Neither is wrapped by x/sys, and both are plain kernel32 exports.
var (
	kernel32                  = windows.NewLazySystemDLL("kernel32.dll")
	procFreeConsole           = kernel32.NewProc("FreeConsole")
	procGetConsoleProcessList = kernel32.NewProc("GetConsoleProcessList")
)

// detachConsole closes the console window that a console-subsystem build is
// given when it is started from Explorer, the Run key or the natsdesk://
// handler, and reports whether it did.
//
// The release build is linked -H=windowsgui and never has a console, so this
// finds nothing to do there. It exists for every other way the binary gets
// built: a plain `go build`, or a `go install` run without
// -ldflags=-H=windowsgui. Both produce a console binary, and Windows opens a console
// window for it that stays for the life of the process - and closing that
// window kills the app. Detaching cannot stop the window appearing, only
// close it again at once, so such a build still flashes one up;
// -H=windowsgui remains the only real fix.
//
// A console shared with any other process is left alone. That is a terminal
// someone ran us from - `make dev`, or `nats-desk -version` at a prompt - and
// our output belongs in it.
func detachConsole() bool {
	var pids [2]uint32
	n, _, _ := procGetConsoleProcessList.Call(uintptr(unsafe.Pointer(&pids[0])), uintptr(len(pids)))
	if n != 1 {
		return false
	}

	// Note which standard handles are the console before leaving it. One a
	// parent redirected to a pipe or a file is still good afterwards, and
	// must keep working.
	var onConsole []**os.File
	for _, f := range []**os.File{&os.Stdin, &os.Stdout, &os.Stderr} {
		var mode uint32
		if *f != nil && windows.GetConsoleMode(windows.Handle((*f).Fd()), &mode) == nil {
			onConsole = append(onConsole, f)
		}
	}

	if ok, _, _ := procFreeConsole.Call(); ok == 0 {
		return false
	}

	// Those handles now name a console we are no longer attached to.
	// Clearing them is what sends applog to its file, exactly as it does
	// for a -H=windowsgui build.
	for _, f := range onConsole {
		*f = nil
	}
	return true
}
