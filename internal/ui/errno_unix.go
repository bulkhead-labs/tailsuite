//go:build !windows

package ui

import "syscall"

const errAddrNotAvail = syscall.EADDRNOTAVAIL
