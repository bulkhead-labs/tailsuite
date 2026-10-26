package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"

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
			if err := removeState(dir); err != nil {
				return nil, err
			}
		}
	}
	if up {
		if st.Engine != o.Engine {
			return nil, fmt.Errorf("the lab that's up (run %s) runs %s, and this run asks for %s; tailsuite down first, or choose --engine %s", st.Run, st.Engine, o.Engine, st.Engine)
		}
		if names := planNames(plan); !slices.Equal(names, st.Devices) {
			return nil, fmt.Errorf("lab.yaml's devices changed since the lab came up (run %s); tailsuite down, then up again", st.Run)
		}
		sb, err := docker.Attach(ctx, st.Run)
		if err != nil {
			return nil, err
		}
		defer func() { _ = sb.Close() }()
		lab, err := headscale.New(headscale.Config{Sandbox: sb}).Attach(ctx, sb.Nodes(), st.Devices)
		if err != nil {
			return nil, err
		}
		// The policy may have changed since up: apply it, and wait for
		// every node to hold it, before any probe (0016).
		if _, err := lab.Apply(ctx, low.Policy); err != nil {
			return nil, fmt.Errorf("applying the policy to the lab: %w", err)
		}
		cfg.Sandbox, cfg.Lab = sb, lab
		return runner.Execute(ctx, cfg, planned)
	}

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
	if err != nil {
		return nil, err
	}
	return r, nil
}

// report writes the run, and returns its exit code. A result that can't
// be written is exit 3: nobody saw it.
func report(r *runner.Run, dir string, asJSON bool, junit string, stdout, stderr io.Writer) int {
	write := runner.WriteText
	if asJSON {
		write = runner.WriteJSON
	}
	var errs []error
	if err := write(stdout, r); err != nil {
		errs = append(errs, err)
	}
	if junit != "" {
		var b bytes.Buffer
		err := runner.WriteJUnit(&b, r)
		if err == nil {
			err = os.WriteFile(junit, b.Bytes(), 0o644) //nolint:gosec // G306: a report for CI to read
		}
		if err != nil {
			errs = append(errs, fmt.Errorf("--junit: %w", err))
		}
	}
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
	if err := errors.Join(errs...); err != nil {
		return fail(stderr, err)
	}
	return r.Exit
}
