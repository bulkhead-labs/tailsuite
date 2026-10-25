//go:build integration

// Integration tests for the node agent in real lab nodes:
//
//	go test -tags integration ./internal/agent/control/
//
// They build the agent for Linux, so they need the Go toolchain and Docker.
// The probes run between nodes on an internal lab network; nothing leaves
// the lab.
package control

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox/docker"
)

// The official Tailscale image: Alpine, with iptables and tailscaled.
// Pinned as in the sandbox's tests.
const nodeImage = "tailscale/tailscale:v1.102.5@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"

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
	agentBinary, err = os.ReadFile(out) //nolint:gosec // G304: out is this test's own temporary file
	_ = os.RemoveAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(m.Run())
}

type lab struct {
	sb  *docker.Sandbox
	lan sandbox.Network
}

func newLab(t *testing.T) *lab {
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
	lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	return &lab{sb: sb, lan: lan}
}

// node starts a tailnet node (it has NET_ADMIN, for iptables) with the
// agent as its entrypoint, running command under it, if any.
func (l *lab) node(t *testing.T, name string, command ...string) sandbox.Node {
	t.Helper()
	n, err := l.sb.StartNode(context.Background(), sandbox.NodeSpec{
		Name:       name,
		Image:      nodeImage,
		Networks:   []sandbox.Network{l.lan},
		Entrypoint: Entrypoint(name, command...),
		Files:      []sandbox.File{AgentFile(agentBinary)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// session opens a session, as one tailsuite process would.
func (l *lab) session(t *testing.T, n sandbox.Node) *Session {
	t.Helper()
	s, err := Start(context.Background(), l.sb, n)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func (l *lab) sh(t *testing.T, n sandbox.Node, script string) {
	t.Helper()
	var out strings.Builder
	code, err := l.sb.Exec(context.Background(), n, sandbox.ExecSpec{Cmd: []string{"sh", "-c", script}, Stdout: &out, Stderr: &out})
	if err != nil || code != 0 {
		t.Fatalf("%s in %s: code %d, %v: %s", script, n.Name, code, err, out.String())
	}
}

func lanAddr(n sandbox.Node) string { return n.Addrs["lan"].String() }

func TestProbesBetweenNodes(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	a := l.session(t, l.node(t, "laptop"))
	serverNode := l.node(t, "server")
	b := l.session(t, serverNode)

	if _, err := b.SetListeners(ctx, []agent.Listener{
		{Proto: agent.ProtoTCP, Port: 5432},
		{Proto: agent.ProtoHTTP, Port: 80},
		{Proto: agent.ProtoTCP, Port: 8081},
	}); err != nil {
		t.Fatal(err)
	}
	// The server drops port 8081, as a Tailscale filter drops a denied
	// connection: silently.
	l.sh(t, serverNode, "iptables -A INPUT -p tcp --dport 8081 -j DROP")

	host := lanAddr(serverNode)
	cases := []struct {
		name      string
		spec      agent.ProbeSpec
		want      agent.Outcome
		connected bool
		peer      string
	}{
		{"tcp, listening", agent.ProbeSpec{Proto: agent.ProtoTCP, Host: host, Port: 5432}, agent.Success, true, "server"},
		{"http, listening", agent.ProbeSpec{Proto: agent.ProtoHTTP, Host: host, Port: 80}, agent.Success, true, "server"},
		{"tcp, nothing listening", agent.ProbeSpec{Proto: agent.ProtoTCP, Host: host, Port: 9}, agent.Refused, false, ""},
		{"http, nothing listening", agent.ProbeSpec{Proto: agent.ProtoHTTP, Host: host, Port: 9}, agent.Refused, false, ""},
		{"tcp, dropped", agent.ProbeSpec{Proto: agent.ProtoTCP, Host: host, Port: 8081, Timeout: agent.Duration(time.Second)}, agent.Timeout, false, ""},
		// TEST-NET-1: the internal network has no route to it, so no
		// packet leaves the node.
		{"no route", agent.ProbeSpec{Proto: agent.ProtoTCP, Host: "192.0.2.1", Port: 80, Timeout: agent.Duration(time.Second)}, agent.Error, false, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res, err := a.Probe(ctx, c.spec)
			if err != nil {
				t.Fatal(err)
			}
			if res.Outcome != c.want || res.Connected != c.connected || res.Peer != c.peer {
				t.Errorf("probe = %+v; want %s, connected %v, peer %q", res, c.want, c.connected, c.peer)
			}
			if c.want == agent.Success && res.Connect <= 0 {
				t.Errorf("no connect time: %+v", res)
			}
		})
	}
}

// `tailsuite up` and `tailsuite test` are separate processes. What one
// opens, the next finds, and both can be connected at once.
func TestListenersOutliveSessions(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	laptop := l.session(t, l.node(t, "laptop"))
	server := l.node(t, "server")
	want := []agent.Listener{{Proto: agent.ProtoTCP, Port: 5432}}

	up, err := Start(ctx, l.sb, server)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := up.SetListeners(ctx, want); err != nil {
		t.Fatal(err)
	}
	if err := up.Close(); err != nil {
		t.Fatalf("closing the first session: %v", err)
	}
	probe := agent.ProbeSpec{Proto: agent.ProtoTCP, Host: lanAddr(server), Port: 5432}
	if res, err := laptop.Probe(ctx, probe); err != nil || res.Outcome != agent.Success {
		t.Fatalf("after the first session closed: %+v, %v", res, err)
	}

	// Two sessions at once declare the same set; nothing changes.
	first, second := l.session(t, server), l.session(t, server)
	for _, s := range []*Session{first, second} {
		open, err := s.SetListeners(ctx, want)
		if err != nil || len(open) != 1 {
			t.Fatalf("declaring again: %+v, %v", open, err)
		}
	}
	// Declaring none closes it, for everyone.
	if _, err := second.SetListeners(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if open, err := first.Listeners(ctx); err != nil || len(open) != 0 {
		t.Errorf("the other session sees %+v, %v", open, err)
	}
	if res, err := laptop.Probe(ctx, probe); err != nil || res.Outcome != agent.Refused {
		t.Errorf("after declaring none: %+v, %v; want refused", res, err)
	}
}

// The agent is controlled through the sandbox, never over a network: a
// node with every packet dropped still answers.
func TestControlIsOutOfBand(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	n := l.node(t, "laptop")
	l.sh(t, n, "iptables -P INPUT DROP && iptables -P OUTPUT DROP && iptables -P FORWARD DROP")
	s := l.session(t, n)
	if _, err := s.Hello(ctx); err != nil {
		t.Fatalf("an isolated node's agent didn't answer: %v", err)
	}
	res, err := s.Probe(ctx, agent.ProbeSpec{Proto: agent.ProtoTCP, Host: "192.0.2.1", Port: 80, Timeout: agent.Duration(500 * time.Millisecond)})
	if err != nil {
		t.Fatalf("an isolated node couldn't run a probe: %v", err)
	}
	if res.Outcome == agent.Success {
		t.Errorf("a node with OUTPUT dropped connected: %+v", res)
	}
}

// The node's real process runs under the agent: tailscaled, in kernel
// mode, as the engines will run it.
func TestTailscaledUnderTheAgent(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	n := l.node(t, "laptop", "tailscaled", "--tun=tailscale0", "--statedir=/tmp/tailscale", "--no-logs-no-support")
	s := l.session(t, n)
	if _, err := s.Hello(ctx); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for {
		err := sandbox.CheckKernelTUN(ctx, l.sb, n, "tailscale0")
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("after 20s: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

func TestStartFailsWithoutTheAgent(t *testing.T) {
	ctx := context.Background()
	l := newLab(t)
	n, err := l.sb.StartNode(ctx, sandbox.NodeSpec{Name: "bare", Image: nodeImage, Networks: []sandbox.Network{l.lan}, Entrypoint: []string{"sleep", "infinity"}})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := Start(ctx, l.sb, n); err == nil {
		_ = s.Close()
		t.Fatal("Start succeeded in a node without the agent")
	}
}
