package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// DefaultSocket is where the agent listens for sessions inside its node.
const DefaultSocket = "/run/tailsuite/agent.sock"

// stopGrace is how long the node's command gets to exit after SIGTERM.
const stopGrace = 10 * time.Second

// ExitError is the exit status of the node's command: its exit code, or
// 128 plus the signal that killed it, as shells report it.
type ExitError struct{ Code int }

func (e *ExitError) Error() string { return fmt.Sprintf("the node's command exited %d", e.Code) }

// Run is the agent's life in a node. It serves sessions on a Unix socket,
// and runs the node's own command (such as tailscaled), if there is one,
// until it exits. It returns when the command exits, with an *ExitError if
// it failed. When ctx is cancelled it stops the command with SIGTERM, and
// returns how the command ended, so a node stopped mid-run doesn't read as
// a clean exit. The listeners close as it returns. It refuses to start
// where another agent already serves.
func Run(ctx context.Context, a *Agent, socket string, command []string, stderr io.Writer) error {
	defer a.Close()
	ln, err := listenUnix(socket)
	if err != nil {
		return err
	}
	var sessions sync.WaitGroup
	defer func() {
		_ = ln.Close()
		_ = os.Remove(socket)
		sessions.Wait()
	}()
	sessionCtx, endSessions := context.WithCancel(ctx)
	defer endSessions()
	// The accept loop is counted too, so no session can start after Wait.
	sessions.Go(func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return // closed
			}
			sessions.Go(func() {
				defer func() { _ = conn.Close() }()
				stop := context.AfterFunc(sessionCtx, func() { _ = conn.Close() })
				defer stop()
				_ = a.Serve(sessionCtx, conn, conn)
			})
		}
	})

	if len(command) == 0 {
		<-ctx.Done()
		return nil
	}
	return supervise(ctx, command, stderr)
}

// listenUnix listens on socket, in a directory only its owner can enter.
// It replaces a stale socket, but refuses one a live agent still serves:
// taking it would orphan that agent's listeners.
func listenUnix(socket string) (net.Listener, error) {
	dir := filepath.Dir(socket)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("agent socket: %w", err)
	}
	if err := os.Chmod(dir, 0o700); err != nil { //nolint:gosec // G302: a directory, which needs its execute bit
		return nil, fmt.Errorf("agent socket: %w", err)
	}
	var d net.Dialer
	dctx, cancel := context.WithTimeout(context.Background(), time.Second)
	conn, err := d.DialContext(dctx, "unix", socket)
	cancel()
	switch {
	case err == nil:
		_ = conn.Close()
		return nil, fmt.Errorf("agent socket %s: another agent is serving on it", socket)
	case errors.Is(err, os.ErrNotExist):
	case errors.Is(err, syscall.ECONNREFUSED):
		if err := os.Remove(socket); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("agent socket: %w", err)
		}
	default:
		return nil, fmt.Errorf("agent socket %s: %w", socket, err)
	}
	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "unix", socket)
	if err != nil {
		return nil, fmt.Errorf("agent socket: %w", err)
	}
	if err := os.Chmod(socket, 0o600); err != nil {
		_ = ln.Close()
		return nil, fmt.Errorf("agent socket: %w", err)
	}
	return ln, nil
}

// supervise runs the node's command with the agent's stdout and stderr,
// passes SIGTERM on when ctx ends, and reports how it exited.
func supervise(ctx context.Context, command []string, stderr io.Writer) error {
	cmd := exec.Command(command[0], command[1:]...) //nolint:gosec,noctx // G204: the node's own command, from its spec; it's stopped with SIGTERM below, not killed by a context
	cmd.Stdout, cmd.Stderr = stderr, stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting the node's command: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()

	select {
	case err := <-done:
		return exitError(err)
	case <-ctx.Done():
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	select {
	case err := <-done:
		return exitError(err)
	case <-time.After(stopGrace):
		_ = cmd.Process.Kill()
		return exitError(<-done)
	}
}

func exitError(err error) error {
	if err == nil {
		return nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) {
		if ws, ok := exit.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return &ExitError{Code: 128 + int(ws.Signal())}
		}
		return &ExitError{Code: exit.ExitCode()}
	}
	return fmt.Errorf("the node's command: %w", err)
}

// Connect bridges one session: stdin to the agent's socket, and the socket
// to stdout. It waits up to wait for the agent to start listening. It
// returns when the agent closes the session; closing stdin ends it.
func Connect(ctx context.Context, socket string, wait time.Duration, stdin io.Reader, stdout io.Writer) error {
	conn, err := dialUnix(ctx, socket, wait)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	go func() {
		_, _ = io.Copy(conn, stdin)
		_ = conn.CloseWrite()
	}()
	if _, err := io.Copy(stdout, conn); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("agent session: %w", err)
	}
	return nil
}

// dialUnix dials the agent's socket, retrying while it doesn't exist yet.
// The bound is for an agent that never starts.
func dialUnix(ctx context.Context, socket string, wait time.Duration) (*net.UnixConn, error) {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	var d net.Dialer
	for {
		conn, err := d.DialContext(ctx, "unix", socket)
		if err == nil {
			return conn.(*net.UnixConn), nil
		}
		if !errors.Is(err, os.ErrNotExist) && !errors.Is(err, syscall.ECONNREFUSED) {
			return nil, fmt.Errorf("agent socket: %w", err)
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("agent socket %s: not there after %s: %w", socket, wait, err)
		case <-time.After(50 * time.Millisecond):
		}
	}
}
