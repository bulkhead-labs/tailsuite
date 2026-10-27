//go:build !windows

package main

import (
	"os"
	"syscall"
)

// detachedProcess runs the holder in a session of its own, so closing the
// terminal (SIGHUP) or Ctrl-C there doesn't reach it.
func detachedProcess() *syscall.SysProcAttr { return &syscall.SysProcAttr{Setsid: true} }

// interrupt asks a holder to tear down and stop.
func interrupt(p *os.Process) error { return p.Signal(syscall.SIGTERM) }
