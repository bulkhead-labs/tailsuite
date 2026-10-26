package headscale

import "github.com/bulkhead-labs/tailsuite/internal/engine"

// capabilities is 0016's matrix for headscale 0.29.5, as built in M1. A
// capability is refused until the milestone that builds it ships; Target
// and Milestone say where it's going.
var capabilities = engine.Capabilities{
	{Capability: engine.GrantsACLs, Support: engine.Native, Note: "M1's basics: TCP ports between tags, hosts, users and groups, *, and \"ip\": [\"*\"]; every port and protocol form in M2"},
	{Capability: engine.UsersGroups, Support: engine.Native, Note: "matched by login, as the user's email"},
	{Capability: engine.AutogroupMember, Support: engine.Refused, Target: engine.Native, Milestone: "M4"},
	{Capability: engine.AutogroupTagged, Support: engine.Refused, Target: engine.Native, Milestone: "M4"},
	{Capability: engine.AutogroupShared, Support: engine.Refused, Note: "headscale has no sharing, and no groups from an identity provider"},
	{Capability: engine.RoleAutogroups, Support: engine.Lowered, Note: "as tag owners, to the lab's admin, which can't change who reaches what; elsewhere, to the users lab.yaml gives that role, when every user has their roles listed (decision 0025)"},
	{Capability: engine.Posture, Support: engine.Refused, Note: "declined upstream (headscale#2458)"},
	{Capability: engine.IPSets, Support: engine.Refused, Target: engine.Lowered, Milestone: "M2", Note: "to be expanded to addresses"},
	{Capability: engine.Routing, Support: engine.Refused, Target: engine.Native, Milestone: "M3"},
	{Capability: engine.SSH, Support: engine.Refused, Target: engine.Native, Milestone: "M3"},
	{Capability: engine.SSHLocalpart, Support: engine.Refused, Note: "headscale's support isn't verified"},
	{Capability: engine.SSHCheck, Support: engine.Refused, Target: engine.Native, Milestone: "M3", Note: "the demand is proven; completing it is UNKNOWN"},
	{Capability: engine.SSHRecording, Support: engine.Refused},
	{Capability: engine.Funnel, Support: engine.Refused},
	{Capability: engine.NodeAttrs, Support: engine.Refused},
	{Capability: engine.AttrConfig, Support: engine.Refused},
	{Capability: engine.Services, Support: engine.Refused},
	{Capability: engine.ExternalTailnets, Support: engine.Refused},
	{Capability: engine.AppGrants, Support: engine.Refused},
	{Capability: engine.PinnedIPv4, Support: engine.Refused, Note: "headscale 0.29.5 can't set a node's address: no API does, and its database can't be edited without a restart (decision 0025, #67)"},
	{Capability: engine.PinnedIPv6, Support: engine.Refused},
	{Capability: engine.CompiledFilter, Support: engine.Native},
	{Capability: engine.IPv4Disabled, Support: engine.Refused},
}
