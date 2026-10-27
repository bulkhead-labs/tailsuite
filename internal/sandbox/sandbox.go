// Package sandbox runs a lab's machines: its networks, one container per
// node, commands inside the nodes, and teardown (decision 0003). The Docker
// implementation is in sandbox/docker.
//
// A sandbox never routes control traffic over a lab network or the
// tailnet. Commands run through the sandbox's own API (Exec), so a deny
// rule can't cut off the test's own control traffic (decision 0019).
package sandbox

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/netip"
	"path"
	"regexp"
	"strings"
	"time"
)

// Labels on everything a sandbox creates. Teardown finds a run's resources
// by them, so it works after a crash, with no state file.
const (
	LabelRun  = "com.bulkheadlabs.tailsuite.run"
	LabelName = "com.bulkheadlabs.tailsuite.name"
	LabelRole = "com.bulkheadlabs.tailsuite.role"
	// LabelProject, when set, names the project folder a run belongs to,
	// by a hash of its path, so `up --clean` finds its leftovers.
	LabelProject = "com.bulkheadlabs.tailsuite.project"
)

// DefaultNodeMemory caps each node's memory when its spec doesn't. tailscaled
// used 20–31 MiB in the 2026-10-23 benchmark (research 0004).
const DefaultNodeMemory int64 = 128 << 20

// TeardownTimeout bounds the teardown that Run does on the way out.
const TeardownTimeout = 2 * time.Minute

// ErrUserspace means a tailnet node would run, or is running, tailscaled in
// userspace networking mode. Outbound connections from such a node fail
// silently, which looks exactly like a deny (research 0004), so it's
// refused.
var ErrUserspace = errors.New("userspace networking is refused: tailnet nodes run tailscaled with a kernel TUN device")

// A Sandbox creates one run's networks and nodes, runs commands in them,
// and tears all of it down.
type Sandbox interface {
	// RunID names this run. Every resource the sandbox creates is named
	// and labelled with it.
	RunID() string

	// CreateNetwork creates a network for this run.
	CreateNetwork(ctx context.Context, spec NetworkSpec) (Network, error)

	// StartNode creates a node on the given networks, copies its files in,
	// and starts it.
	StartNode(ctx context.Context, spec NodeSpec) (Node, error)

	// Exec runs a command in a node through the sandbox's API, never over
	// a network, and waits for it to exit. It returns the command's exit
	// code. A non-nil error means the sandbox couldn't run the command or
	// lost track of it, not that the command failed.
	Exec(ctx context.Context, node Node, spec ExecSpec) (int, error)

	// Teardown removes everything this run created, found by its labels.
	// It's idempotent. Give it a context that isn't already cancelled.
	Teardown(ctx context.Context) error
}

// NetworkSpec describes a lab network.
type NetworkSpec struct {
	// Name is unique in the run.
	Name string

	// Internet gives the network a route out of the lab. Off, the network
	// is internal: no route out, and on the Docker Engines the sandbox
	// accepts, no DNS out. Its nodes can still connect to the host itself,
	// through the network's gateway address (#54). Only the Tailscale
	// engine needs Internet, to reach Tailscale's control plane.
	Internet bool
}

// A Network is one the sandbox created.
type Network struct {
	Name string
	ID   string
}

// Role says what a node is for.
type Role string

const (
	// RoleTailnetNode runs real tailscaled with a kernel TUN device. It's
	// the zero value's meaning too: a spec has to opt out of TUN, never in.
	RoleTailnetNode Role = "tailnet-node"

	// RoleService is a lab machine that isn't on the tailnet, such as the
	// control plane or a DERP server. It gets no TUN device.
	RoleService Role = "service"
)

// NodeSpec describes one node.
type NodeSpec struct {
	// Name is unique in the run, and is the node's hostname. It's a DNS
	// label: lower-case letters, digits and hyphens.
	Name string

	// Role defaults to RoleTailnetNode.
	Role Role

	// Image is pinned by digest: name[:tag]@sha256:<hex>.
	Image string

	// Networks the node joins, in order. At least one.
	Networks []Network

	Env        map[string]string
	Entrypoint []string // empty keeps the image's
	Cmd        []string // empty keeps the image's

	// Files are copied in before the node starts, such as the node agent.
	Files []File

	// Memory caps the node, in bytes, with no swap. Zero means
	// DefaultNodeMemory.
	Memory int64
}

// A File to put in a node before it starts.
type File struct {
	Path    string // absolute, inside the node
	Mode    fs.FileMode
	Content []byte
}

// A Node is one the sandbox started.
type Node struct {
	Name string
	ID   string
	Role Role

	// Addrs holds the node's address on each lab network, by network name.
	// These are the underlay, not tailnet addresses.
	Addrs map[string]netip.Addr
}

// ExecSpec describes a command to run in a node.
type ExecSpec struct {
	Cmd []string

	// Stdin, if set, is copied to the command until it ends; then the
	// command's stdin is closed. If the command exits first, Exec returns
	// while the copy may still be waiting in Stdin's Read: close Stdin
	// then, so the copy ends rather than taking the next write.
	Stdin io.Reader

	// Stdout and Stderr receive the command's output. Nil discards it.
	Stdout io.Writer
	Stderr io.Writer
}

// Run calls fn, then tears the sandbox down however fn ends: when it
// returns, fails or panics. fn must return when ctx is cancelled, as by an
// interrupt through signal.NotifyContext; then Run tears down too.
// Teardown gets a context of its own, so a cancelled ctx doesn't stop it. A
// process that's killed outright (SIGKILL) can't tear down; removing the
// run by its ID cleans up after it.
func Run(ctx context.Context, sb Sandbox, fn func(context.Context) error) (err error) {
	defer func() {
		tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), TeardownTimeout)
		defer cancel()
		if terr := sb.Teardown(tctx); terr != nil {
			err = errors.Join(err, fmt.Errorf("tearing down run %s: %w", sb.RunID(), terr))
		}
	}()
	return fn(ctx)
}

var (
	runIDPattern  = regexp.MustCompile(`^[0-9a-f]{8}$`)
	namePattern   = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	digestPattern = regexp.MustCompile(`^[^@\s]+@sha256:[0-9a-f]{64}$`)
)

// NewRunID returns a random run ID: 8 hex digits.
func NewRunID() (string, error) {
	b := make([]byte, 4)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("making a run ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CheckRunID checks a run ID's form.
func CheckRunID(id string) error {
	if !runIDPattern.MatchString(id) {
		return fmt.Errorf("run ID %q: want 8 lower-case hex digits", id)
	}
	return nil
}

// CheckName checks that a network or node name is a DNS label.
func CheckName(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("name %q: want 1 to 63 lower-case letters, digits or hyphens, not starting or ending with a hyphen", name)
	}
	return nil
}

// ResourceName is the name a run gives a network or node in the sandbox.
func ResourceName(runID, name string) string {
	return "tailsuite-" + runID + "-" + name
}

// CheckPinned checks that an image reference is pinned by a sha256 digest.
func CheckPinned(image string) error {
	if !digestPattern.MatchString(image) {
		return fmt.Errorf("image %q isn't pinned by digest: want name[:tag]@sha256:<64 hex digits>", image)
	}
	return nil
}

// CheckNodeSpec checks a node spec before anything is created.
func CheckNodeSpec(spec NodeSpec) error {
	if err := CheckName(spec.Name); err != nil {
		return fmt.Errorf("node: %w", err)
	}
	if err := CheckPinned(spec.Image); err != nil {
		return fmt.Errorf("node %s: %w", spec.Name, err)
	}
	if len(spec.Networks) == 0 {
		return fmt.Errorf("node %s: no networks", spec.Name)
	}
	if spec.Memory < 0 {
		return fmt.Errorf("node %s: negative memory limit", spec.Name)
	}
	for _, f := range spec.Files {
		if !path.IsAbs(f.Path) || path.Clean(f.Path) == "/" || strings.Contains(f.Path, "..") {
			return fmt.Errorf("node %s: file path %q: want an absolute path to a file, without \"..\"", spec.Name, f.Path)
		}
	}
	switch spec.Role {
	case "", RoleTailnetNode:
		return checkKernelMode(spec)
	case RoleService:
		return nil
	default:
		return fmt.Errorf("node %s: unknown role %q", spec.Name, spec.Role)
	}
}

// checkKernelMode refuses a tailnet node whose configuration asks for
// userspace networking, through the official image's TS_USERSPACE or
// tailscaled's --tun flag.
func checkKernelMode(spec NodeSpec) error {
	if v, ok := spec.Env["TS_USERSPACE"]; ok && v != "false" {
		return fmt.Errorf("node %s: TS_USERSPACE=%s: %w", spec.Name, v, ErrUserspace)
	}
	args := append(append([]string{}, spec.Entrypoint...), spec.Cmd...)
	args = append(args, strings.Fields(spec.Env["TS_TAILSCALED_EXTRA_ARGS"])...)
	for i, a := range args {
		if strings.Contains(a, "userspace-networking") {
			return fmt.Errorf("node %s: argument %q: %w", spec.Name, a, ErrUserspace)
		}
		if (a == "--tun" || a == "-tun") && i+1 < len(args) && args[i+1] == "userspace-networking" {
			return fmt.Errorf("node %s: argument %q: %w", spec.Name, a+" "+args[i+1], ErrUserspace)
		}
	}
	return nil
}

// CheckKernelTUN checks that a running node has a kernel TUN interface named
// iface, as tailscaled creates in kernel mode ("tailscale0" by default). A
// node in userspace mode has none, and gets ErrUserspace. It runs `cat` in
// the node. Engines call it on every tailnet node once tailscaled is up
// (#5, #6): CheckNodeSpec only sees the spec, not what the image's own
// scripts do.
func CheckKernelTUN(ctx context.Context, sb Sandbox, node Node, iface string) error {
	if err := CheckName(iface); err != nil {
		return fmt.Errorf("interface: %w", err)
	}
	var stderr strings.Builder
	code, err := sb.Exec(ctx, node, ExecSpec{Cmd: []string{"cat", "/sys/class/net/" + iface + "/tun_flags"}, Stderr: &stderr})
	if err != nil {
		return fmt.Errorf("checking node %s for a TUN interface: %w", node.Name, err)
	}
	switch code {
	case 0:
		return nil
	case 1: // cat: no such file
		return fmt.Errorf("node %s has no TUN interface %s: %w", node.Name, iface, ErrUserspace)
	default:
		return fmt.Errorf("couldn't check node %s for a TUN interface: cat exited %d: %s", node.Name, code, strings.TrimSpace(stderr.String()))
	}
}
