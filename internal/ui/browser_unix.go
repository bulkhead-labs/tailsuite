//go:build !windows

package ui

import "syscall"

// detached runs the browser's opener in a session of its own.
func detached() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }
