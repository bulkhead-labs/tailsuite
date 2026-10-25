//go:build integration

// Integration tests against a real Docker Engine:
//
//	go test -tags integration ./internal/sandbox/docker/
//
// They pull a pinned Tailscale image, start real tailscaled, and remove
// everything they create.
package docker

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/moby/client"

	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// tailscaleImage is the official client image, v1.102.5 (released
// 2026-09-29), pinned by its index digest.
const tailscaleImage = "tailscale/tailscale:v1.102.5@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"

// tailscaled in kernel mode, with nothing to log in to and no logs sent
// anywhere. The lab network is internal, so it can't reach out anyway.
var tailscaled = []string{"tailscaled", "--tun=tailscale0", "--statedir=/tmp/tailscale", "--no-logs-no-support"}

func newSandbox(t *testing.T) *Sandbox {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	sb, err := New(ctx)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), sandbox.TeardownTimeout)
		defer cancel()
		if err := sb.Teardown(ctx); err != nil {
			t.Errorf("teardown: %v", err)
		}
		assertGone(t, sb.cli, sb.RunID())
		_ = sb.Close()
	})
	return sb
}

// assertGone fails the test if anything labelled with the run remains.
func assertGone(t *testing.T, cli *client.Client, runID string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runs, err := Runs(ctx, cli)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range runs {
		if id == runID {
			t.Errorf("run %s still has containers or networks", runID)
		}
	}
}

// waitFor polls check until it succeeds or the deadline passes. It's for
// tailscaled starting up, the one wait these tests need.
func waitFor(ctx context.Context, d time.Duration, check func() error) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	for {
		err := check()
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("after %s: %w", d, err)
		case <-time.After(200 * time.Millisecond):
		}
	}
}

func TestTailnetNodeRunsKernelTUN(t *testing.T) {
	ctx := context.Background()
	sb := newSandbox(t)
	lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := sb.StartNode(ctx, sandbox.NodeSpec{Name: "laptop", Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: tailscaled})
	if err != nil {
		t.Fatal(err)
	}
	if !node.Addrs["lan"].IsValid() {
		t.Errorf("node has no address on lan: %v", node.Addrs)
	}
	if err := waitFor(ctx, 20*time.Second, func() error {
		return sandbox.CheckKernelTUN(ctx, sb, node, "tailscale0")
	}); err != nil {
		t.Fatal(err)
	}

	// The lab network is internal: no default route out of the lab.
	var routes bytes.Buffer
	if code, err := sb.Exec(ctx, node, sandbox.ExecSpec{Cmd: []string{"ip", "route", "show", "default"}, Stdout: &routes}); err != nil || code != 0 {
		t.Fatalf("ip route: code %d, %v", code, err)
	}
	if strings.TrimSpace(routes.String()) != "" {
		t.Errorf("an internal network has a default route: %q", routes.String())
	}
}

// A node that runs tailscaled in userspace mode has no TUN interface, and
// CheckKernelTUN says so. Only a service can be made to run that way.
func TestCheckKernelTUNCatchesUserspace(t *testing.T) {
	ctx := context.Background()
	sb := newSandbox(t)
	lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	userspace := []string{"tailscaled", "--tun=userspace-networking", "--statedir=/tmp/tailscale", "--no-logs-no-support"}
	if _, err := sb.StartNode(ctx, sandbox.NodeSpec{Name: "laptop", Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: userspace}); !errors.Is(err, sandbox.ErrUserspace) {
		t.Fatalf("a userspace tailnet node: err = %v, want ErrUserspace", err)
	}
	svc, err := sb.StartNode(ctx, sandbox.NodeSpec{Name: "svc", Role: sandbox.RoleService, Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: userspace})
	if err != nil {
		t.Fatal(err)
	}
	// Give tailscaled time to start, so the check isn't passing on an
	// empty node: wait for its socket.
	if err := waitFor(ctx, 20*time.Second, func() error {
		code, err := sb.Exec(ctx, svc, sandbox.ExecSpec{Cmd: []string{"tailscale", "status"}})
		if err != nil {
			return err
		}
		if code == 0 || code == 1 { // 1: logged out, which is fine
			return nil
		}
		return fmt.Errorf("tailscale status exited %d", code)
	}); err != nil {
		t.Fatal(err)
	}
	if err := sandbox.CheckKernelTUN(ctx, sb, svc, "tailscale0"); !errors.Is(err, sandbox.ErrUserspace) {
		t.Fatalf("CheckKernelTUN on a userspace node = %v, want ErrUserspace", err)
	}
}

func TestExec(t *testing.T) {
	ctx := context.Background()
	sb := newSandbox(t)
	lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	node, err := sb.StartNode(ctx, sandbox.NodeSpec{
		Name: "box", Role: sandbox.RoleService, Image: tailscaleImage,
		Networks:   []sandbox.Network{lan},
		Entrypoint: []string{"sleep", "infinity"},
		Files:      []sandbox.File{{Path: "/opt/tailsuite/greet", Mode: 0o755, Content: []byte("#!/bin/sh\necho \"hello, $1\"\n")}},
	})
	if err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	code, err := sb.Exec(ctx, node, sandbox.ExecSpec{
		Cmd:    []string{"sh", "-c", "cat; echo oops >&2; exit 3"},
		Stdin:  strings.NewReader("from stdin\n"),
		Stdout: &out, Stderr: &errOut,
	})
	if err != nil {
		t.Fatal(err)
	}
	if code != 3 || out.String() != "from stdin\n" || errOut.String() != "oops\n" {
		t.Errorf("code %d, stdout %q, stderr %q; want 3, %q, %q", code, out.String(), errOut.String(), "from stdin\n", "oops\n")
	}

	out.Reset()
	if code, err := sb.Exec(ctx, node, sandbox.ExecSpec{Cmd: []string{"/opt/tailsuite/greet", "lab"}, Stdout: &out}); err != nil || code != 0 {
		t.Fatalf("the copied file: code %d, %v", code, err)
	}
	if out.String() != "hello, lab\n" {
		t.Errorf("the copied file said %q", out.String())
	}

	// A cancelled context stops the wait.
	cctx, cancel := context.WithTimeout(ctx, 500*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := sb.Exec(cctx, node, sandbox.ExecSpec{Cmd: []string{"sleep", "30"}}); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Exec after the deadline = %v, want DeadlineExceeded", err)
	}
	if time.Since(start) > 10*time.Second {
		t.Errorf("Exec took %s to notice its deadline", time.Since(start))
	}
}

func TestNamesLabelsAndDuplicates(t *testing.T) {
	ctx := context.Background()
	sb := newSandbox(t)
	lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"}); err == nil {
		t.Error("a second network named lan was created")
	}
	spec := sandbox.NodeSpec{Name: "db", Role: sandbox.RoleService, Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: []string{"sleep", "infinity"}}
	node, err := sb.StartNode(ctx, spec)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := sb.StartNode(ctx, spec); err == nil {
		t.Error("a second node named db was started")
	}
	other := spec
	other.Name = "web"
	other.Networks = []sandbox.Network{{Name: "lan", ID: "not-this-runs"}}
	if _, err := sb.StartNode(ctx, other); err == nil {
		t.Error("a node joined a network from outside the run")
	}

	got, err := sb.cli.ContainerInspect(ctx, node.ID, client.ContainerInspectOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if want := "/" + sandbox.ResourceName(sb.RunID(), "db"); got.Container.Name != want {
		t.Errorf("container name = %q, want %q", got.Container.Name, want)
	}
	if l := got.Container.Config.Labels; l[sandbox.LabelRun] != sb.RunID() || l[sandbox.LabelName] != "db" || l[sandbox.LabelRole] != "service" {
		t.Errorf("labels = %v", l)
	}
}

// Two runs side by side: tearing one down leaves the other alone.
func TestRunsAreIsolated(t *testing.T) {
	ctx := context.Background()
	a, b := newSandbox(t), newSandbox(t)
	for _, sb := range []*Sandbox{a, b} {
		lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := sb.StartNode(ctx, sandbox.NodeSpec{Name: "db", Role: sandbox.RoleService, Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: []string{"sleep", "infinity"}}); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.Teardown(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Teardown(ctx); err != nil {
		t.Fatalf("a second teardown: %v", err)
	}
	runs, err := Runs(ctx, a.cli)
	if err != nil {
		t.Fatal(err)
	}
	var aLeft, bLeft bool
	for _, id := range runs {
		aLeft = aLeft || id == a.RunID()
		bLeft = bLeft || id == b.RunID()
	}
	if aLeft || !bLeft {
		t.Errorf("after tearing down %s: runs = %v", a.RunID(), runs)
	}
}

// An interrupt tears the lab down. The test runs itself as a child, which
// starts a node and waits; the parent sends it SIGINT and checks that the
// run is gone.
func TestInterruptTearsDown(t *testing.T) {
	if os.Getenv("TAILSUITE_SANDBOX_CHILD") == "1" {
		runChild()
		return
	}
	ctx := context.Background()
	cli, err := Connect(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cli.Close() }()

	cctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	cmd := exec.CommandContext(cctx, os.Args[0], "-test.run=^TestInterruptTearsDown$", "-test.v") //nolint:gosec // G204: it runs this test binary itself
	cmd.Env = append(os.Environ(), "TAILSUITE_SANDBOX_CHILD=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	// The child prints "RUN <id>" once its node is up. Read all of its
	// output, as Wait requires.
	runID := make(chan string, 1)
	drained := make(chan []string, 1)
	go func() {
		var lines []string
		sent := false
		sc := bufio.NewScanner(stdout)
		for sc.Scan() {
			lines = append(lines, sc.Text())
			if id, ok := strings.CutPrefix(sc.Text(), "RUN "); ok && !sent {
				runID <- id
				sent = true
			}
		}
		if !sent {
			runID <- ""
		}
		drained <- lines
	}()
	var id string
	select {
	case id = <-runID:
	case <-time.After(2 * time.Minute):
	}
	if id == "" {
		_ = cmd.Process.Kill()
		t.Fatalf("the child never reported a run; stdout:\n%s\nstderr:\n%s", strings.Join(<-drained, "\n"), stderr.String())
	}
	t.Cleanup(func() { _ = TeardownRun(context.Background(), cli, id) })

	runs, err := Runs(ctx, cli)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, r := range runs {
		found = found || r == id
	}
	if !found {
		t.Fatalf("run %s isn't up before the interrupt", id)
	}

	if err := cmd.Process.Signal(syscall.SIGINT); err != nil {
		t.Fatal(err)
	}
	// Its output ends when it exits; Wait comes after the last read.
	var lines []string
	select {
	case lines = <-drained:
	case <-time.After(sandbox.TeardownTimeout + 30*time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("the child didn't exit after SIGINT")
	}
	if err := cmd.Wait(); err != nil {
		t.Errorf("the child: %v; stdout:\n%s\nstderr:\n%s", err, strings.Join(lines, "\n"), stderr.String())
	}
	assertGone(t, cli, id)
}

func runChild() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	sb, err := New(ctx)
	if err != nil {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
	defer func() { _ = sb.Close() }()
	err = sandbox.Run(ctx, sb, func(ctx context.Context) error {
		lan, err := sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "lan"})
		if err != nil {
			return err
		}
		if _, err := sb.StartNode(ctx, sandbox.NodeSpec{Name: "db", Role: sandbox.RoleService, Image: tailscaleImage, Networks: []sandbox.Network{lan}, Entrypoint: []string{"sleep", "infinity"}}); err != nil {
			return err
		}
		fmt.Println("RUN " + sb.RunID())
		<-ctx.Done()
		return ctx.Err()
	})
	if err != nil && !errors.Is(err, context.Canceled) {
		fmt.Println("ERROR", err)
		os.Exit(1)
	}
}
