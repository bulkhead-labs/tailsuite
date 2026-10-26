package suite

import (
	"fmt"
	"net/netip"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// Needs is what one device pair of a test needs (0016).
type Needs struct {
	// All is every capability the pair needs, sorted: the needs: labels.
	All []engine.Capability
	// Refused says why the pair is REFUSED on the engine: a need the
	// engine refuses, or a change in its copy of the policy that could
	// touch the pair. Empty means the pair can run.
	Refused []string
}

var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Planner computes needs against one engine's capabilities and lowering.
type Planner struct {
	t    model.Tailnet
	caps engine.Capabilities
	low  engine.Lowering
}

// NewPlanner computes needs for an engine.
func NewPlanner(t model.Tailnet, caps engine.Capabilities, low engine.Lowering) *Planner {
	return &Planner{t: t, caps: caps, low: low}
}

// Needs computes what a pair of a test needs. It always over-approximates
// (0016): every rule that could match the pair's source and destination
// counts, ignoring ports and conditions, and a selector it can't resolve
// matches every device.
func (pl *Planner) Needs(t Test, p Pair) Needs {
	need := map[engine.Capability]string{}
	add := func(c engine.Capability, why string) {
		if _, ok := need[c]; !ok {
			need[c] = why
		}
	}
	// The test's own.
	if t.SSH != "" {
		add(engine.SSH, "it's an SSH test")
		if t.Expect == Check {
			add(engine.SSHCheck, "it expects an SSH check")
		}
		if strings.HasPrefix(t.SSH, "localpart:") {
			add(engine.SSHLocalpart, "its login is tied to the source's login")
		}
	}
	if len(t.When) > 0 {
		add(engine.Posture, "it pins a device condition (when)")
	}

	// Every rule that could match the pair.
	if pl.t.Policy != nil {
		pol := &pl.t.Policy.Policy
		rules := pl.rules(pol, t.SSH != "")
		for _, r := range rules {
			if !pl.couldMatch(r.src, p.From) || !pl.couldMatch(r.dst, p.To) {
				continue
			}
			for c, why := range pl.ruleNeeds(r) {
				add(c, why)
			}
		}
	}

	var n Needs
	for c := range need {
		n.All = append(n.All, c)
	}
	slices.Sort(n.All)
	for _, c := range n.All {
		if pl.caps.Support(c) == engine.Refused {
			n.Refused = append(n.Refused, fmt.Sprintf("%s refuses %s: %s", pl.low.Engine, c, need[c]))
		}
	}
	// The engine's copy of the policy: every change that could touch the
	// pair refuses it, whatever the capability's row says.
	for _, c := range pl.low.Changes {
		if pl.changeTouches(c, p) {
			n.Refused = append(n.Refused, fmt.Sprintf("%s's copy of the policy %s %s (%s): %s", pl.low.Engine, verb(c.Kind), c.Pointer, c.Capability, c.Reason))
		}
	}
	return n
}

func verb(k engine.ChangeKind) string {
	if k == engine.Inexact {
		return "can't reproduce"
	}
	return "leaves out"
}

type rule struct {
	section string
	index   int
	src     []string
	dst     []string
	via     []string
	posture bool
	app     bool
}

func (pl *Planner) rules(p *policy.Policy, ssh bool) []rule {
	var out []rule
	for i, a := range p.ACLs {
		src := append(slices.Clone(a.Src), a.Users...)
		var dst []string
		for _, d := range append(slices.Clone(a.Dst), a.Ports...) {
			dst = append(dst, stripPort(d))
		}
		out = append(out, rule{section: "acls", index: i, src: src, dst: dst, posture: len(a.SrcPosture) > 0})
	}
	for i, g := range p.Grants {
		out = append(out, rule{section: "grants", index: i, src: g.Src, dst: g.Dst, via: g.Via, posture: len(g.SrcPosture) > 0, app: len(g.App) > 0})
	}
	if ssh {
		for i, s := range p.SSH {
			out = append(out, rule{section: "ssh", index: i, src: s.Src, dst: s.Dst, posture: len(s.SrcPosture) > 0})
		}
	}
	return out
}

// ruleNeeds is what a rule uses, by capability.
func (pl *Planner) ruleNeeds(r rule) map[engine.Capability]string {
	where := fmt.Sprintf("/%s/%d", r.section, r.index)
	out := map[engine.Capability]string{engine.GrantsACLs: "rule " + where}
	if r.section == "ssh" {
		out[engine.SSH] = "SSH rule " + where
	}
	for _, e := range append(append(slices.Clone(r.src), r.dst...), r.via...) {
		if c, ok := pl.entryNeed(e); ok {
			out[c] = fmt.Sprintf("rule %s names %s", where, e)
		}
	}
	if len(r.via) > 0 {
		out[engine.Routing] = fmt.Sprintf("rule %s routes via %s", where, strings.Join(r.via, ", "))
	}
	if r.posture {
		out[engine.Posture] = fmt.Sprintf("rule %s has a posture condition", where)
	}
	if r.app {
		out[engine.AppGrants] = fmt.Sprintf("rule %s grants application capabilities", where)
	}
	return out
}

func (pl *Planner) hosts() map[string]string {
	if pl.t.Policy == nil {
		return nil
	}
	return pl.t.Policy.Policy.Hosts
}

// entryNeed is the capability one rule entry needs, beyond the basics.
func (pl *Planner) entryNeed(e string) (engine.Capability, bool) {
	switch policy.KindOf(e, pl.hosts()) {
	case policy.User, policy.Group:
		return engine.UsersGroups, true
	case policy.IPSet:
		return engine.IPSets, true
	case policy.Autogroup:
		switch {
		case e == "autogroup:member" || e == "autogroup:self":
			return engine.AutogroupMember, true
		case e == "autogroup:tagged":
			return engine.AutogroupTagged, true
		case e == "autogroup:shared":
			return engine.AutogroupShared, true
		case e == "autogroup:internet":
			return engine.Routing, true
		case roleOf[e] != "":
			return engine.RoleAutogroups, true
		default:
			return engine.Capability("autogroup " + e), true // no row: refused everywhere
		}
	case policy.Host:
		return pl.addressNeed(pl.hosts()[e])
	case policy.Address:
		return pl.addressNeed(e)
	case policy.Wildcard, policy.Tag:
		return "", false
	default:
		return engine.Capability("selector " + e), true // unknown: no row, so refused
	}
}

// addressNeed: an address outside the tailnet's range is a route (M3);
// inside it, the engine's lowering says whether it's exact.
func (pl *Planner) addressNeed(a string) (engine.Capability, bool) {
	p, ok := asPrefix(a)
	if !ok {
		if lo, _, isRange := strings.Cut(a, "-"); isRange {
			p, ok = asPrefix(lo)
		}
	}
	if !ok {
		return engine.Capability("selector " + a), true
	}
	if p.Overlaps(tailnetV4) || p.Overlaps(tailnetV6) {
		return "", false
	}
	return engine.Routing, true
}

// couldMatch says whether a list of selectors could match a device. It
// over-approximates: what it can't resolve matches.
func (pl *Planner) couldMatch(entries []string, device string) bool {
	d, ok := pl.t.Device(device)
	if !ok {
		return true
	}
	tagged := len(d.Tags) > 0
	var groups map[string][]string
	if pl.t.Policy != nil {
		groups = pl.t.Policy.Policy.Groups
	}
	for _, e := range entries {
		switch policy.KindOf(e, pl.hosts()) {
		case policy.Wildcard:
			return true
		case policy.User:
			if !tagged && d.User == e {
				return true
			}
		case policy.Group:
			members, ok := groups[e]
			if !ok || (!tagged && slices.Contains(members, d.User)) {
				return true
			}
		case policy.Tag:
			if slices.Contains(d.Tags, e) {
				return true
			}
		case policy.Autogroup:
			switch {
			case e == "autogroup:member" || e == "autogroup:self":
				if !tagged {
					return true
				}
			case e == "autogroup:tagged":
				if tagged {
					return true
				}
			case e == "autogroup:internet":
				// The internet holds no lab device.
			case roleOf[e] != "":
				if !tagged && pl.mayHoldRole(d.User, roleOf[e]) {
					return true
				}
			default:
				return true
			}
		case policy.Host:
			if pl.addressCouldBe(pl.hosts()[e]) {
				return true
			}
		case policy.Address:
			if pl.addressCouldBe(e) {
				return true
			}
		default:
			return true // IP sets, and anything unknown
		}
	}
	return false
}

// mayHoldRole says whether a user could hold a role: they do in lab.yaml,
// or lab.yaml doesn't list their roles.
func (pl *Planner) mayHoldRole(login, role string) bool {
	for _, u := range pl.t.Users {
		if u.Login == login {
			return len(u.Roles) == 0 || slices.Contains(u.Roles, role)
		}
	}
	return true
}

// addressCouldBe: a lab device's address is somewhere in the tailnet's
// range, and the engine decides where, so any address there could be it.
func (pl *Planner) addressCouldBe(a string) bool {
	p, ok := asPrefix(a)
	if !ok {
		lo, hi, isRange := strings.Cut(a, "-")
		if !isRange {
			return true
		}
		l, err1 := netip.ParseAddr(lo)
		h, err2 := netip.ParseAddr(hi)
		if err1 != nil || err2 != nil {
			return true
		}
		return rangeOverlaps(l, h, tailnetV4) || rangeOverlaps(l, h, tailnetV6)
	}
	return p.Overlaps(tailnetV4) || p.Overlaps(tailnetV6)
}

// rangeOverlaps says whether the addresses lo to hi meet a prefix.
func rangeOverlaps(lo, hi netip.Addr, p netip.Prefix) bool {
	first := p.Masked().Addr()
	if lo.BitLen() != first.BitLen() || hi.BitLen() != first.BitLen() {
		return false
	}
	return lo.Compare(lastAddr(p)) <= 0 && first.Compare(hi) <= 0
}

// lastAddr is a prefix's last address.
func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().AsSlice()
	for i := range a {
		for b := 0; b < 8; b++ {
			if i*8+b >= p.Bits() {
				a[i] |= 0x80 >> b
			}
		}
	}
	out, _ := netip.AddrFromSlice(a)
	return out
}

func (pl *Planner) changeTouches(c engine.Change, p Pair) bool {
	if len(c.Targets) > 0 {
		return slices.Contains(c.Targets, "*") || pl.couldMatch(c.Targets, p.From) || pl.couldMatch(c.Targets, p.To)
	}
	if len(c.Src) == 0 && len(c.Dst) == 0 {
		return false
	}
	return pl.couldMatch(c.Src, p.From) && pl.couldMatch(c.Dst, p.To)
}

// stripPort drops a host:port entry's port.
func stripPort(v string) string {
	if strings.HasPrefix(v, "[") {
		if end := strings.Index(v, "]"); end > 0 {
			return v[1:end]
		}
	}
	if _, err := netip.ParseAddr(v); err == nil {
		return v
	}
	if i := strings.LastIndex(v, ":"); i >= 0 {
		return v[:i]
	}
	return v
}
