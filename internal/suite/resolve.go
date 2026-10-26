package suite

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/model"
)

// roleOf maps each role autogroup to the lab.yaml role it means.
var roleOf = map[string]string{
	"autogroup:owner":         "owner",
	"autogroup:admin":         "admin",
	"autogroup:it-admin":      "it-admin",
	"autogroup:network-admin": "network-admin",
	"autogroup:billing-admin": "billing-admin",
	"autogroup:auditor":       "auditor",
}

// Resolver turns a test's from and to into lab devices.
type Resolver struct {
	t model.Tailnet
}

// NewResolver resolves against a model.
func NewResolver(t model.Tailnet) *Resolver { return &Resolver{t: t} }

// A Match is the devices an identity names. Self is set for
// autogroup:self, which a destination may name: each source then pairs
// only with its own user's devices.
type Match struct {
	Devices []string
	Self    bool
}

// ResolveError says why an identity names no lab device.
type ResolveError struct {
	Identity string
	Reason   string
}

func (e *ResolveError) Error() string { return e.Identity + ": " + e.Reason }

// Resolve returns the lab devices an identity names: a device by name, or
// every device the identity matches (0015).
func (r *Resolver) Resolve(id string) (Match, error) {
	t := r.t
	var hosts map[string]string
	var groups map[string][]string
	if t.Policy != nil {
		hosts, groups = t.Policy.Policy.Hosts, t.Policy.Policy.Groups
	}
	if d, ok := t.Device(id); ok {
		if a, isHost := hosts[id]; isHost && a != d.IPv4 {
			return Match{}, &ResolveError{id, fmt.Sprintf("is both a device and a host (%s) whose address isn't the device's", a)}
		}
		return Match{Devices: []string{d.Name}}, nil
	}
	pick := func(keep func(model.Device) bool) Match {
		var m Match
		for _, d := range t.Devices {
			if keep(d) {
				m.Devices = append(m.Devices, d.Name)
			}
		}
		return m
	}
	tagged := func(d model.Device) bool { return len(d.Tags) > 0 }
	ownedBy := func(logins ...string) func(model.Device) bool {
		return func(d model.Device) bool { return !tagged(d) && slices.Contains(logins, d.User) }
	}
	var m Match
	switch {
	case id == "*":
		m = pick(func(model.Device) bool { return true })
	case strings.HasPrefix(id, "group:"):
		members, ok := groups[id]
		if !ok {
			return Match{}, &ResolveError{id, "isn't a group in the policy"}
		}
		m = pick(ownedBy(members...))
	case strings.HasPrefix(id, "tag:"):
		m = pick(func(d model.Device) bool { return slices.Contains(d.Tags, id) })
	case id == "autogroup:member":
		m = pick(func(d model.Device) bool { return !tagged(d) })
	case id == "autogroup:tagged":
		m = pick(tagged)
	case id == "autogroup:self":
		m = pick(func(d model.Device) bool { return !tagged(d) })
		m.Self = true
	case roleOf[id] != "":
		var logins []string
		for _, u := range t.Users {
			if slices.Contains(u.Roles, roleOf[id]) {
				logins = append(logins, u.Login)
			}
		}
		m = pick(ownedBy(logins...))
	case strings.HasPrefix(id, "autogroup:"):
		return Match{}, &ResolveError{id, "names no device the lab can build"}
	case strings.Contains(id, "@"):
		m = pick(ownedBy(id))
	default:
		addr := id
		if a, ok := hosts[id]; ok {
			addr = a
		}
		p, ok := asPrefix(addr)
		if !ok {
			return Match{}, &ResolveError{id, "isn't a device in lab.yaml, or an identity the policy uses"}
		}
		m = pick(func(d model.Device) bool {
			a, err := netip.ParseAddr(d.IPv4)
			return err == nil && p.Contains(a)
		})
	}
	if len(m.Devices) == 0 {
		return Match{}, &ResolveError{id, "matches no device in the lab"}
	}
	return m, nil
}

func asPrefix(s string) (netip.Prefix, bool) {
	if a, err := netip.ParseAddr(s); err == nil {
		return netip.PrefixFrom(a, a.BitLen()), true
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Masked(), true
	}
	return netip.Prefix{}, false
}

// Pair is one source device and one destination device.
type Pair struct {
	From string `json:"from"`
	To   string `json:"to"`
}

// Pairs expands a test's from and to into device pairs. A device is never
// paired with itself: its traffic to itself never meets a filter.
func (r *Resolver) Pairs(from, to Match) []Pair {
	var out []Pair
	for _, f := range from.Devices {
		fd, _ := r.t.Device(f)
		for _, d := range to.Devices {
			if f == d {
				continue
			}
			if to.Self {
				td, _ := r.t.Device(d)
				if len(fd.Tags) > 0 || fd.User != td.User {
					continue
				}
			}
			out = append(out, Pair{From: f, To: d})
		}
	}
	return out
}
