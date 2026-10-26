package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

// stateDir is the project's own gitignored folder (decision 0014).
const stateDir = ".tailsuite"

// labState is what `up` leaves for `test` and `down`: which run is this
// project's lab. The lab itself is found by the run's labels.
type labState struct {
	Version int      `json:"version"`
	Run     string   `json:"run"`
	Engine  string   `json:"engine"`
	Devices []string `json:"devices"`
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

func removeState(dir string) error {
	err := os.Remove(statePath(dir))
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
func planNames(plan engine.LabPlan) []string {
	names := make([]string, len(plan.Devices))
	for i, d := range plan.Devices {
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
	if st, ok, err := readState(*dir); err != nil {
		return fail(stderr, err)
	} else if ok {
		alive, err := runAlive(ctx, st.Run)
		if err != nil {
			return fail(stderr, err)
		}
		if alive {
			return fail(stderr, fmt.Errorf("this project's lab is already up (run %s); tailsuite down tears it down", st.Run))
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
	if err := writeState(*dir, labState{Run: sb.RunID(), Engine: o.Engine, Devices: planNames(plan)}); err != nil {
		return fail(stderr, err)
	}
	_, _ = fmt.Fprintf(stderr, "Starting the lab on %s (run %s)...\n", e.Version(), sb.RunID())
	lab, err := headscale.New(headscale.Config{Sandbox: sb, Agent: agent}).Start(ctx, plan, low)
	if err != nil {
		// Start has torn down what it made.
		_ = removeState(*dir)
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
	b.WriteString("\ntailsuite test runs the suite on it; tailsuite down tears it down. The local UI comes with #11.\n")
	return wrote(io.WriteString(stdout, b.String()))
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
		if err := removeState(*dir); err != nil {
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
