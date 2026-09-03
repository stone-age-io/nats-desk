//go:build windows

package server

import (
	"errors"
	"syscall"
)

// WSAEADDRINUSE. Winsock does not reuse the POSIX errno values, and stdlib
// syscall does not export a name for this one on Windows, so the number is
// spelled out. Verified against a real double-bind: syscall.EADDRINUSE does
// not match here, errno 10048 does.
const wsaeAddrInUse = syscall.Errno(10048)

func isAddrInUse(err error) bool {
	return errors.Is(err, wsaeAddrInUse) || errors.Is(err, syscall.EADDRINUSE)
}
