//go:build windows

package main

import (
	"os"
	"syscall"
)

// detachedProcess runs the holder without a console of its own, in a new
// process group, so Ctrl-C in the terminal doesn't reach it.
func detachedProcess() *syscall.SysProcAttr {
	const detached, newGroup = 0x00000008, 0x00000200 // DETACHED_PROCESS, CREATE_NEW_PROCESS_GROUP
	return &syscall.SysProcAttr{CreationFlags: detached | newGroup}
}

// interrupt stops a holder. Windows has no SIGTERM, so it's killed, and
// the lab is left for tailsuite down.
func interrupt(p *os.Process) error { return p.Kill() }
