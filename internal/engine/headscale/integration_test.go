//go:build integration

package headscale

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/agent/control"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
)

// These run real headscale and real tailscaled, in Docker:
//
//	go test -tags integration ./internal/engine/headscale/
//
// Every policy is synthetic.

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

// The startup lab: alice's laptop, a web server and a database.
const startupPolicy = `// Synthetic.
{
	"groups": {"group:eng": ["alice@example.com"]},
	"tagOwners": {"tag:prod": ["autogroup:admin"], "tag:db": ["autogroup:admin"]},
	"grants": [
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:80"]},
		{"src": ["tag:prod"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
	],
}`

var startupPlan = engine.LabPlan{Devices: []engine.PlanDevice{
	{Name: "laptop", User: "alice@example.com"},
	{Name: "server", Tags: []string{"tag:prod"}},
	{Name: "db", Tags: []string{"tag:db"}},
}}

type testLab struct {
	*Lab
	t     *testing.T
	nodes map[string]engine.Node
}

func start(t *testing.T, src string, plan engine.LabPlan, users ...model.User) *testLab {
	t.Helper()
	ctx := context.Background()
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
	l, err := Lower(tailnet(t, src, users...))
	if err != nil {
		t.Fatal(err)
	}
	e := New(Config{Sandbox: sb, Agent: agentBinary})
	lab, err := e.Start(ctx, plan, l)
	if err != nil {
		t.Fatal(err)
	}
	tl := &testLab{Lab: lab.(*Lab), t: t, nodes: map[string]engine.Node{}}
	for _, n := range lab.Nodes() {
		tl.nodes[n.Name] = n
	}
	return tl
}

func (l *testLab) session(name string) *control.Session {
	l.t.Helper()
	s, err := control.Start(context.Background(), l.sb, l.nodes[name].Sandbox)
	if err != nil {
		l.t.Fatal(err)
	}
	l.t.Cleanup(func() { _ = s.Close() })
	return s
}

func (l *testLab) listen(name string, ports ...int) {
	l.t.Helper()
	var want []agent.Listener
	for _, p := range ports {
		want = append(want, agent.Listener{Proto: "tcp", Port: p})
	}
	if _, _, err := l.session(name).SetListeners(context.Background(), want); err != nil {
		l.t.Fatal(err)
	}
}

func (l *testLab) probe(from, to string, port int) agent.ProbeResult {
	l.t.Helper()
	r, err := l.session(from).Probe(context.Background(), agent.ProbeSpec{
		Proto: "tcp", Host: l.nodes[to].IPv4.String(), Port: port, Timeout: agent.Duration(3 * time.Second),
	})
	if err != nil {
		l.t.Fatal(err)
	}
	return r
}

func (l *testLab) filter(name string) engine.Filter {
	l.t.Helper()
	f, err := l.CompiledFilter(context.Background(), l.nodes[name])
	if err != nil {
		l.t.Fatal(err)
	}
	return f
}

// waitFilter polls a node's filter until ok holds, bounded: the plan's
// one allowed kind of wait, for policy propagation.
func (l *testLab) waitFilter(name string, ok func(engine.Filter) bool) engine.Filter {
	l.t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for {
		f := l.filter(name)
		if ok(f) {
			return f
		}
		if time.Now().After(deadline) {
			l.t.Fatalf("node %s's filter never got there: %+v", name, f.Rules)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func TestStartAndProbe(t *testing.T) {
	l := start(t, startupPolicy, startupPlan)
	for name, n := range l.nodes {
		if !n.IPv4.IsValid() || !n.IPv6.IsValid() {
			t.Errorf("%s has addresses %v %v", name, n.IPv4, n.IPv6)
		}
		// Start waited for the marker in every node's filter.
		applied, ops := l.Applied()
		if len(ops) != 1 || !strings.Contains(string(applied), engine.MarkerSource) {
			t.Fatalf("applied %s with %+v", applied, ops)
		}
	}
	l.listen("server", 80)
	l.listen("db", 5432)

	// The allow, with the peer that answered named.
	if r := l.probe("laptop", "server", 80); r.Outcome != agent.Success || r.Peer != "server" {
		t.Errorf("laptop → server:80: %+v", r)
	}
	if r := l.probe("server", "db", 5432); r.Outcome != agent.Success || r.Peer != "db" {
		t.Errorf("server → db:5432: %+v", r)
	}
	// A deny comes in two forms, each with its positive control above
	// (server:80 and db:5432 are alive).
	//
	// laptop and server are peers, through the allowed path, so a port
	// the policy doesn't allow is a drop: a timeout.
	l.listen("server", 80, 5432)
	if r := l.probe("laptop", "server", 5432); r.Outcome != agent.Timeout || r.Connected {
		t.Errorf("laptop → server:5432: %+v", r)
	}
	// The policy gives laptop and db no path either way, so they aren't
	// peers, and laptop has no route to db at all. That's Tailscale's
	// behaviour too: a node is sent only the peers it has a path with.
	if r := l.probe("laptop", "db", 5432); r.Outcome != agent.Error || !strings.Contains(r.Detail, "unreachable") {
		t.Errorf("laptop → db:5432: %+v", r)
	}
	// headscale's copy took the role autogroup's tag owners.
	applied, _ := l.Applied()
	if !strings.Contains(string(applied), LabAdmin) {
		t.Errorf("applied %s", applied)
	}
}

// 0016 and 0021: the marker must stay visible beside an allow-all rule, or
// the wait couldn't tell an apply had landed. Two sources were tried: one
// outside the tailnet's range (the one chosen, MarkerSource), and one
// inside.
func TestMarkerSurvivesBesideAllowAll(t *testing.T) {
	l := start(t, startupPolicy, startupPlan)
	const inside = "100.99.99.99/32"
	pol := `{"grants": [
		{"src": ["*"], "dst": ["*"], "ip": ["*"]},
		{"src": ["` + engine.MarkerSource + `"], "dst": ["*"], "ip": ["tcp:41101"]},
		{"src": ["` + inside + `"], "dst": ["*"], "ip": ["tcp:41102"]},
	]}`
	if err := l.SetPolicy(context.Background(), []byte(pol)); err != nil {
		t.Fatal(err)
	}
	own := func(f engine.Filter, src string, port uint16) bool {
		return slices.ContainsFunc(f.Rules, func(r engine.FilterRule) bool {
			return len(r.SrcIPs) == 1 && strings.TrimSuffix(r.SrcIPs[0], "/32") == strings.TrimSuffix(src, "/32") && r.HasPort(port)
		})
	}
	for name := range l.nodes {
		f := l.waitFilter(name, func(f engine.Filter) bool { return own(f, engine.MarkerSource, 41101) })
		if !own(f, inside, 41102) {
			t.Errorf("%s: the inside marker isn't a rule of its own: %+v", name, f.Rules)
		}
		if !f.HasMarker(41101) {
			t.Errorf("%s: HasMarker doesn't see it", name)
		}
		// The allow-all rule is there too, beside them.
		if !slices.ContainsFunc(f.Rules, func(r engine.FilterRule) bool { return r.HasPort(1) && r.SrcAdmits(l.nodes["laptop"].IPv4) }) {
			t.Errorf("%s: no allow-all rule: %+v", name, f.Rules)
		}
	}
}

// 0021: headscale must read {"grants": []} with no acls as deny-all. The
// shape is never applied bare in a run (Start adds the marker), so only a
// test like this one would see headscale's reading change.
func TestEmptyGrantsDeniesEverything(t *testing.T) {
	l := start(t, `{"tagOwners": {"tag:prod": [], "tag:db": []}, "grants": [{"src": ["tag:prod"], "dst": ["tag:db"], "ip": ["tcp:8080"]}]}`, startupPlan)
	l.listen("db", 8080)

	// The known state: the allowed port answers, and the filter holds
	// that rule.
	if r := l.probe("server", "db", 8080); r.Outcome != agent.Success {
		t.Fatalf("positive control: server → db:8080: %+v", r)
	}
	if f := l.filter("db"); !slices.ContainsFunc(f.Rules, func(r engine.FilterRule) bool { return r.HasPort(8080) }) {
		t.Fatalf("db's filter lacks the rule: %+v", f.Rules)
	}

	if err := l.SetPolicy(context.Background(), []byte(`{"grants": []}`)); err != nil {
		t.Fatal(err)
	}
	l.waitFilter("db", func(f engine.Filter) bool {
		return !slices.ContainsFunc(f.Rules, func(r engine.FilterRule) bool { return r.HasPort(8080) })
	})
	if f := l.filter("db"); len(f.Rules) != 0 {
		t.Errorf("db's filter isn't empty: %+v", f.Rules)
	}
	// Closed: with no rule, server and db aren't peers any more, so it's
	// no route rather than a drop. Either way nothing gets through.
	if r := l.probe("server", "db", 8080); r.Outcome == agent.Success || r.Connected {
		t.Errorf("server → db:8080 after the bare shape: %+v", r)
	}
}

// TLS is the lab CA's: a node that doesn't trust it can't join. The
// positive control is every node Start joined, which trusts it.
func TestNodesTrustOnlyTheLabCA(t *testing.T) {
	l := start(t, startupPolicy, startupPlan)
	ctx := context.Background()
	n, err := l.sb.StartNode(ctx, sandbox.NodeSpec{
		Name:       "stranger",
		Image:      TailscaleImage,
		Networks:   []sandbox.Network{l.net},
		Entrypoint: control.Entrypoint("stranger", "tailscaled", "--state=mem:", "--tun=tailscale0", "--no-logs-no-support"),
		Files:      []sandbox.File{control.AgentFile(agentBinary)},
	})
	if err != nil {
		t.Fatal(err)
	}
	out, err := l.headscale(ctx, nil, "preauthkeys", "create", "--tags", "tag:prod", "--expiration", "10m", "-o", "json")
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Split(strings.Split(string(out), `"key":`)[1], `"`)[1]

	// tailscale up only times out; tailscaled's own log says why. It
	// streams, so it's read while the join runs, and stopped after.
	var logs syncBuffer
	lctx, stop := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = l.sb.Exec(lctx, n, sandbox.ExecSpec{Cmd: []string{"tailscale", "debug", "daemon-logs"}, Stdout: &logs})
	}()
	time.Sleep(time.Second)
	_, err = l.run(ctx, n, []byte(key), "tailscale", "up", "--login-server=https://"+ControlName, "--auth-key=file:/dev/stdin", "--hostname=stranger", "--timeout=15s")
	stop()
	<-done
	var ee *execError
	if !errors.As(err, &ee) {
		t.Fatalf("a node without the lab CA joined: %v", err)
	}
	if !strings.Contains(logs.String(), "x509: certificate signed by unknown authority") {
		t.Errorf("the join failed, but not on the certificate: %s\n%s", ee.stderr, logs.String())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.b.String()
}

func TestRejectedPolicyIsRefused(t *testing.T) {
	l := start(t, startupPolicy, startupPlan)
	err := l.SetPolicy(context.Background(), []byte(`{"grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"], "newThing": true}]}`))
	var r *engine.Refusal
	if !errors.As(err, &r) || r.Engine != Name || !strings.Contains(r.Reason, "headscale rejected") {
		t.Fatalf("got %v", err)
	}
	// headscale still holds the last good policy: the allow still works.
	l.listen("server", 80)
	if res := l.probe("laptop", "server", 80); res.Outcome != agent.Success {
		t.Errorf("after the rejection: %+v", res)
	}
}

// The lowering of research 0005's third policy is a policy headscale
// takes: every feature it rejected, one at a time, is gone or rewritten.
func TestHeadscaleTakesTheLoweredEverything(t *testing.T) {
	pol := everything
	low, err := Lower(tailnet(t, pol))
	if err != nil || low.Refused() {
		t.Fatalf("%v %v", err, low.Err())
	}
	plan := engine.LabPlan{Devices: []engine.PlanDevice{
		{Name: "laptop", User: "alice@example.com"},
		{Name: "web", Tags: []string{"tag:prod"}},
	}}
	l := start(t, pol, plan)
	l.listen("web", 443)
	// group:eng reaches tag:prod on 443 in the copy (its posture
	// condition is gone, and the tests it touches are REFUSED).
	if r := l.probe("laptop", "web", 443); r.Outcome != agent.Success {
		t.Errorf("laptop → web:443: %+v", r)
	}
}

func TestStartRefusesARefusedLowering(t *testing.T) {
	low, err := Lower(tailnet(t, `{"newThing": 1, "grants": []}`))
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{Agent: agentBinary}).Start(context.Background(), startupPlan, low)
	var r *engine.Refusal
	if !errors.As(err, &r) {
		t.Fatalf("got %v", err)
	}
}
