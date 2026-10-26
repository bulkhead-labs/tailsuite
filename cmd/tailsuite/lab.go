package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
	"github.com/bulkhead-labs/tailsuite/internal/version"
)

// stateDir is the project's own gitignored folder (decision 0014).
const stateDir = ".tailsuite"

// labState is what `up` leaves for `test` and `down`: which run is this
// project's lab, and what it was built from, so `test` can refuse a lab
// that no longer matches the project or the build. The lab itself is
// found by the run's labels.
type labState struct {
	Version int    `json:"version"`
	Run     string `json:"run"`
	// Dir is the project's absolute path: a copied project isn't this lab's.
	Dir    string `json:"dir"`
	Engine string `json:"engine"`
	Build  build  `json:"build"`
	// Devices are the plan the lab was started with. A node's user and
	// tags are fixed when it joins, so a changed plan needs a new lab.
	Devices []engine.PlanDevice `json:"devices"`
}

// build is what made a lab: this tailsuite, the images, and the agent.
type build struct {
	Tailsuite string   `json:"tailsuite"`
	Images    []string `json:"images"`
	Agent     string   `json:"agent"` // SHA-256 of the node agent's binary
}

func buildOf(agent []byte) build {
	sum := sha256.Sum256(agent)
	return build{Tailsuite: version.String(), Images: []string{headscale.HeadscaleImage, headscale.TailscaleImage}, Agent: hex.EncodeToString(sum[:])}
}

// samePlan compares two plans device by device: name, user, tags, IPv4.
func samePlan(a, b []engine.PlanDevice) bool {
	return slices.EqualFunc(a, b, func(x, y engine.PlanDevice) bool {
		return x.Name == y.Name && x.User == y.User && x.IPv4 == y.IPv4 && slices.Equal(x.Tags, y.Tags)
	})
}

const labStateVersion = 1

func statePath(dir string) string { return filepath.Join(dir, stateDir, "lab.json") }

// readState returns the project's lab state, if there is one.
func readState(dir string) (labState, bool, error) {
	b, err := os.ReadFile(statePath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return labState{}, false, nil
	}
	if err != nil {
		return labState{}, false, err
	}
	var st labState
	if err := json.Unmarshal(b, &st); err != nil || st.Version != labStateVersion {
		return labState{}, false, fmt.Errorf("%s isn't a lab state this tailsuite reads; tailsuite down --run <id> tears a lab down by its run ID", statePath(dir))
	}
	if err := sandbox.CheckRunID(st.Run); err != nil {
		return labState{}, false, fmt.Errorf("%s: %w", statePath(dir), err)
	}
	return st, true, nil
}

// writeState writes the state, readable only by its owner, through a
// rename so a reader never sees half of it.
func writeState(dir string, st labState) error {
	if err := os.MkdirAll(filepath.Join(dir, stateDir), 0o700); err != nil {
		return err
	}
	st.Version = labStateVersion
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	tmp := statePath(dir) + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, statePath(dir))
}

// removeState removes the project's lab state if it still names run.
// Another up may have written its own since, as after a down from
// another terminal: that one isn't ours to remove.
func removeState(dir, run string) error {
	st, ok, err := readState(dir)
	if err != nil || !ok || st.Run != run {
		return nil //nolint:nilerr // an unreadable or foreign state isn't this run's
	}
	err = os.Remove(statePath(dir))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// runAlive says whether the Docker Engine still has anything of a run.
func runAlive(ctx context.Context, runID string) (bool, error) {
	cli, err := docker.Connect(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = cli.Close() }()
	runs, err := docker.Runs(ctx, cli)
	return slices.Contains(runs, runID), err
}

// checkEngine refuses an engine that isn't built.
func checkEngine(name string) error {
	switch name {
	case headscale.Name:
		return nil
	case "tailscale":
		return errors.New("the Tailscale engine isn't built yet (#6); use --engine headscale, or a profile that does")
	}
	return fmt.Errorf("no engine %q: there's headscale, and tailscale from #6", name)
}

// planNames are the devices the lab rebuilds, in the plan's order.
func planNames(devices []engine.PlanDevice) []string {
	names := make([]string, len(devices))
	for i, d := range devices {
		names[i] = d.Name
	}
	return names
}

// newSandbox connects to Docker for a new run, with the node agent for the
// Docker host's architecture (decision 0027). Without the agent nothing
// is started.
func newSandbox(ctx context.Context) (*docker.Sandbox, []byte, error) {
	if len(agentbin.Embedded()) == 0 {
		return nil, nil, agentbin.ErrNotEmbedded
	}
	sb, err := docker.New(ctx)
	if err != nil {
		return nil, nil, err
	}
	agent, err := agentbin.For(sb.Arch())
	if err != nil {
		_ = sb.Close()
		return nil, nil, err
	}
	return sb, agent, nil
}

// load reads the project and resolves the options, profile first.
func load(dir string, flags suite.Options) (*project.Project, suite.Options, error) {
	p, err := project.Load(dir)
	if err != nil {
		return nil, suite.Options{}, err
	}
	o, err := suite.Resolve(p.Lab, flags)
	if err != nil {
		return nil, suite.Options{}, err
	}
	if err := checkEngine(o.Engine); err != nil {
		return nil, suite.Options{}, err
	}
	return p, o, nil
}

func cmdUp(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flags("up", stderr)
	dir := fs.String("dir", ".", "the project")
	prof := fs.String("profile", "", "the profile in lab.yaml whose engine runs the lab (default: default)")
	eng := fs.String("engine", "", "the engine, over the profile's")
	if ok, code := parse(fs, args, 0); !ok {
		return code
	}
	p, o, err := load(*dir, suite.Options{Profile: *prof, Engine: *eng})
	if err != nil {
		return fail(stderr, err)
	}
	// Refused before Docker is touched (0027).
	if len(agentbin.Embedded()) == 0 {
		return fail(stderr, agentbin.ErrNotEmbedded)
	}
	abs, err := filepath.Abs(*dir)
	if err != nil {
		return fail(stderr, err)
	}
	if st, ok, err := readState(*dir); err != nil {
		return fail(stderr, err)
	} else if ok {
		alive, err := runAlive(ctx, st.Run)
		if err != nil {
			return fail(stderr, err)
		}
		switch {
		case alive && st.Dir != abs:
			return fail(stderr, fmt.Errorf("%s holds the lab state of %s (run %s): if this folder is a copy, delete %s", stateDir, st.Dir, st.Run, statePath(*dir)))
		case alive:
			return fail(stderr, fmt.Errorf("this project's lab is already up (run %s): Ctrl-C in the terminal running tailsuite up tears it down, or tailsuite down", st.Run))
		}
	}
	e := headscale.New(headscale.Config{})
	low, err := e.Lower(p.Tailnet)
	if err != nil {
		return fail(stderr, err)
	}
	if err := low.Err(); err != nil {
		return fail(stderr, err)
	}
	sb, agent, err := newSandbox(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = sb.Close() }()
	plan := engine.PlanEveryDevice(p.Tailnet)
	// Written first, so a crash while starting still leaves the run ID
	// for down.
	if err := writeState(*dir, labState{Run: sb.RunID(), Dir: abs, Engine: o.Engine, Build: buildOf(agent), Devices: plan.Devices}); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stderr, "Starting the lab on %s (run %s)...\n", e.Version(), sb.RunID())
	lab, err := headscale.New(headscale.Config{Sandbox: sb, Agent: agent}).Start(ctx, plan, low)
	if err != nil {
		// Start tears down what it made. If that failed too, the state
		// keeps the run ID for down.
		if alive, aerr := runAlive(context.WithoutCancel(ctx), sb.RunID()); aerr == nil && !alive {
			_ = removeState(*dir, sb.RunID())
		} else {
			_, _ = fmt.Fprintf(stderr, "tailsuite: run %s is still partly up: tailsuite down removes it\n", sb.RunID())
		}
		return fail(stderr, fmt.Errorf("the lab didn't start: %w", err))
	}
	var b strings.Builder
	fmt.Fprintf(&b, "The lab is up: run %s, %s, %d nodes.\n", sb.RunID(), e.Version(), len(lab.Nodes()))
	for _, n := range lab.Nodes() {
		fmt.Fprintf(&b, "  %-16s %s\n", n.Name, n.IPv4)
	}
	for _, c := range low.Changes {
		fmt.Fprintf(&b, "Changed for %s: %s (%s)\n", e.Name(), c.Pointer, c.Reason)
	}
	b.WriteString("\nThe lab stays up until Ctrl-C, which tears it down. tailsuite test, in another terminal, runs the suite on it. The local UI comes with #11.\n")
	if code := wrote(io.WriteString(stdout, b.String())); code != exitOK {
		return teardownUp(ctx, *dir, sb, stderr, code)
	}
	// The maintainer's choice (2026-10-26): up holds the lab in the
	// foreground, and Ctrl-C (or SIGTERM) tears it down.
	// A down from another terminal ends the lab too; then there's
	// nothing left to hold.
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			_, _ = fmt.Fprintf(stderr, "Tearing down run %s...\n", sb.RunID())
			return teardownUp(ctx, *dir, sb, stderr, exitOK)
		case <-tick.C:
			if alive, err := runAlive(ctx, sb.RunID()); err == nil && !alive {
				_, _ = fmt.Fprintf(stderr, "Run %s was torn down from elsewhere.\n", sb.RunID())
				return teardownUp(ctx, *dir, sb, stderr, exitOK)
			}
		}
	}
}

// teardownUp removes up's lab and its state, with a context of its own
// so a cancelled one doesn't stop it (0019). A failed teardown keeps the
// state, for down.
func teardownUp(ctx context.Context, dir string, sb *docker.Sandbox, stderr io.Writer, code int) int {
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandbox.TeardownTimeout)
	defer cancel()
	if err := sb.Teardown(tctx); err != nil {
		return fail(stderr, fmt.Errorf("tearing down run %s: %w; tailsuite down removes what's left", sb.RunID(), err))
	}
	if err := removeState(dir, sb.RunID()); err != nil {
		return fail(stderr, err)
	}
	return code
}

func cmdDown(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flags("down", stderr)
	dir := fs.String("dir", ".", "the project")
	runID := fs.String("run", "", "tear down this run instead, by its ID, as after a crash")
	if ok, code := parse(fs, args, 0); !ok {
		return code
	}
	st, ok, err := readState(*dir)
	if err != nil && *runID == "" {
		return fail(stderr, err)
	}
	target := *runID
	if target == "" {
		if !ok {
			return downNothing(ctx, stdout, stderr)
		}
		if abs, err := filepath.Abs(*dir); err != nil || abs != st.Dir {
			return fail(stderr, fmt.Errorf("%s holds the lab state of %s (run %s): if this folder is a copy, delete %s; to tear that lab down anyway, tailsuite down --run %s", stateDir, st.Dir, st.Run, statePath(*dir), st.Run))
		}
		target = st.Run
	}
	cli, err := docker.Connect(ctx)
	if err != nil {
		return fail(stderr, err)
	}
	defer func() { _ = cli.Close() }()
	// Teardown finishes even when ctx is cancelled (decision 0019).
	tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandbox.TeardownTimeout)
	defer cancel()
	if err := docker.TeardownRun(tctx, cli, target); err != nil {
		return fail(stderr, err)
	}
	if ok && st.Run == target {
		if err := removeState(*dir, target); err != nil {
			return fail(stderr, err)
		}
	}
	return wrote(fmt.Fprintf(stdout, "Run %s is torn down.\n", target))
}

// downNothing says there's no lab for this project, and lists any other
// runs left on the Docker Engine, so a crashed one can be found.
func downNothing(ctx context.Context, stdout, stderr io.Writer) int {
	msg := "No lab is up for this project.\n"
	if cli, err := docker.Connect(ctx); err == nil {
		runs, err := docker.Runs(ctx, cli)
		_ = cli.Close()
		if err == nil && len(runs) > 0 {
			msg += "Other tailsuite runs on this Docker Engine: " + strings.Join(runs, ", ") + ". tailsuite down --run <id> tears one down.\n"
		}
	} else {
		_, _ = fmt.Fprintf(stderr, "tailsuite: couldn't list other runs: %v\n", err)
	}
	return wrote(io.WriteString(stdout, msg))
}
