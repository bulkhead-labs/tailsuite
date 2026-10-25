package sandbox

import (
	"context"
	"errors"
	"strings"
	"testing"
)

const pinned = "tailscale/tailscale:v1.102.5@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"

func TestNewRunID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id, err := NewRunID()
		if err != nil {
			t.Fatal(err)
		}
		if err := CheckRunID(id); err != nil {
			t.Fatal(err)
		}
		if seen[id] {
			t.Fatalf("run ID %s repeated", id)
		}
		seen[id] = true
	}
}

func TestCheckRunID(t *testing.T) {
	for _, bad := range []string{"", "0123456", "012345678", "0123456G", "ABCDEF01", "0123-567", "x=y,z=w1"} {
		if CheckRunID(bad) == nil {
			t.Errorf("CheckRunID(%q) accepted it", bad)
		}
	}
	if err := CheckRunID("0a1b2c3d"); err != nil {
		t.Error(err)
	}
}

func TestCheckName(t *testing.T) {
	for _, good := range []string{"a", "laptop", "db-1", "0", strings.Repeat("a", 63)} {
		if err := CheckName(good); err != nil {
			t.Errorf("CheckName(%q): %v", good, err)
		}
	}
	for _, bad := range []string{"", "-a", "a-", "Laptop", "a_b", "a.b", "a/b", "../x", strings.Repeat("a", 64)} {
		if CheckName(bad) == nil {
			t.Errorf("CheckName(%q) accepted it", bad)
		}
	}
}

func TestResourceName(t *testing.T) {
	if got := ResourceName("0a1b2c3d", "laptop"); got != "tailsuite-0a1b2c3d-laptop" {
		t.Errorf("ResourceName = %q", got)
	}
}

func TestCheckPinned(t *testing.T) {
	for _, good := range []string{pinned, "tailscale/tailscale@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"} {
		if err := CheckPinned(good); err != nil {
			t.Errorf("CheckPinned(%q): %v", good, err)
		}
	}
	for _, bad := range []string{
		"tailscale/tailscale",
		"tailscale/tailscale:stable",
		"tailscale/tailscale@sha256:c507f3",
		"tailscale/tailscale@sha512:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065",
		"@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065",
	} {
		if CheckPinned(bad) == nil {
			t.Errorf("CheckPinned(%q) accepted it", bad)
		}
	}
}

func TestCheckNodeSpecRefusesUserspace(t *testing.T) {
	base := func() NodeSpec {
		return NodeSpec{Name: "laptop", Image: pinned, Networks: []Network{{Name: "lan", ID: "x"}}}
	}
	cases := map[string]func(*NodeSpec){
		"TS_USERSPACE=true": func(s *NodeSpec) { s.Env = map[string]string{"TS_USERSPACE": "true"} },
		"TS_USERSPACE=1":    func(s *NodeSpec) { s.Env = map[string]string{"TS_USERSPACE": "1"} },
		"--tun=userspace-networking in Cmd": func(s *NodeSpec) {
			s.Cmd = []string{"tailscaled", "--tun=userspace-networking"}
		},
		"--tun userspace-networking in Entrypoint": func(s *NodeSpec) {
			s.Entrypoint = []string{"tailscaled", "--tun", "userspace-networking"}
		},
		"-tun userspace-networking": func(s *NodeSpec) {
			s.Cmd = []string{"tailscaled", "-tun", "userspace-networking"}
		},
		"TS_TAILSCALED_EXTRA_ARGS": func(s *NodeSpec) {
			s.Env = map[string]string{"TS_TAILSCALED_EXTRA_ARGS": "--verbose=1 --tun=userspace-networking"}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			spec := base()
			mutate(&spec)
			if err := CheckNodeSpec(spec); !errors.Is(err, ErrUserspace) {
				t.Errorf("CheckNodeSpec = %v, want ErrUserspace", err)
			}
			spec.Role = RoleService
			if err := CheckNodeSpec(spec); err != nil {
				t.Errorf("a service may run anything, got %v", err)
			}
		})
	}

	spec := base()
	spec.Env = map[string]string{"TS_USERSPACE": "false"}
	spec.Cmd = []string{"tailscaled", "--tun=tailscale0"}
	if err := CheckNodeSpec(spec); err != nil {
		t.Errorf("kernel mode refused: %v", err)
	}
}

func TestCheckNodeSpec(t *testing.T) {
	ok := NodeSpec{Name: "db", Image: pinned, Networks: []Network{{Name: "lan", ID: "x"}}}
	if err := CheckNodeSpec(ok); err != nil {
		t.Fatal(err)
	}
	cases := map[string]func(*NodeSpec){
		"bad name":          func(s *NodeSpec) { s.Name = "DB" },
		"unpinned image":    func(s *NodeSpec) { s.Image = "tailscale/tailscale:stable" },
		"no networks":       func(s *NodeSpec) { s.Networks = nil },
		"negative memory":   func(s *NodeSpec) { s.Memory = -1 },
		"relative file":     func(s *NodeSpec) { s.Files = []File{{Path: "opt/agent"}} },
		"file escaping":     func(s *NodeSpec) { s.Files = []File{{Path: "/opt/../../etc/passwd"}} },
		"unknown role":      func(s *NodeSpec) { s.Role = "router" },
		"empty file name":   func(s *NodeSpec) { s.Files = []File{{Path: "/"}} },
		"directory is root": func(s *NodeSpec) { s.Files = []File{{Path: "/."}} },
	}
	for name, mutate := range cases {
		spec := ok
		mutate(&spec)
		if CheckNodeSpec(spec) == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

// fake records what Run does with it.
type fake struct {
	teardowns   int
	teardownErr error

	// The teardown context's state during the call.
	ctxErr      error
	ctxDeadline bool
}

func (f *fake) RunID() string { return "0a1b2c3d" }
func (f *fake) CreateNetwork(context.Context, NetworkSpec) (Network, error) {
	return Network{}, nil
}
func (f *fake) StartNode(context.Context, NodeSpec) (Node, error) { return Node{}, nil }
func (f *fake) Exec(context.Context, Node, ExecSpec) (int, error) { return 0, nil }
func (f *fake) Teardown(ctx context.Context) error {
	f.teardowns++
	f.ctxErr = ctx.Err()
	_, f.ctxDeadline = ctx.Deadline()
	return f.teardownErr
}

func TestRunTearsDownWhenFnSucceeds(t *testing.T) {
	f := &fake{}
	if err := Run(context.Background(), f, func(context.Context) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if f.teardowns != 1 {
		t.Fatalf("teardowns = %d, want 1", f.teardowns)
	}
}

func TestRunTearsDownWhenFnFails(t *testing.T) {
	f := &fake{teardownErr: errors.New("network busy")}
	boom := errors.New("boom")
	err := Run(context.Background(), f, func(context.Context) error { return boom })
	if f.teardowns != 1 {
		t.Fatalf("teardowns = %d, want 1", f.teardowns)
	}
	if !errors.Is(err, boom) || !errors.Is(err, f.teardownErr) {
		t.Fatalf("err = %v, want both fn's and teardown's errors", err)
	}
}

func TestRunTearsDownOnPanic(t *testing.T) {
	f := &fake{}
	defer func() {
		if recover() == nil {
			t.Fatal("the panic was swallowed")
		}
		if f.teardowns != 1 {
			t.Fatalf("teardowns = %d, want 1", f.teardowns)
		}
	}()
	_ = Run(context.Background(), f, func(context.Context) error { panic("boom") })
}

// An interrupt cancels ctx through signal.NotifyContext. Teardown still
// gets a live context.
func TestRunTearsDownWithALiveContextAfterCancel(t *testing.T) {
	f := &fake{}
	ctx, cancel := context.WithCancel(context.Background())
	err := Run(ctx, f, func(ctx context.Context) error {
		cancel()
		<-ctx.Done()
		return ctx.Err()
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if f.teardowns != 1 {
		t.Fatalf("teardowns = %d, want 1", f.teardowns)
	}
	if f.ctxErr != nil {
		t.Fatalf("teardown's context was already done: %v", f.ctxErr)
	}
	if !f.ctxDeadline {
		t.Fatal("teardown's context has no deadline")
	}
}
