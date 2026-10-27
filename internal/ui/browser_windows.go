//go:build windows

package ui

import "syscall"

// detached runs the browser's opener in a process group of its own.
func detached() *syscall.SysProcAttr {
	const newGroup = 0x00000200 // CREATE_NEW_PROCESS_GROUP
	return &syscall.SysProcAttr{CreationFlags: newGroup}
}
