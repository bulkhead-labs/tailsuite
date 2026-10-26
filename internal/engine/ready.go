package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand/v2"
	"net/netip"
	"slices"
	"time"

	"github.com/tailscale/hujson"

	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// MarkerSource is the readiness marker's source: an address in TEST-NET-1
// (RFC 5737), outside the tailnet's range, which no node holds or can be
// given, on either engine. Both engines keep a rule from it as a rule of
// its own, beside an allow-all rule too (0021 for Tailscale; #5's
// integration test for headscale, which also tried a source inside the
// range).
const MarkerSource = "192.0.2.77/32"

// Marker ports are chosen in this range, one per apply.
const (
	markerPortMin = 41000
	markerPortMax = 61000
)

// allowAll is a new tailnet's default, made explicit: exact for a policy
// with neither an acls nor a grants section (0016).
const allowAll = `{"src": ["*"], "dst": ["*"], "ip": ["*"]}`

// MarkerNote labels the marker in the patch the user is shown.
const MarkerNote = "tailsuite's readiness marker: from an address no node holds, so it allows nothing"

// WithMarker adds the readiness marker for port to an engine's copy of the
// policy, and returns the new copy and the operations it took. The marker
// isn't part of Lower's patch, so Lower stays pure (0016).
//
// A policy with neither an acls nor a grants section gets its default made
// explicit first: both engines allow everything for it, and with the
// marker alone would allow only the marker. An empty section, such as
// "grants": [], denies everything on both engines, and gets no default
// (0021).
func WithMarker(src []byte, port uint16) ([]byte, []PatchOp, error) {
	f, err := policy.Parse(src)
	if err != nil {
		return nil, nil, err
	}
	marker, err := json.Marshal(map[string][]string{
		"src": {MarkerSource},
		"dst": {"*"},
		"ip":  {fmt.Sprintf("tcp:%d", port)},
	})
	if err != nil {
		return nil, nil, err
	}
	_, hasACLs := f.FilePointer("/acls")
	grants, hasGrants := f.FilePointer("/grants")
	// A section counts only as a list: null is read differently by
	// different engines, and Lower refuses it.
	if (hasACLs && f.Policy.ACLs == nil) || (hasGrants && f.Policy.Grants == nil) {
		return nil, nil, errors.New("the policy has an acls or grants section that isn't a list")
	}
	var ops []PatchOp
	switch {
	case hasGrants:
		ops = append(ops, PatchOp{Op: "add", Path: grants + "/-", Value: marker, Note: MarkerNote})
	case hasACLs:
		ops = append(ops, PatchOp{Op: "add", Path: "/grants", Value: json.RawMessage("[" + string(marker) + "]"), Note: MarkerNote})
	default:
		ops = append(ops,
			PatchOp{Op: "add", Path: "/grants", Value: json.RawMessage("[" + allowAll + "]"),
				Note: "the policy has neither acls nor grants, so it allows everything: that default, made explicit"},
			PatchOp{Op: "add", Path: "/grants/-", Value: marker, Note: MarkerNote})
	}
	out, err := ApplyPatch(src, ops)
	if err != nil {
		return nil, nil, err
	}
	return out, ops, nil
}

// ApplyPatch applies RFC 6902 operations to a HuJSON file with
// tailscale/hujson, which keeps comments and formatting elsewhere.
func ApplyPatch(src []byte, ops []PatchOp) ([]byte, error) {
	if len(ops) == 0 {
		return slices.Clone(src), nil
	}
	v, err := hujson.Parse(src)
	if err != nil {
		return nil, fmt.Errorf("patching the policy: %w", err)
	}
	b, err := json.Marshal(ops)
	if err != nil {
		return nil, err
	}
	if err := v.Patch(b); err != nil {
		return nil, fmt.Errorf("patching the policy: %w", err)
	}
	return v.Pack(), nil
}

// NewMarkerPort picks a port for the next apply's marker that no node's
// current filter has a marker on. So a wait can't be satisfied by an
// earlier apply's filter, even one made by another tailsuite process.
func NewMarkerPort(current []Filter) (uint16, error) {
	used := map[uint16]bool{}
	for _, f := range current {
		for _, r := range f.Rules {
			if isMarker(r) {
				for _, d := range r.DstPorts {
					for p := int(d.Ports.First); p <= int(d.Ports.Last); p++ {
						used[uint16(p)] = true //nolint:gosec // G115: bounded by Last, a uint16
					}
				}
			}
		}
	}
	var free []uint16
	for p := markerPortMin; p < markerPortMax; p++ {
		if !used[uint16(p)] {
			free = append(free, uint16(p))
		}
	}
	if len(free) == 0 {
		return 0, fmt.Errorf("no port left for the readiness marker: every port from %d to %d holds one", markerPortMin, markerPortMax-1)
	}
	return free[rand.IntN(len(free))], nil //nolint:gosec // G404: a port, not a secret
}

func isMarker(r FilterRule) bool {
	src := netip.MustParsePrefix(MarkerSource)
	return len(r.SrcIPs) == 1 && (r.SrcIPs[0] == src.Addr().String() || r.SrcIPs[0] == src.String())
}

// HasMarker says whether a filter holds the marker for port.
func (f Filter) HasMarker(port uint16) bool {
	return slices.ContainsFunc(f.Rules, func(r FilterRule) bool { return isMarker(r) && r.HasPort(port) })
}

// Wait is how long to wait for a lab to be ready, and how often to look.
type Wait struct {
	Bound time.Duration
	Poll  time.Duration
}

// WaitReady waits, bounded, until every node shows the apply whose
// marker is on port (0016):
//
//   - its compiled filter holds the marker;
//   - its peers include every node it should have: those its filter
//     admits traffic from, and those whose filters admit traffic from it.
//
// The peers are judged from the filters, once every filter shows this
// apply. Peers it has beyond those, such as through SSH rules, are fine.
// read returns a node's netmap; names are for errors. At the bound it
// returns a *NotReadyError.
func WaitReady(ctx context.Context, names []string, read func(ctx context.Context, i int) (Netmap, error), port uint16, w Wait) error {
	ctx, cancel := context.WithTimeout(ctx, w.Bound)
	defer cancel()
	var last *NotReadyError
	for {
		nm := make([]Netmap, len(names))
		last = nil
		for i := range names {
			m, err := read(ctx, i)
			switch {
			case err != nil:
				last = &NotReadyError{Node: names[i], Reason: err.Error()}
			case !m.Filter.HasMarker(port):
				last = &NotReadyError{Node: names[i], Reason: fmt.Sprintf("its filter doesn't hold the marker on port %d yet", port)}
			}
			if last != nil {
				break
			}
			nm[i] = m
		}
		if last == nil {
			last = missingPeer(names, nm)
		}
		if last == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			if errors.Is(context.Cause(ctx), context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
				last.Reason += fmt.Sprintf(" (waited %s)", w.Bound)
				return last
			}
			return ctx.Err()
		case <-time.After(w.Poll):
		}
	}
}

// missingPeer finds a node missing a peer its filters say it should have.
func missingPeer(names []string, nm []Netmap) *NotReadyError {
	admits := func(f Filter, addrs []netip.Addr) bool {
		return slices.ContainsFunc(f.Rules, func(r FilterRule) bool {
			return slices.ContainsFunc(addrs, r.SrcAdmits)
		})
	}
	for i := range nm {
		for j := range nm {
			if i == j {
				continue
			}
			if !admits(nm[i].Filter, nm[j].Self) && !admits(nm[j].Filter, nm[i].Self) {
				continue
			}
			has := slices.ContainsFunc(nm[i].Peers, func(p []netip.Addr) bool {
				return slices.ContainsFunc(p, func(a netip.Addr) bool { return slices.Contains(nm[j].Self, a) })
			})
			if !has {
				return &NotReadyError{Node: names[i], Reason: "its peers don't include " + names[j] + " yet"}
			}
		}
	}
	return nil
}
