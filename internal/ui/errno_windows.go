//go:build windows

package ui

import "syscall"

// WSAEADDRNOTAVAIL.
const errAddrNotAvail = syscall.Errno(10049)
