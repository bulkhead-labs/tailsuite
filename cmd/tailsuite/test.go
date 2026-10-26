package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"

	"github.com/bulkhead-labs/tailsuite/internal/agentbin"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/runner"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

func cmdTest(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flags("test", stderr)
	dir := fs.String("dir", ".", "the project")
	var f suite.Options
	var only, skip labels
	fs.StringVar(&f.Profile, "profile", "", "the profile in lab.yaml (default: default)")
	fs.StringVar(&f.Engine, "engine", "", "the engine, over the profile's")
	fs.Var(&only, "only", "run only tests with this label (repeatable)")
	fs.Var(&skip, "skip", "skip tests with this label (repeatable; wins over --only)")
	fs.StringVar(&f.Run, "run", "", "run only tests whose name matches this regular expression")
	fs.BoolVar(&f.Strict, "strict", false, "fail on UNKNOWN and REFUSED results too")
	asJSON := fs.Bool("json", false, "print the results as JSON")
	junit := fs.String("junit", "", "also write JUnit XML to this file")
	if ok, code := parse(fs, args, 0); !ok {
		return code
	}
	f.Only, f.Skip = only, skip

	p, o, err := load(*dir, f)
	if err != nil {
		return fail(stderr, err)
	}
	e := headscale.New(headscale.Config{})
	low, err := e.Lower(p.Tailnet)
	if err != nil {
		return fail(stderr, err)
	}
	if err := low.Err(); err != nil {
		return fail(stderr, err)
	}
	planned, warnings, err := plan(p, e.Capabilities(), low, o)
	if err != nil {
		return fail(stderr, err)
	}

	r, err := runOnLab(ctx, *dir, p, o, low, planned, stderr)
	if err != nil {
		return fail(stderr, err)
	}
	r.Warnings = append(r.Warnings, warnings...)
	return report(r, *dir, *asJSON, *junit, stdout, stderr)
}

// plan reads the suite and chooses what runs (0015). Every problem with
// the files or the selection is an error, before any lab is touched.
func plan(p *project.Project, caps engine.Capabilities, low engine.Lowering, o suite.Options) ([]suite.Planned, []string, error) {
	tests, err := suite.ReadDir(p.Dir)
	if err != nil {
		return nil, nil, err
	}
	tests = append(tests, suite.PolicyTests(p.Tailnet.Policy)...)
	planned, err := suite.Build(p, tests, caps, low)
	if err != nil {
		return nil, nil, err
	}
	warnings, err := suite.Select(planned, o)
	if err != nil {
		return nil, nil, err
	}
	return planned, append(warnings, suite.Warnings(planned, p.Lab, o.Engine)...), nil
}

// runOnLab runs on the project's lab if it's up, with the current policy
// applied first; otherwise on a lab of its own, torn down after.
func runOnLab(ctx context.Context, dir string, p *project.Project, o suite.Options, low engine.Lowering, planned []suite.Planned, stderr io.Writer) (*runner.Run, error) {
	cfg := runner.Config{Engine: headscale.Name, Version: headscale.New(headscale.Config{}).Version(), Project: p, Options: o}
	plan := engine.PlanEveryDevice(p.Tailnet)

	st, up, err := readState(dir)
	if err != nil {
		return nil, err
	}
	if up {
		if up, err = runAlive(ctx, st.Run); err != nil {
			return nil, err
		}
		if !up {
			_, _ = fmt.Fprintf(stderr, "The lab that was up (run %s) is gone; starting one for this run.\n", st.Run)
			if err := removeState(dir, st.Run); err != nil {
				return nil, err
			}
		}
	}
	var r *runner.Run
	if up {
		r, err = onUpsLab(ctx, dir, st, plan, low, planned, cfg)
	} else {
		r, err = onOwnLab(ctx, plan, low, planned, cfg, stderr)
	}
	if err == nil && ctx.Err() != nil {
		// Interrupted: probes cut short read as failures to connect.
		return nil, fmt.Errorf("interrupted: %w", ctx.Err())
	}
	return r, err
}

// onUpsLab runs on the lab `up` started, after checking it's still what
// this project and this build would start, and applying the current
// policy. A result is only true of the lab it was read on.
func onUpsLab(ctx context.Context, dir string, st labState, plan engine.LabPlan, low engine.Lowering, planned []suite.Planned, cfg runner.Config) (*runner.Run, error) {
	again := "; tailsuite down, then tailsuite up, starts a lab that matches"
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	switch {
	case st.Dir != abs:
		return nil, fmt.Errorf("%s holds the lab state of %s (run %s): if this folder is a copy, delete %s; if the project moved, tailsuite down --run %s and up again", stateDir, st.Dir, st.Run, statePath(dir), st.Run)
	case st.Engine != cfg.Options.Engine:
		return nil, fmt.Errorf("the lab that's up (run %s) runs %s, and this run asks for %s%s, or choose --engine %s", st.Run, st.Engine, cfg.Options.Engine, again, st.Engine)
	case !samePlan(plan.Devices, st.Devices):
		return nil, fmt.Errorf("lab.yaml or tailnet.auto.tfvars changed a device since the lab came up (run %s): a node keeps the user and tags it joined with%s", st.Run, again)
	}
	sb, err := docker.Attach(ctx, st.Run)
	if err != nil {
		return nil, fmt.Errorf("%w%s", err, again)
	}
	defer func() { _ = sb.Close() }()
	agent, err := agentbin.For(sb.Arch())
	if err != nil {
		return nil, err
	}
	if b := buildOf(agent); !reflect.DeepEqual(b, st.Build) {
		return nil, fmt.Errorf("the lab that's up (run %s) was started by another tailsuite build (%s, agent %.12s), and this is %s, agent %.12s%s", st.Run, st.Build.Tailsuite, st.Build.Agent, b.Tailsuite, b.Agent, again)
	}
	lab, err := headscale.New(headscale.Config{Sandbox: sb}).Attach(ctx, sb.Nodes(), planNames(st.Devices))
	if err != nil {
		return nil, fmt.Errorf("%w%s", err, again)
	}
	// The policy may have changed since up: apply it, and wait for every
	// node to hold it, before any probe (0016).
	if _, err := lab.Apply(ctx, low.Policy); err != nil {
		return nil, fmt.Errorf("applying the policy to the lab: %w", err)
	}
	cfg.Sandbox, cfg.Lab = sb, lab
	r, err := runner.Execute(ctx, cfg, planned)
	if err != nil {
		return nil, err
	}
	// Another process may have applied its own policy while this one
	// probed: then no result here was read under this run's policy.
	if err := lab.StillHolds(ctx); err != nil {
		r.Unprove("not read under this run's policy: " + err.Error())
	}
	return r, nil
}

// onOwnLab starts a lab for this run alone, and always tears it down. A
// teardown that fails after the run doesn't change what was proven; it's
// reported, with what removes the rest.
func onOwnLab(ctx context.Context, plan engine.LabPlan, low engine.Lowering, planned []suite.Planned, cfg runner.Config, stderr io.Writer) (*runner.Run, error) {
	sb, agent, err := newSandbox(ctx)
	if err != nil {
		return nil, err
	}
	defer func() { _ = sb.Close() }()
	_, _ = fmt.Fprintf(stderr, "No lab is up; starting one for this run (run %s). tailsuite up keeps one running.\n", sb.RunID())
	var r *runner.Run
	err = sandbox.Run(ctx, sb, func(ctx context.Context) error {
		lab, err := headscale.New(headscale.Config{Sandbox: sb, Agent: agent}).Start(ctx, plan, low)
		if err != nil {
			return fmt.Errorf("the lab didn't start: %w", err)
		}
		cfg.Sandbox, cfg.Lab = sb, lab
		r, err = runner.Execute(ctx, cfg, planned)
		return err
	})
	if err != nil && r != nil && ctx.Err() == nil {
		warn := fmt.Sprintf("tearing down run %s: %v; tailsuite down --run %s removes what's left", sb.RunID(), err, sb.RunID())
		_, _ = fmt.Fprintf(stderr, "tailsuite: warning: %s\n", warn)
		r.Warnings = append(r.Warnings, warn)
		return r, nil
	}
	if err != nil {
		return nil, err
	}
	return r, nil
}

// report writes the run, and returns its exit code. A result that can't
// be written is exit 3: nobody saw it.
func report(r *runner.Run, dir string, asJSON bool, junit string, stdout, stderr io.Writer) int {
	// JUnit first: if it can't be written the exit is 3, and the results
	// printed below say so.
	var junitErr error
	if junit != "" {
		var b bytes.Buffer
		junitErr = runner.WriteJUnit(&b, r)
		if junitErr == nil {
			junitErr = os.WriteFile(junit, b.Bytes(), 0o644) //nolint:gosec // G306: a report for CI to read
		}
		if junitErr != nil {
			junitErr = fmt.Errorf("--junit: %w", junitErr)
			r.Exit = exitCouldNotRun
			r.Warnings = append(r.Warnings, junitErr.Error())
		}
	}
	write := runner.WriteText
	if asJSON {
		write = runner.WriteJSON
	}
	writeErr := write(stdout, r)
	// The last results, for the UI (#11). Losing them loses no result.
	var b bytes.Buffer
	if err := runner.WriteJSON(&b, r); err == nil {
		err = os.MkdirAll(filepath.Join(dir, stateDir), 0o700)
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, stateDir, "results.json"), b.Bytes(), 0o600)
		}
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "tailsuite: warning: couldn't save the results in %s: %v\n", stateDir, err)
		}
	}
	if err := errors.Join(junitErr, writeErr); err != nil {
		return fail(stderr, err)
	}
	return r.Exit
}
