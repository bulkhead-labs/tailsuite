// Package engine is the interface every control plane implements
// (decision 0016): a pure Lower, which turns the tailnet model into the
// engine's copy of the policy, and Start, which brings up a lab that's
// ready to probe. The engines are in engine/headscale and, with #6,
// engine/tailscale.
package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"

	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// An Engine runs labs on one control plane.
type Engine interface {
	// Name is the engine's name in lab.yaml's profiles: headscale or
	// tailscale.
	Name() string
	// Version names the control plane and its version, such as
	// "headscale 0.29.5". It's part of every result.
	Version() string
	// Capabilities is what the engine does natively, after a rewrite, or
	// refuses. It's data, per engine version.
	Capabilities() Capabilities
	// Lower turns the model into the engine's copy of the policy: the
	// user's file plus a recorded patch. It's pure: no I/O, so it's
	// unit-tested and fuzzed without Docker, and shown to the user
	// before the lab starts.
	Lower(t model.Tailnet) (Lowering, error)
	// Start brings up the lab and returns only when it's ready: the
	// control plane is up, the nodes have joined, the policy is applied,
	// and every node's packet filter and peer list show it. A Lowering
	// with refusals isn't started: Start returns a *Refusal.
	Start(ctx context.Context, plan LabPlan, l Lowering) (Lab, error)
}

// A Lab is a running lab.
type Lab interface {
	Nodes() []Node
	// CompiledFilter is the packet filter the node's tailscaled runs.
	CompiledFilter(ctx context.Context, n Node) (Filter, error)
	// Teardown removes the lab. It's idempotent.
	Teardown(ctx context.Context) error
}

// A Node is one tailnet node in a running lab.
type Node struct {
	// Name is the device's name in the model.
	Name string
	// IPv4 and IPv6 are its tailnet addresses.
	IPv4 netip.Addr
	IPv6 netip.Addr
	// Sandbox is its machine, for exec and agent sessions.
	Sandbox sandbox.Node
}

// LabPlan says which of the model's devices the lab rebuilds. The planner
// (#8, and #22's equivalence classes in M4) chooses them; PlanEveryDevice
// takes them all.
type LabPlan struct {
	Devices []PlanDevice
}

// PlanDevice is one device the lab rebuilds.
type PlanDevice struct {
	Name string
	// User owns the device; empty when it's tagged.
	User string
	// Tags make it a tagged device: its identity is its tags, not its user.
	Tags []string
	// IPv4 is the device's pinned Tailscale address, if any. Whether an
	// engine can pin it is a capability (PinnedIPv4).
	IPv4 string
}

// PlanEveryDevice plans a lab with every device in the model.
func PlanEveryDevice(t model.Tailnet) LabPlan {
	var p LabPlan
	for _, d := range t.Devices {
		user := d.User
		if len(d.Tags) > 0 {
			user = "" // a tagged device's identity is its tags
		}
		p.Devices = append(p.Devices, PlanDevice{Name: d.Name, User: user, Tags: d.Tags, IPv4: d.IPv4})
	}
	return p
}

// Lowering is an engine's copy of the policy, and the record of how it
// differs from the user's file (0014, 0016).
type Lowering struct {
	// Engine is the engine that lowered it.
	Engine string
	// Policy is the user's file with Patch applied, comments kept.
	Policy []byte
	// Patch is the RFC 6902 patch from the user's file to Policy, applied
	// in order. Its pointers are in the file's own spelling.
	Patch []PatchOp
	// Rewrites are the patch's exact changes: tests they touch still run.
	Rewrites []Rewrite
	// Changes are the patch's removals and the inexact places: every test
	// a change could touch is REFUSED on this engine (0016).
	Changes []Change
	// Refusals refuse the whole run on this engine. When there are any,
	// Start refuses too.
	Refusals []Refusal
	// Notes are things the user should know that don't change a result.
	Notes []string
}

// Refused says whether the whole run is refused on this engine.
func (l Lowering) Refused() bool { return len(l.Refusals) > 0 }

// PatchOp is one RFC 6902 operation. Note is ours, and RFC 6902 has
// receivers ignore members it doesn't define, so the patch stays valid.
type PatchOp struct {
	Op    string          `json:"op"`
	Path  string          `json:"path"`
	From  string          `json:"from,omitempty"`
	Value json.RawMessage `json:"value,omitempty"`
	Note  string          `json:"note,omitempty"`
}

// Rewrite is an exact change to the user's file.
type Rewrite struct {
	Pointer string `json:"pointer"`
	Was     string `json:"was"`
	Now     string `json:"now"`
	Reason  string `json:"reason"`
}

// ChangeKind says what a change did.
type ChangeKind string

// Kinds of change.
const (
	// Removed: the engine's copy leaves the place out.
	Removed ChangeKind = "removed"
	// Inexact: the place is kept, but the lab can't reproduce what it
	// means in production.
	Inexact ChangeKind = "inexact"
)

// Change is a place in the policy the engine's copy doesn't carry
// exactly. Every test whose source and destination it could touch is
// REFUSED on this engine, naming Capability.
type Change struct {
	Pointer    string     `json:"pointer"`
	Kind       ChangeKind `json:"kind"`
	Capability Capability `json:"capability"`
	Reason     string     `json:"reason"`
	// Src and Dst are the identities of the rule the change is in, as
	// written: a test whose source could match Src and whose destination
	// could match Dst is touched. Both empty with Targets empty means the
	// change touches no test.
	Src []string `json:"src,omitempty"`
	Dst []string `json:"dst,omitempty"`
	// Targets are identities whose devices the change touches whatever the
	// rule, such as a node attribute's targets. A test from or to one of
	// them is touched. "*" is every device.
	Targets []string `json:"targets,omitempty"`
}

// Refusal refuses a whole run on an engine.
type Refusal struct {
	Engine     string     `json:"engine"`
	Capability Capability `json:"capability,omitempty"`
	Pointer    string     `json:"pointer,omitempty"`
	Reason     string     `json:"reason"`
}

func (r *Refusal) Error() string {
	msg := "REFUSED on " + r.Engine + ": " + r.Reason
	if r.Pointer != "" {
		msg += " (at " + r.Pointer + ")"
	}
	return msg
}

// Err returns the refusals as one error, or nil.
func (l Lowering) Err() error {
	if len(l.Refusals) == 0 {
		return nil
	}
	errs := make([]error, len(l.Refusals))
	for i := range l.Refusals {
		errs[i] = &l.Refusals[i]
	}
	return errors.Join(errs...)
}

// ErrNotReady means the lab didn't become ready within its bound: a node's
// filter didn't show the applied policy, or a peer it expected was
// missing. The run stops with exit 3, and no probe starts (0016).
var ErrNotReady = errors.New("the lab didn't become ready")

// NotReadyError says which node wasn't ready, and why.
type NotReadyError struct {
	Node   string
	Reason string
}

func (e *NotReadyError) Error() string {
	return fmt.Sprintf("%v: node %s: %s", ErrNotReady, e.Node, e.Reason)
}

func (e *NotReadyError) Unwrap() error { return ErrNotReady }
