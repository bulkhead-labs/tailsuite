package headscale

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"slices"
	"strconv"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// LabAdmin owns, in headscale's copy, every tag a role autogroup owned.
// Tags come from tofu/ and the engine, never from a device asking, so a
// tag's owners can't change who reaches what. The .invalid domain can't be
// anyone's login (RFC 2606).
const LabAdmin = "lab-admin@tailsuite.invalid"

// roleAutogroups maps each role autogroup to the lab.yaml role it means.
// autogroup:member isn't one: it's every user, whatever their role.
var roleAutogroups = map[string]string{
	"autogroup:owner":         "owner",
	"autogroup:admin":         "admin",
	"autogroup:it-admin":      "it-admin",
	"autogroup:network-admin": "network-admin",
	"autogroup:billing-admin": "billing-admin",
	"autogroup:auditor":       "auditor",
}

// The tailnet's address ranges. A rule that names an address inside one,
// short of the whole range, names devices by address, which the lab
// can't reproduce on headscale (PinnedIPv4, PinnedIPv6).
var (
	tailnetV4 = netip.MustParsePrefix("100.64.0.0/10")
	tailnetV6 = netip.MustParsePrefix("fd7a:115c:a1e0::/48")
)

// Lower makes headscale's copy of the policy (0014, 0016). headscale
// rejects a whole policy over any key or feature it lacks, so its copy
// leaves out what it can't take, and records each removal; the tests a
// removal could touch are REFUSED. Users and groups are native: headscale
// matches a login against a user's email.
func (e *Engine) Lower(t model.Tailnet) (engine.Lowering, error) {
	return Lower(t)
}

// Lower is the engine's Lower, without an engine.
func Lower(t model.Tailnet) (engine.Lowering, error) {
	if t.Policy == nil {
		return engine.Lowering{}, errors.New("headscale: the model has no policy")
	}
	l := &lowerer{f: t.Policy, p: &t.Policy.Policy, out: engine.Lowering{Engine: Name}}
	l.roles = rolesOf(t.Users)

	for _, u := range l.f.Unknown {
		l.refuse("", u.Pointer, fmt.Sprintf("headscale rejects any policy key it doesn't know, and this one is %s", u.Reason))
	}
	switch {
	case len(l.p.DefaultSrcPosture) > 0:
		l.refuse(engine.Posture, l.ptr("/defaultSrcPosture"), "defaultSrcPosture applies to every rule, and headscale has no posture")
	case l.has("/defaultSrcPosture"):
		l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr("/defaultSrcPosture"), Was: "defaultSrcPosture: []", Now: "(removed)", Reason: "an empty list changes nothing, and headscale doesn't read the key"})
		l.remove("/defaultSrcPosture", "an empty list changes nothing")
	}
	// A section written as null: headscale reads it as absent, so a policy
	// with no other section allows everything there. Tailscale's reading
	// isn't checked, and the marker would make the lab deny everything.
	if l.has("/acls") && l.p.ACLs == nil {
		l.refuse("", l.ptr("/acls"), "acls is null, which no engine's reading has been checked for; write [] to deny everything, or leave the section out")
	}
	if l.has("/grants") && l.p.Grants == nil {
		l.refuse("", l.ptr("/grants"), "grants is null, which no engine's reading has been checked for; write [] to deny everything, or leave the section out")
	}
	l.markerSource()
	if l.p.DisableIPv4 {
		l.refuse(engine.IPv4Disabled, l.ptr("/disableIPv4"), "the tailnet has IPv4 disabled, and the lab's probes are IPv4")
	}

	before := len(l.p.ACLs) + len(l.p.Grants)
	removed, dropped := 0, 0
	for _, section := range []string{"acls", "grants"} {
		n := len(l.p.ACLs)
		if section == "grants" {
			n = len(l.p.Grants)
		}
		for i := n - 1; i >= 0; i-- {
			switch l.rule(section, i) {
			case ruleDropped:
				dropped++
				removed++
			case ruleGone:
				removed++
			}
		}
	}
	for i := len(l.p.SSH) - 1; i >= 0; i-- {
		l.rule("ssh", i)
	}
	// Rules removed because they match no lab device leave the lab denying
	// what production denies too. Rules removed because headscale can't do
	// them don't: with none left, the lab would deny everything, which
	// isn't the policy (0016).
	if before > 0 && removed == before && dropped > 0 {
		l.refuse("", "", "every rule in acls and grants is left out of headscale's copy, some because they need what headscale can't do; with none left, headscale would deny everything, which isn't the policy")
	}

	for _, d := range t.Devices {
		if d.IPv4 != "" {
			l.out.Notes = append(l.out.Notes, fmt.Sprintf("device %s's pinned address %s isn't honoured on headscale until #67; it gets whatever headscale assigns", d.Name, d.IPv4))
		}
	}
	l.tagOwners()
	l.autoApprovers()
	l.sections()

	if err := l.finish(); err != nil {
		return engine.Lowering{}, err
	}
	return l.out, nil
}

type lowerer struct {
	f     *policy.File
	p     *policy.Policy
	roles roleIndex
	ops   []engine.PatchOp
	out   engine.Lowering
}

func (l *lowerer) ptr(canonical string) string {
	if p, ok := l.f.FilePointer(canonical); ok {
		return p
	}
	return canonical
}

func (l *lowerer) has(canonical string) bool {
	_, ok := l.f.FilePointer(canonical)
	return ok
}

func (l *lowerer) refuse(c engine.Capability, ptr, reason string) {
	l.out.Refusals = append(l.out.Refusals, engine.Refusal{Engine: Name, Capability: c, Pointer: ptr, Reason: reason})
}

func (l *lowerer) op(op engine.PatchOp) { l.ops = append(l.ops, op) }

func (l *lowerer) remove(canonical, note string) {
	l.op(engine.PatchOp{Op: "remove", Path: l.ptr(canonical), Note: note})
}

func (l *lowerer) replaceList(canonical string, values []string, note string) {
	b, _ := json.Marshal(values) // strings never fail
	l.op(engine.PatchOp{Op: "replace", Path: l.ptr(canonical), Value: b, Note: note})
}

// ruleLists are a rule's lists of identities, by section: the canonical
// field name, whether it holds host:port entries, and whether it's a
// source.
type ruleList struct {
	field    string
	values   []string
	hostPort bool
	src      bool
}

func (l *lowerer) lists(section string, i int) []ruleList {
	switch section {
	case "acls":
		r := l.p.ACLs[i]
		return []ruleList{{"src", r.Src, false, true}, {"users", r.Users, false, true}, {"dst", r.Dst, true, false}, {"ports", r.Ports, true, false}}
	case "grants":
		r := l.p.Grants[i]
		return []ruleList{{"src", r.Src, false, true}, {"dst", r.Dst, false, false}, {"via", r.Via, false, false}}
	default:
		r := l.p.SSH[i]
		return []ruleList{{"src", r.Src, false, true}, {"dst", r.Dst, false, false}}
	}
}

// What rule did with a rule.
type ruleOutcome int

const (
	ruleKept    ruleOutcome = iota
	ruleGone                // removed exactly: it matches no lab device
	ruleDropped             // removed because headscale can't do it: its tests are REFUSED
)

// rule lowers one rule. Rules are visited from the last to the first, so
// removing one doesn't move the pointers of the rules still to come.
func (l *lowerer) rule(section string, i int) ruleOutcome {
	base := "/" + section + "/" + strconv.Itoa(i)
	lists := l.lists(section, i)
	var src, dst []string
	for _, rl := range lists {
		for _, v := range rl.values {
			if rl.hostPort {
				v = stripPort(v)
			}
			// headscale decides which lab device holds an address in the
			// tailnet's range, so such an address could be any of them.
			if _, ok := l.tailnetAddress(v); ok {
				v = "*"
			}
			if rl.src {
				src = append(src, v)
			} else if rl.field != "via" {
				dst = append(dst, v)
			}
		}
	}
	change := func(kind engine.ChangeKind, c engine.Capability, ptr, reason string) {
		l.out.Changes = append(l.out.Changes, engine.Change{Pointer: l.ptr(ptr), Kind: kind, Capability: c, Reason: reason, Src: src, Dst: dst})
	}
	drop := func(c engine.Capability, reason string) ruleOutcome {
		change(engine.Removed, c, base, reason)
		l.remove(base, "headscale's copy leaves this rule out: "+reason)
		return ruleDropped
	}

	// Whole-rule removals first: then nothing else in the rule matters.
	for _, rl := range lists {
		for _, v := range rl.values {
			name := v
			if rl.hostPort {
				name = stripPort(v)
			}
			switch {
			case strings.HasPrefix(name, "ipset:"):
				return drop(engine.IPSets, "it names an IP set ("+name+"), which headscale has no section for")
			case name == "autogroup:shared":
				return drop(engine.AutogroupShared, "it names autogroup:shared, which headscale doesn't have")
			}
		}
	}

	// Role autogroups, list by list.
	type edit struct {
		field  string
		values []string
	}
	var edits []edit
	for _, rl := range lists {
		out, changed, reason := l.lowerRoles(base+"/"+rl.field, rl.values, rl.hostPort)
		if reason != "" {
			return drop(engine.RoleAutogroups, reason)
		}
		if changed {
			edits = append(edits, edit{rl.field, out})
		}
	}
	// A list emptied by roles no lab user holds makes the rule match
	// nothing in the lab, so the rule goes, and that's exact.
	for _, e := range edits {
		if len(e.values) == 0 && e.field != "via" {
			l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr(base), Was: "the rule", Now: "(removed)",
				Reason: "its " + e.field + " names only roles that no lab user holds, so it matches no lab device"})
			l.remove(base, "matches no lab device: its "+e.field+" names only roles no lab user holds")
			return ruleGone
		}
	}
	for _, e := range edits {
		l.replaceList(base+"/"+e.field, e.values, "role autogroups lowered to the users lab.yaml gives those roles")
	}

	// Conditions and keys headscale lacks: removed, not the rule (0016).
	switch section {
	case "grants":
		l.srcPosture(base, len(l.p.Grants[i].SrcPosture), change)
		if len(l.p.Grants[i].App) > 0 {
			change(engine.Inexact, engine.AppGrants, base+"/app", "the lab doesn't exercise application capabilities")
		}
	case "acls":
		l.srcPosture(base, len(l.p.ACLs[i].SrcPosture), change)
	case "ssh":
		r := l.p.SSH[i]
		l.srcPosture(base, len(r.SrcPosture), change)
		if l.has(base + "/enforceRecorder") {
			change(engine.Removed, engine.SSHRecording, base+"/enforceRecorder", "headscale has no session recording")
			l.remove(base+"/enforceRecorder", "headscale has no session recording")
		}
		if l.has(base + "/recorder") {
			change(engine.Removed, engine.SSHRecording, base+"/recorder", "headscale has no session recording")
			l.remove(base+"/recorder", "headscale has no session recording")
		}
	}

	// Addresses in the tailnet's range name devices by address.
	for _, rl := range lists {
		for j, v := range rl.values {
			name := v
			if rl.hostPort {
				name = stripPort(v)
			}
			if c, ok := l.tailnetAddress(name); ok {
				change(engine.Inexact, c, base+"/"+rl.field+"/"+strconv.Itoa(j),
					"it names "+name+", inside the tailnet's range, and headscale can't give a lab device the address production has (#67)")
			}
		}
	}

	// The legacy spellings headscale doesn't read: users for src, ports
	// for dst. Moved, which is exact.
	if section == "acls" {
		for _, mv := range [][2]string{{"users", "src"}, {"ports", "dst"}} {
			if !l.has(base + "/" + mv[0]) {
				continue
			}
			if l.has(base + "/" + mv[1]) {
				l.refuse("", l.ptr(base), "the rule has both "+mv[0]+" and "+mv[1])
				continue
			}
			from := l.ptr(base + "/" + mv[0])
			to := from[:strings.LastIndex(from, "/")+1] + mv[1]
			l.op(engine.PatchOp{Op: "move", From: from, Path: to, Note: "headscale reads only " + mv[1]})
			l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: from, Was: mv[0], Now: mv[1], Reason: "the legacy spelling, which headscale doesn't read"})
		}
	}
	return ruleKept
}

// srcPosture removes a rule's posture condition: the condition, not the
// rule (0016). An empty list changes nothing, so its removal is exact.
func (l *lowerer) srcPosture(base string, n int, change func(engine.ChangeKind, engine.Capability, string, string)) {
	switch {
	case n > 0:
		change(engine.Removed, engine.Posture, base+"/srcPosture", "headscale has no posture")
		l.remove(base+"/srcPosture", "headscale has no posture; the tests this rule could touch are REFUSED")
	case l.has(base + "/srcPosture"):
		l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr(base + "/srcPosture"), Was: "srcPosture: []", Now: "(removed)", Reason: "an empty list changes nothing, and headscale doesn't read the key"})
		l.remove(base+"/srcPosture", "an empty list changes nothing")
	}
}

// markerSource refuses a policy whose rules name the readiness marker's
// source address: the wait couldn't tell such a rule from the marker.
func (l *lowerer) markerSource() {
	marker := netip.MustParsePrefix(engine.MarkerSource).Addr()
	for _, r := range l.f.References() {
		if r.Role != "src" && r.Role != "dst" {
			continue
		}
		a := r.Value
		if h, ok := l.p.Hosts[a]; ok {
			a = h
		}
		var p netip.Prefix
		if ad, err := netip.ParseAddr(a); err == nil {
			p = netip.PrefixFrom(ad, ad.BitLen())
		} else if q, err := netip.ParsePrefix(a); err == nil {
			p = q
		} else {
			continue
		}
		if p.Contains(marker) {
			l.refuse("", r.Pointer, "the rule names "+r.Value+", which holds "+marker.String()+", tailsuite's readiness marker's source; the lab couldn't tell the rule from the marker")
		}
	}
}

// tailnetAddress says whether a name is, or is a host for, an address or
// prefix inside a tailnet range, short of the whole range.
func (l *lowerer) tailnetAddress(name string) (engine.Capability, bool) {
	if a, ok := l.p.Hosts[name]; ok {
		name = a
	}
	var p netip.Prefix
	if a, err := netip.ParseAddr(name); err == nil {
		p = netip.PrefixFrom(a, a.BitLen())
	} else if q, err := netip.ParsePrefix(name); err == nil {
		p = q.Masked()
	} else if lo, hi, ok := strings.Cut(name, "-"); ok {
		a, err1 := netip.ParseAddr(lo)
		b, err2 := netip.ParseAddr(hi)
		if err1 != nil || err2 != nil {
			return "", false
		}
		for _, r := range []netip.Prefix{tailnetV4, tailnetV6} {
			if a.Compare(r.Addr()) <= 0 && lastAddr(r).Compare(b) <= 0 {
				continue // the range covers the whole tailnet range
			}
			if a.Compare(lastAddr(r)) <= 0 && r.Addr().Compare(b) <= 0 {
				return capFor(r), true
			}
		}
		return "", false
	} else {
		return "", false
	}
	for _, r := range []netip.Prefix{tailnetV4, tailnetV6} {
		if p.Overlaps(r) && (p.Bits() > r.Bits() || !p.Contains(r.Addr())) {
			return capFor(r), true
		}
	}
	return "", false
}

func capFor(r netip.Prefix) engine.Capability {
	if r.Addr().Is4() {
		return engine.PinnedIPv4
	}
	return engine.PinnedIPv6
}

func lastAddr(p netip.Prefix) netip.Addr {
	a := p.Masked().Addr().AsSlice()
	bits := p.Bits()
	for i := range a {
		for b := 0; b < 8; b++ {
			if i*8+b >= bits {
				a[i] |= 0x80 >> b
			}
		}
	}
	out, _ := netip.AddrFromSlice(a)
	return out
}

// roleIndex is which users hold each role, and whether every user has
// their roles listed, without which no role autogroup can be lowered
// exactly.
type roleIndex struct {
	users    map[string][]string // role → logins, sorted
	complete bool
	owners   bool // some user is an owner without also being an admin
}

func rolesOf(users []model.User) roleIndex {
	ri := roleIndex{users: map[string][]string{}, complete: len(users) > 0}
	for _, u := range users {
		if len(u.Roles) == 0 {
			ri.complete = false
		}
		for _, r := range u.Roles {
			ri.users[r] = append(ri.users[r], u.Login)
		}
		if slices.Contains(u.Roles, "owner") && !slices.Contains(u.Roles, "admin") {
			ri.owners = true
		}
	}
	for r := range ri.users {
		slices.Sort(ri.users[r])
	}
	return ri
}

// lowerRoles replaces each role autogroup in a list with the users that
// hold the role. It says why it can't, when it can't do it exactly.
func (l *lowerer) lowerRoles(canonical string, values []string, hostPort bool) (out []string, changed bool, cant string) {
	for j, v := range values {
		name, port := v, ""
		if hostPort {
			name = stripPort(v)
			port = strings.TrimPrefix(v, name)
		}
		role, ok := roleAutogroups[name]
		if !ok {
			out = append(out, v)
			continue
		}
		changed = true
		switch {
		case !l.roles.complete:
			return nil, false, "it names " + name + ", and lab.yaml doesn't list every user's roles, so the lab can't tell who holds that role"
		case name == "autogroup:admin" && l.roles.owners:
			return nil, false, "it names autogroup:admin, and a lab user is an owner: Tailscale's docs don't say whether autogroup:admin includes the owner"
		}
		for _, u := range l.roles.users[role] {
			out = append(out, u+port)
		}
		l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr(canonical) + "/" + strconv.Itoa(j), Was: name, Now: strings.Join(l.roles.users[role], ", "),
			Reason: "headscale has no role autogroups: the users lab.yaml gives the " + role + " role"})
	}
	return out, changed, ""
}

// tagOwners lowers role autogroups as tag owners to the lab's admin.
func (l *lowerer) tagOwners() {
	for _, tag := range sortedKeys(l.p.TagOwners) {
		owners := l.p.TagOwners[tag]
		var out []string
		changed := false
		for _, o := range owners {
			if _, ok := roleAutogroups[o]; ok {
				changed = true
				if !slices.Contains(out, LabAdmin) {
					out = append(out, LabAdmin)
				}
				continue
			}
			out = append(out, o)
		}
		if !changed {
			continue
		}
		canonical := "/tagOwners/" + escape(tag)
		l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr(canonical), Was: strings.Join(owners, ", "), Now: strings.Join(out, ", "),
			Reason: "headscale takes no role autogroup as a tag owner; tags come from tofu/, so a tag's owners can't change who reaches what"})
		l.replaceList(canonical, out, "role autogroups as tag owners, lowered to the lab's admin")
	}
}

// autoApprovers lowers role autogroups among route approvers to users,
// and drops the services headscale doesn't have. Routes are M3, so these
// only keep headscale's copy valid.
func (l *lowerer) autoApprovers() {
	lower := func(canonical string, values []string) {
		out, changed, cant := l.lowerRoles(canonical, values, false)
		switch {
		case cant != "":
			l.out.Changes = append(l.out.Changes, engine.Change{Pointer: l.ptr(canonical), Kind: engine.Removed, Capability: engine.Routing, Reason: cant})
			l.remove(canonical, cant)
		case changed:
			l.replaceList(canonical, out, "role autogroups lowered to the users lab.yaml gives those roles")
		}
	}
	for _, route := range sortedKeys(l.p.AutoApprovers.Routes) {
		lower("/autoApprovers/routes/"+escape(route), l.p.AutoApprovers.Routes[route])
	}
	if l.has("/autoApprovers/exitNode") {
		lower("/autoApprovers/exitNode", l.p.AutoApprovers.ExitNode)
		// A removed approver names no identities, so it refuses no test:
		// true while routes are refused (M3). When routes are built, it
		// must name the approvers' devices.
	}
	if l.has("/autoApprovers/services") {
		l.out.Changes = append(l.out.Changes, engine.Change{Pointer: l.ptr("/autoApprovers/services"), Kind: engine.Removed, Capability: engine.Services, Reason: "headscale has no services"})
		l.remove("/autoApprovers/services", "headscale has no services")
	}
}

// sections removes the top-level sections headscale doesn't take.
func (l *lowerer) sections() {
	refsUnder := func(canonical string) []string {
		var out []string
		prefix := l.ptr(canonical) + "/"
		for _, r := range l.f.References() {
			if strings.HasPrefix(r.Pointer, prefix) && r.Role != "defines" {
				out = append(out, r.Value)
			}
		}
		return out
	}
	type section struct {
		canonical string
		c         engine.Capability
		reason    string
		touches   bool // it touches the devices its references name
	}
	for _, s := range []section{
		{"/tests", engine.PolicyTests, "tailsuite runs the policy's own tests itself, with real traffic (0015)", false},
		{"/sshTests", engine.PolicyTests, "tailsuite runs the policy's own tests itself, with real traffic (0015)", false},
		{"/ipsets", engine.IPSets, "headscale has no IP sets; the rules that name one are left out too", false},
		{"/postures", engine.Posture, "headscale has no posture; the conditions that name one are left out too", false},
		{"/attrConfig", engine.AttrConfig, "headscale has no custom attributes", true},
		{"/externalTailnets", engine.ExternalTailnets, "headscale has no external tailnets", true},
	} {
		if !l.has(s.canonical) {
			continue
		}
		c := engine.Change{Pointer: l.ptr(s.canonical), Kind: engine.Removed, Capability: s.c, Reason: s.reason}
		if s.touches {
			c.Targets = refsUnder(s.canonical)
		}
		l.out.Changes = append(l.out.Changes, c)
		l.remove(s.canonical, s.reason)
	}
	if l.has("/nodeAttrs") {
		for i, a := range l.p.NodeAttrs {
			c := engine.NodeAttrs
			if slices.Contains(a.Attr, "funnel") {
				c = engine.Funnel
			}
			l.out.Changes = append(l.out.Changes, engine.Change{Pointer: l.ptr("/nodeAttrs/" + strconv.Itoa(i)), Kind: engine.Removed, Capability: c,
				Reason: "node attributes aren't built yet on headscale", Targets: a.Target})
		}
		l.remove("/nodeAttrs", "node attributes aren't built yet on headscale")
	}
	// Settings that don't change who reaches what: removed, exactly.
	for _, s := range []struct{ canonical, reason string }{
		{"/derpMap", "the lab uses headscale's own DERP server"},
		{"/oneCGNATRoute", "a client routing preference, the same for every device in the lab"},
		{"/disableIPv4", "false is the default"},
	} {
		if !l.has(s.canonical) {
			continue
		}
		l.out.Rewrites = append(l.out.Rewrites, engine.Rewrite{Pointer: l.ptr(s.canonical), Was: s.canonical[1:], Now: "(removed)", Reason: s.reason})
		l.remove(s.canonical, s.reason)
	}
}

// finish applies the patch, and checks that the copy still parses.
func (l *lowerer) finish() error {
	if l.out.Refused() {
		l.ops = nil
		l.out.Policy = nil
		return nil
	}
	l.out.Patch = l.ops
	out, err := engine.ApplyPatch(l.f.Raw, l.ops)
	if err != nil {
		return fmt.Errorf("headscale: lowering: %w", err)
	}
	if _, err := policy.Parse(out); err != nil {
		return fmt.Errorf("headscale: lowering made a policy that doesn't parse: %w", err)
	}
	l.out.Policy = out
	return nil
}

// stripPort drops a host:port entry's port. It mirrors the policy
// package's, which a test checks.
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

func escape(key string) string {
	return strings.ReplaceAll(strings.ReplaceAll(key, "~", "~0"), "/", "~1")
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
