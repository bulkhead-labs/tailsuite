package policy

import (
	"net/netip"
	"slices"
	"strconv"
	"strings"
)

// Kind is what a reference names.
type Kind string

// Kinds of reference.
const (
	User      Kind = "user"      // alice@example.com, or alice@ for a domain-less login
	Group     Kind = "group"     // group:eng
	Tag       Kind = "tag"       // tag:prod
	Autogroup Kind = "autogroup" // autogroup:member
	Host      Kind = "host"      // a name from the hosts section
	IPSet     Kind = "ipset"     // ipset:office
	Address   Kind = "address"   // an IP address, prefix or range
	Wildcard  Kind = "wildcard"  // *
	Other     Kind = "other"     // anything else, such as a service: or a posture
)

// Ref is one place in the policy that names a user, group, tag, host or
// address: where the rewrites in 0014 have to look.
type Ref struct {
	// Pointer is where the string is, in the file's own spelling.
	Pointer string
	// Value is the name, without any :port.
	Value string
	Kind  Kind
	// Role says what the place is for: src, dst, via, owner, member,
	// approver, target, recorder, test-src or broadcast.
	Role string
}

// References lists every reference in the policy, in a stable order. It
// knows every place Tailscale's policy reference lets a user, group or tag
// appear. Two kinds of place aren't covered, and both are in Unknown,
// which makes 0014 refuse an identity rewrite: places inside keys the
// typed copy doesn't understand, and places under a key whose exact name
// is written twice, which no pointer can address.
func (f *File) References() []Ref {
	var refs []Ref
	add := func(canonical, value, role string) {
		ptr, ok := f.FilePointer(canonical)
		if !ok {
			return // the field's absent from the file
		}
		refs = append(refs, Ref{Pointer: ptr, Value: value, Kind: KindOf(value, f.Policy.Hosts), Role: role})
	}
	list := func(base string, values []string, role string, hostPort bool) {
		for i, v := range values {
			if hostPort {
				v = stripPort(v)
			}
			add(base+"/"+strconv.Itoa(i), v, role)
		}
	}
	p := &f.Policy
	for i, r := range p.ACLs {
		b := "/acls/" + strconv.Itoa(i)
		list(b+"/src", r.Src, "src", false)
		list(b+"/users", r.Users, "src", false)
		list(b+"/dst", r.Dst, "dst", true)
		list(b+"/ports", r.Ports, "dst", true)
	}
	for i, r := range p.Grants {
		b := "/grants/" + strconv.Itoa(i)
		list(b+"/src", r.Src, "src", false)
		list(b+"/dst", r.Dst, "dst", false)
		list(b+"/via", r.Via, "via", false)
	}
	for i, r := range p.SSH {
		b := "/ssh/" + strconv.Itoa(i)
		list(b+"/src", r.Src, "src", false)
		list(b+"/dst", r.Dst, "dst", false)
		list(b+"/recorder", r.Recorder, "recorder", false)
	}
	for _, name := range sortedKeys(p.Groups) {
		list("/groups/"+escape(name), p.Groups[name], "member", false)
	}
	for _, tag := range sortedKeys(p.TagOwners) {
		list("/tagOwners/"+escape(tag), p.TagOwners[tag], "owner", false)
	}
	for _, route := range sortedKeys(p.AutoApprovers.Routes) {
		list("/autoApprovers/routes/"+escape(route), p.AutoApprovers.Routes[route], "approver", false)
	}
	list("/autoApprovers/exitNode", p.AutoApprovers.ExitNode, "approver", false)
	for _, svc := range sortedKeys(p.AutoApprovers.Services) {
		list("/autoApprovers/services/"+escape(svc), p.AutoApprovers.Services[svc], "approver", false)
	}
	for i, a := range p.NodeAttrs {
		list("/nodeAttrs/"+strconv.Itoa(i)+"/target", a.Target, "target", false)
	}
	for i, t := range p.Tests {
		b := "/tests/" + strconv.Itoa(i)
		if t.Src != "" {
			add(b+"/src", t.Src, "test-src")
		}
		if t.User != "" {
			add(b+"/user", t.User, "test-src")
		}
		list(b+"/accept", t.Accept, "dst", true)
		list(b+"/allow", t.Allow, "dst", true)
		list(b+"/deny", t.Deny, "dst", true)
	}
	for i, t := range p.SSHTests {
		b := "/sshTests/" + strconv.Itoa(i)
		if t.Src != "" {
			add(b+"/src", t.Src, "test-src")
		}
		list(b+"/dst", t.Dst, "dst", false)
	}
	for _, name := range sortedKeys(p.AttrConfig) {
		list("/attrConfig/"+escape(name)+"/broadcastToPeers", p.AttrConfig[name].BroadcastToPeers, "broadcast", false)
	}
	return refs
}

// Identities is References limited to users, groups, tags and autogroups:
// what a rewrite of identities, such as users to tags (#6), has to find.
func (f *File) Identities() []Ref {
	var out []Ref
	for _, r := range f.References() {
		switch r.Kind {
		case User, Group, Tag, Autogroup:
			out = append(out, r)
		}
	}
	return out
}

// KindOf classifies a name as the policy reference does. hosts is the
// policy's hosts section, since a bare name is a host only if it's
// defined there.
func KindOf(v string, hosts map[string]string) Kind {
	switch {
	case v == "*":
		return Wildcard
	case strings.HasPrefix(v, "group:"):
		return Group
	case strings.HasPrefix(v, "tag:"):
		return Tag
	case strings.HasPrefix(v, "autogroup:"):
		return Autogroup
	case strings.HasPrefix(v, "ipset:"):
		return IPSet
	case strings.Contains(v, "@"):
		return User
	}
	if _, ok := hosts[v]; ok {
		return Host
	}
	if isAddress(v) {
		return Address
	}
	return Other
}

func isAddress(v string) bool {
	if _, err := netip.ParseAddr(v); err == nil {
		return true
	}
	if _, err := netip.ParsePrefix(v); err == nil {
		return true
	}
	if lo, hi, ok := strings.Cut(v, "-"); ok {
		_, err1 := netip.ParseAddr(lo)
		_, err2 := netip.ParseAddr(hi)
		return err1 == nil && err2 == nil
	}
	return false
}

// stripPort drops a host:port entry's port, as in "tag:prod:443",
// "alice@example.com:*", "[fd7a::1]:22" or "192.0.2.0/24:80-90".
func stripPort(v string) string {
	if strings.HasPrefix(v, "[") {
		if end := strings.Index(v, "]"); end > 0 {
			return v[1:end]
		}
	}
	i := strings.LastIndex(v, ":")
	if i < 0 {
		return v
	}
	head := v[:i]
	if _, err := netip.ParseAddr(v); err == nil {
		return v // a bare IPv6 address, no port
	}
	return head
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
