//go:build integration

package runner

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/agent/control"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

// A real headscale lab, running the synthetic startup project's suite:
//
//	go test -tags integration ./internal/runner/

var agentBinary []byte

func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "tailsuite-agent-")
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	out := filepath.Join(dir, "tailsuite-agent")
	build := exec.Command("go", "build", "-trimpath", "-o", out, "github.com/bulkhead-labs/tailsuite/cmd/tailsuite-agent") //nolint:gosec,noctx // G204: out is this test's own temporary file; the build has no deadline to honour
	build.Env = append(os.Environ(), "GOOS=linux", "GOARCH="+runtime.GOARCH, "CGO_ENABLED=0")
	build.Stdout, build.Stderr = os.Stderr, os.Stderr
	if err := build.Run(); err != nil {
		fmt.Fprintln(os.Stderr, "building the agent:", err)
		os.Exit(1)
	}
	agentBinary, err = os.ReadFile(out)
	_ = os.RemoveAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

type fixture struct {
	sb      *docker.Sandbox
	lab     engine.Lab
	project *project.Project
	planned func() []suite.Planned
}

func startLab(t *testing.T, extra ...suite.Test) *fixture {
	t.Helper()
	ctx := context.Background()
	p, err := project.Load("../suite/testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	low, err := headscale.Lower(p.Tailnet)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := docker.New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), sandbox.TeardownTimeout)
		defer cancel()
		if err := sb.Teardown(ctx); err != nil {
			t.Errorf("teardown: %v", err)
		}
		_ = sb.Close()
	})
	e := headscale.New(headscale.Config{Sandbox: sb, Agent: agentBinary})
	lab, err := e.Start(ctx, engine.PlanEveryDevice(p.Tailnet), low)
	if err != nil {
		t.Fatal(err)
	}
	planned := func() []suite.Planned {
		tests, err := suite.ReadDir(p.Dir)
		if err != nil {
			t.Fatal(err)
		}
		tests = append(append(tests, extra...), suite.PolicyTests(p.Tailnet.Policy)...)
		pl, err := suite.Build(p, tests, e.Capabilities(), low)
		if err != nil {
			t.Fatal(err)
		}
		return pl
	}
	return &fixture{sb: sb, lab: lab, project: p, planned: planned}
}

func (f *fixture) config() Config {
	o, _ := suite.Resolve(f.project.Lab, suite.Options{})
	return Config{Sandbox: f.sb, Lab: f.lab, Engine: headscale.Name, Version: "headscale " + headscale.HeadscaleVersion, Project: f.project, Options: o}
}

func test(t *testing.T, run *Run, name string) TestResult {
	t.Helper()
	i := slices.IndexFunc(run.Tests, func(r TestResult) bool { return r.Name == name })
	if i < 0 {
		t.Fatalf("no result for %q", name)
	}
	return run.Tests[i]
}

func pair(t *testing.T, tr TestResult, from string) PairResult {
	t.Helper()
	i := slices.IndexFunc(tr.Pairs, func(p PairResult) bool { return p.From == from })
	if i < 0 {
		t.Fatalf("%s: no pair from %s", tr.Name, from)
	}
	return tr.Pairs[i]
}

func TestTheStartupSuite(t *testing.T) {
	wrong := suite.Test{Name: "the laptop reaches the database (wrongly expected)", From: "laptop", To: "db", Port: 5432, Expect: suite.Allow, Source: suite.SourceTests, Where: "test", Protocols: []string{"tcp"}}
	f := startLab(t, wrong)
	run, err := Execute(context.Background(), f.config(), f.planned())
	if err != nil {
		t.Fatal(err)
	}

	// An allow, proven: Alice's laptop reaches the server over HTTP.
	e := test(t, run, "engineers reach the web tier")
	if p := pair(t, e, "laptop"); p.Result != Pass || p.Probe != "http" {
		t.Errorf("laptop: %+v", p)
	}
	// Bob's laptop could be touched by the rule headscale's copy leaves
	// out, so it's REFUSED, and so is the test: never a pass.
	if p := pair(t, e, "bobs-laptop"); p.Result != Refused {
		t.Errorf("bobs-laptop: %+v", p)
	}
	if e.Result != Refused {
		t.Errorf("test: %s", e.Result)
	}

	// A deny with no path at all: no route, and the positive control
	// (the server, which the database's filter admits) shows db alive.
	d := test(t, run, "laptops can't reach the database")
	if d.Result != Pass {
		t.Fatalf("%+v", d)
	}
	h := d.Pairs[0].Halves[0]
	if h.Saw != sawBlocked || h.Control == nil || !h.Control.Alive || h.Control.From != "server" {
		t.Errorf("deny half %+v control %+v", h, h.Control)
	}

	// A deny between peers: dropped. Nothing may reach the admin port,
	// so the control comes from the server itself.
	a := pair(t, test(t, run, "the web tier's admin port is closed to engineers"), "laptop")
	if a.Result != Pass || a.Halves[0].Outcome.Outcome != agent.Timeout || a.Halves[0].Control.From != "server" {
		t.Errorf("admin port: %+v %+v", a, a.Halves[0].Control)
	}

	if r := test(t, run, "the web tier reaches the database"); r.Result != Pass {
		t.Errorf("%+v", r)
	}
	if r := test(t, run, wrong.Name); r.Result != Fail {
		t.Errorf("a wrong expectation: %+v", r)
	}

	// The policy's own tests: an accept passes on TCP alone (either half);
	// a deny with an omitted proto is UNKNOWN until UDP probes exist; one
	// naming no lab device is UNKNOWN.
	if r := test(t, run, "policy tests[0]: alice@example.com → tag:prod:80"); r.Result != Pass {
		t.Errorf("%+v", r)
	}
	if r := test(t, run, "policy tests[0]: alice@example.com → tag:db:5432"); r.Result != Unknown {
		t.Errorf("%+v", r)
	}
	if r := test(t, run, "policy tests[1]: carol@example.com → tag:prod:80"); r.Result != Unknown {
		t.Errorf("%+v", r)
	}
	if run.Exit != 1 || run.Summary.Fail != 1 {
		t.Errorf("exit %d, %+v", run.Exit, run.Summary)
	}
}

// A dead service never reads as a deny (0001). db's port answers every
// connection with a reset, its own included, while its listener stays
// declared: the laptop's probe is still blocked (no route), but the
// positive control can't show db alive, so the deny isn't proven.
func TestADeadServiceIsNeverADeny(t *testing.T) {
	f := startLab(t)
	db := node(t, f, "db")
	var out strings.Builder
	code, err := f.sb.Exec(context.Background(), db.Sandbox, sandbox.ExecSpec{
		Cmd:    []string{"iptables", "-I", "INPUT", "-p", "tcp", "--dport", "5432", "-j", "REJECT", "--reject-with", "tcp-reset"},
		Stdout: &out, Stderr: &out,
	})
	if err != nil || code != 0 {
		t.Fatalf("iptables: %d %v %s", code, err, out.String())
	}
	run, err := Execute(context.Background(), f.config(), f.planned())
	if err != nil {
		t.Fatal(err)
	}
	d := test(t, run, "laptops can't reach the database")
	if d.Result != Unknown || !strings.Contains(d.Reason, "positive control") {
		t.Errorf("%+v", d)
	}
	if c := d.Pairs[0].Halves[0].Control; c == nil || c.Alive {
		t.Errorf("control %+v", c)
	}
	// An allow to it is UNKNOWN too: refused isn't the lab's deny.
	if r := test(t, run, "the web tier reaches the database"); r.Result != Unknown || !strings.Contains(r.Reason, "refused") {
		t.Errorf("an allow to a dead service: %+v", r)
	}
}

// Listeners another process changes during a run make what they served
// UNKNOWN (0020): here db's are closed once they're declared.
func TestChangedListenersAreUnknown(t *testing.T) {
	f := startLab(t)
	cfg := f.config()
	db := node(t, f, "db")
	cfg.afterDeclare = func() {
		s, err := control.Start(context.Background(), f.sb, db.Sandbox)
		if err != nil {
			t.Error(err)
			return
		}
		defer func() { _ = s.Close() }()
		if _, _, err := s.SetListeners(context.Background(), []agent.Listener{}); err != nil {
			t.Error(err)
		}
	}
	run, err := Execute(context.Background(), cfg, f.planned())
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"laptops can't reach the database", "the web tier reaches the database"} {
		if r := test(t, run, name); r.Result != Unknown || !strings.Contains(r.Reason, "listeners changed") {
			t.Errorf("%s: %+v", name, r)
		}
	}
	// The server's weren't touched.
	if r := pair(t, test(t, run, "engineers reach the web tier"), "laptop"); r.Result != Pass {
		t.Errorf("%+v", r)
	}
}

func node(t *testing.T, f *fixture, name string) engine.Node {
	t.Helper()
	for _, n := range f.lab.Nodes() {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("no node %s", name)
	return engine.Node{}
}
