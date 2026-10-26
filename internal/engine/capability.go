package engine

import "slices"

// Support is how an engine handles a capability (0016).
type Support string

// Kinds of support.
const (
	// Native: the engine does it as Tailscale does.
	Native Support = "native"
	// Lowered: the engine does it after a rewrite that's exact for every
	// test it touches, recorded in the patch.
	Lowered Support = "lowered"
	// Refused: the engine can't, or can only approximate it. A test that
	// needs it is REFUSED on that engine.
	Refused Support = "refused"
)

// Capability names one row of 0016's matrix.
type Capability string

// The capabilities. A feature with no row is refused on every engine.
const (
	GrantsACLs       Capability = "grants-acls"          // grants and ACLs on TCP ports between tags, hosts, users and groups; *; "ip": ["*"]
	UsersGroups      Capability = "users-groups"         // users and groups as sources and destinations
	AutogroupMember  Capability = "autogroup-member"     // autogroup:member and autogroup:self
	AutogroupTagged  Capability = "autogroup-tagged"     // autogroup:tagged
	AutogroupShared  Capability = "autogroup-shared"     // autogroup:shared, and groups synced from an identity provider
	RoleAutogroups   Capability = "role-autogroups"      // autogroup:admin, :owner, :it-admin, :network-admin, :billing-admin, :auditor
	Posture          Capability = "posture"              // postures, srcPosture, defaultSrcPosture
	IPSets           Capability = "ip-sets"              // ipsets and ipset: references
	Routing          Capability = "routing"              // via, autogroup:internet, subnet routes, exit nodes
	SSH              Capability = "ssh"                  // SSH accept and deny
	SSHLocalpart     Capability = "ssh-localpart"        // SSH logins tied to the source's login
	SSHCheck         Capability = "ssh-check"            // SSH check mode
	SSHRecording     Capability = "ssh-recording"        // SSH session recording
	Funnel           Capability = "funnel"               // Funnel, in nodeAttrs
	NodeAttrs        Capability = "node-attrs"           // other nodeAttrs
	AttrConfig       Capability = "attr-config"          // attrConfig
	Services         Capability = "services"             // svc: and autoApprovers.services
	ExternalTailnets Capability = "external-tailnets"    // externalTailnets
	AppGrants        Capability = "app-grants"           // a grant's app capabilities
	PinnedIPv4       Capability = "pinned-ipv4"          // a device's Tailscale IPv4 address, as production has it
	PinnedIPv6       Capability = "pinned-ipv6"          // a device's Tailscale IPv6 address
	CompiledFilter   Capability = "compiled-filter-dump" // each node's compiled packet filter
	IPv4Disabled     Capability = "disable-ipv4"         // disableIPv4: true
	PolicyTests      Capability = "policy-tests"         // the policy's own tests, which tailsuite runs itself (0015)
)

// Entry is one capability's row for one engine.
type Entry struct {
	Capability Capability `json:"capability"`
	// Support is what the engine does today.
	Support Support `json:"support"`
	// Target is what it will do once Milestone ships; empty when it's
	// Support already.
	Target    Support `json:"target,omitempty"`
	Milestone string  `json:"milestone,omitempty"`
	Note      string  `json:"note,omitempty"`
}

// Capabilities is an engine's rows.
type Capabilities []Entry

// Support returns how the engine handles c. A capability with no row is
// refused.
func (cs Capabilities) Support(c Capability) Support {
	i := slices.IndexFunc(cs, func(e Entry) bool { return e.Capability == c })
	if i < 0 {
		return Refused
	}
	return cs[i].Support
}
