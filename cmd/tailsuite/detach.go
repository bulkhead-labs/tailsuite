package main

import (
	"context"
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

func logPath(dir string) string { return filepath.Join(dir, stateDir, "up.log") }

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
	log, err := os.OpenFile(logPath(dir), os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = log.Close() }()
	args := []string{"up", "--dir", abs, "--" + holderFlag}
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
			out, _ := os.ReadFile(logPath(dir))
			_, _ = stderr.Write(out)
			return fail(stderr, fmt.Errorf("the background process stopped before the lab was up (%w); its log is %s", err, logPath(dir)))
		case <-ctx.Done():
			// Interrupted while starting: the holder tears down what it made.
			_ = interrupt(cmd.Process)
			<-exited
			return fail(stderr, fmt.Errorf("interrupted; the background process tore the lab down"))
		case <-tick.C:
			if st, ok, err := readState(dir); err == nil && ok && st.Holder == pid && st.Ready {
				out, _ := os.ReadFile(logPath(dir))
				_, _ = stdout.Write(out)
				return wrote(fmt.Fprintf(stdout, "Held by pid %d; its log is %s.\n", pid, logPath(dir)))
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
