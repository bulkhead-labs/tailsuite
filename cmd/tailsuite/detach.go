package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
)

// holderFlag marks the background process `up --detach` starts (#81).
const holderFlag = "detached-holder"

// holdPoll is how often a holder checks its lab is still there, and how
// often a detaching up checks on its holder.
var holdPoll = 5 * time.Second

// readyPoll is how often a detaching up checks the holder's progress.
const readyPoll = 250 * time.Millisecond

// logPath is a holder's log: one file per holder, by its token, so a new
// holder never shares a file with an old one that hasn't exited yet (on
// Windows, a file another process has open can't even be removed).
func logPath(dir, token string) string {
	return filepath.Join(dir, stateDir, "up-"+token+".log")
}

// removeOldLogs removes earlier holders' logs, best-effort: one still
// open (on Windows) stays until a later up.
func removeOldLogs(dir string) {
	old, _ := filepath.Glob(filepath.Join(dir, stateDir, "up-*.log"))
	for _, f := range old {
		_ = os.Remove(f)
	}
}

// spawnHolder starts tailsuite up again, as a background process in a
// session of its own, and returns once it holds a ready lab, with what it
// printed. If it stops first, its log says why.
func spawnHolder(ctx context.Context, dir, abs, profile, engineName string, stdout, stderr io.Writer) int {
	exe, err := os.Executable()
	if err != nil {
		return fail(stderr, err)
	}
	if err := os.MkdirAll(filepath.Join(dir, stateDir), 0o700); err != nil {
		return fail(stderr, err)
	}
	token, err := sandbox.NewRunID() // 8 random hex digits
	if err != nil {
		return fail(stderr, err)
	}
	removeOldLogs(dir)
	logFile := logPath(dir, token)
	log, err := os.OpenFile(logFile, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600) //nolint:gosec // G304: the project's own .tailsuite/, a name we chose
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = log.Close() }()
	args := []string{"up", "--dir", abs, "--" + holderFlag + "=" + token}
	if profile != "" {
		args = append(args, "--profile", profile)
	}
	if engineName != "" {
		args = append(args, "--engine", engineName)
	}
	cmd := exec.Command(exe, args...) //nolint:gosec,noctx // G204: this binary, re-run; it must outlive ctx
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = detachedProcess()
	if err := cmd.Start(); err != nil {
		return fail(stderr, fmt.Errorf("starting the background process: %w", err))
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	_, _ = fmt.Fprintf(stderr, "Starting the lab in the background (pid %d)...\n", pid)
	tick := time.NewTicker(readyPoll)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			out, _ := os.ReadFile(logFile) //nolint:gosec // G304: our holder's log
			_, _ = stderr.Write(out)
			return fail(stderr, fmt.Errorf("the background process stopped before the lab was up (%w); its log is %s", err, logFile))
		case <-ctx.Done():
			// Interrupted while starting: the holder tears down what it
			// made (on Unix; on Windows it's killed). Check, rather than
			// assume.
			_ = interrupt(cmd.Process)
			<-exited
			if st, ok, err := readState(dir); err == nil && ok && st.Token == token {
				if alive, aerr := runAlive(context.WithoutCancel(ctx), st.Run); aerr != nil || alive {
					return fail(stderr, fmt.Errorf("interrupted; run %s may still be partly up: tailsuite down removes it", st.Run))
				}
			}
			return fail(stderr, errors.New("interrupted; the background process stopped, and no lab of it is left"))
		case <-tick.C:
			if st, ok, err := readState(dir); err == nil && ok && st.Token == token && st.Holder == pid && st.Ready {
				out, _ := os.ReadFile(logFile) //nolint:gosec // G304: our holder's log
				_, _ = stdout.Write(out)
				return wrote(fmt.Fprintf(stdout, "Held by pid %d; its log is %s.\n", pid, logFile))
			}
		}
	}
}

// cleanProject tears down every earlier lab of this project: the runs
// carrying its label, and the one its state names (#81). Other projects'
// labs are never touched. A holder whose lab goes notices and exits.
func cleanProject(ctx context.Context, dir, abs string, stderr io.Writer) error {
	cli, err := docker.Connect(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	runs, err := docker.RunsOf(ctx, cli, projectID(abs))
	if err != nil {
		return err
	}
	st, ok, _ := readState(dir)
	if ok && st.Dir == abs {
		alive, err := runAlive(ctx, st.Run)
		if err != nil {
			return err
		}
		if alive && !slices.Contains(runs, st.Run) {
			runs = append(runs, st.Run)
		}
	}
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandbox.TeardownTimeout)
	defer cancel()
	for _, r := range runs {
		if err := docker.TeardownRun(tctx, cli, r); err != nil {
			return fmt.Errorf("tearing down run %s: %w", r, err)
		}
		_, _ = fmt.Fprintf(stderr, "Tore down run %s, an earlier lab of this project.\n", r)
	}
	if ok && st.Dir == abs {
		return removeState(dir, st.Run)
	}
	return nil
}
