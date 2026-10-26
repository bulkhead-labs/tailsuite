package headscale

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// All policies here are synthetic.

func tailnet(t *testing.T, src string, users ...model.User) model.Tailnet {
	t.Helper()
	f, err := policy.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	return model.Tailnet{Policy: f, Users: users}
}

func lower(t *testing.T, src string, users ...model.User) engine.Lowering {
	t.Helper()
	l, err := Lower(tailnet(t, src, users...))
	if err != nil {
		t.Fatal(err)
	}
	if !l.Refused() {
		// The patch, applied to the user's file, is the copy.
		got, err := engine.ApplyPatch([]byte(src), l.Patch)
		if err != nil {
			t.Fatalf("patch doesn't apply: %v", err)
		}
		if string(got) != string(l.Policy) {
			t.Fatalf("patch gives\n%s\nnot\n%s", got, l.Policy)
		}
	}
	return l
}

// typed parses a lowered copy, for checking what it says.
func typed(t *testing.T, b []byte) policy.Policy {
	t.Helper()
	f, err := policy.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Unknown) > 0 {
		t.Fatalf("the copy has keys headscale won't take: %v", f.Unknown)
	}
	return f.Policy
}

func changes(l engine.Lowering) map[string]engine.Change {
	m := map[string]engine.Change{}
	for _, c := range l.Changes {
		m[c.Pointer] = c
	}
	return m
}

func TestLowerLeavesASimplePolicyAlone(t *testing.T) {
	src := `// Synthetic. A small startup's first segmented policy.
{
	"groups": {"group:eng": ["alice@example.com"]},
	"tagOwners": {"tag:prod": ["group:eng"], "tag:db": ["group:eng"]},
	"grants": [
		// Engineers reach the web tier over HTTP.
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:80"]},
		{"src": ["tag:prod"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
	],
}`
	l := lower(t, src)
	if l.Refused() || len(l.Patch) != 0 || len(l.Changes) != 0 || len(l.Rewrites) != 0 {
		t.Fatalf("got %+v", l)
	}
	if string(l.Policy) != src {
		t.Fatal("the copy differs from the file")
	}
}

// Research 0005's third policy, without defaultSrcPosture (which refuses
// the whole run; tested below). headscale 0.29.5 rejected six of its
// features, one at a time.
const everything = `// Synthetic. The features a lab has to treat carefully, in one file.
{
	"groups": {
		"group:eng": ["alice@example.com", "dave@example.com"],
	},
	"tagOwners": {
		"tag:prod":     ["autogroup:admin"],
		"tag:router":   ["autogroup:admin"],
		"tag:exit":     ["autogroup:admin"],
		"tag:recorder": ["autogroup:admin"],
	},
	"postures": {
		"posture:stableMac": ["node:os == 'macos'", "node:tsReleaseTrack == 'stable'"],
	},
	"ipsets": {
		"ipset:office": ["add 192.0.2.0/24", "remove 192.0.2.10"],
	},
	"grants": [
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:443"], "srcPosture": ["posture:stableMac"]},
		// The office LAN, only through the office router.
		{"src": ["group:eng"], "dst": ["ipset:office"], "ip": ["*"], "via": ["tag:router"]},
		{"src": ["autogroup:member"], "dst": ["autogroup:self"], "ip": ["*"]},
		{"src": ["autogroup:member"], "dst": ["autogroup:internet"], "ip": ["*"]},
	],
	"ssh": [
		{
			"action":      "check",
			"src":         ["group:eng"],
			"dst":         ["tag:prod"],
			"users":       ["autogroup:nonroot"],
			"checkPeriod": "12h",
			"recorder":        ["tag:recorder"],
			"enforceRecorder": true,
		},
	],
	"sshTests": [
		{"src": "alice@example.com", "dst": ["tag:prod"], "check": ["ubuntu"], "deny": ["root"]},
	],
	"nodeAttrs": [
		{"target": ["tag:prod"], "attr": ["funnel"]},
	],
	"autoApprovers": {
		"routes":   {"192.0.2.0/24": ["tag:router"]},
		"exitNode": ["tag:exit"],
	},
	"attrConfig": {
		"custom:team": {"type": "string", "broadcastToPeers": ["dave@example.com"]},
	},
}`

func TestLowerEverything(t *testing.T) {
	l := lower(t, everything)
	if l.Refused() {
		t.Fatalf("refused: %v", l.Err())
	}
	p := typed(t, l.Policy)

	// Removed: what headscale has no section for.
	if p.Postures != nil || p.IPSets != nil || p.NodeAttrs != nil || p.AttrConfig != nil || p.SSHTests != nil {
		t.Errorf("sections left in: %+v", p)
	}
	// The ipset rule goes; the others stay, the first without its
	// condition.
	if len(p.Grants) != 3 {
		t.Fatalf("%d grants, want 3", len(p.Grants))
	}
	if p.Grants[0].SrcPosture != nil || !slices.Equal(p.Grants[0].Dst, []string{"tag:prod"}) {
		t.Errorf("first grant: %+v", p.Grants[0])
	}
	if p.SSH[0].Recorder != nil || p.SSH[0].EnforceRecorder {
		t.Errorf("SSH rule keeps recording: %+v", p.SSH[0])
	}
	// Role autogroups as tag owners: the lab's admin.
	for tag, owners := range p.TagOwners {
		if !slices.Equal(owners, []string{LabAdmin}) {
			t.Errorf("%s owners %v", tag, owners)
		}
	}
	// Every comment survives, except those inside what was removed.
	if strings.Contains(string(l.Policy), "// The office LAN") {
		t.Error("the removed rule's comment is still there")
	}
	for _, c := range []string{"// Synthetic."} {
		if !strings.Contains(string(l.Policy), c) {
			t.Errorf("comment %q lost", c)
		}
	}

	ch := changes(l)
	want := map[string]engine.Capability{
		"/grants/1":            engine.IPSets,
		"/grants/0/srcPosture": engine.Posture,
		"/ssh/0/recorder":      engine.SSHRecording,
		"/postures":            engine.Posture,
		"/ipsets":              engine.IPSets,
		"/nodeAttrs/0":         engine.Funnel,
		"/attrConfig":          engine.AttrConfig,
		"/sshTests":            engine.PolicyTests,
	}
	for ptr, c := range want {
		if ch[ptr].Capability != c {
			t.Errorf("%s: change %+v, want %s", ptr, ch[ptr], c)
		}
	}
	// A rule's change names its identities, so its tests are REFUSED.
	if g := ch["/grants/1"]; !slices.Equal(g.Src, []string{"group:eng"}) || !slices.Equal(g.Dst, []string{"ipset:office"}) {
		t.Errorf("ipset rule's scope: %+v", g)
	}
	// Removals that aren't about a rule name the devices they touch.
	if !slices.Equal(ch["/nodeAttrs/0"].Targets, []string{"tag:prod"}) || !slices.Equal(ch["/attrConfig"].Targets, []string{"dave@example.com"}) {
		t.Errorf("targets: %+v, %+v", ch["/nodeAttrs/0"], ch["/attrConfig"])
	}
	// The policy's own tests touch nothing: tailsuite runs them.
	if c := ch["/sshTests"]; c.Src != nil || c.Dst != nil || c.Targets != nil {
		t.Errorf("sshTests touch %+v", c)
	}
}

func TestLowerRefuses(t *testing.T) {
	for name, src := range map[string]string{
		"default posture": `{"postures": {"posture:a": ["node:os == 'linux'"]}, "defaultSrcPosture": ["posture:a"], "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`,
		"unknown key":     `{"grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"], "newThing": true}]}`,
		"duplicate key":   `{"grants": [], "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`,
		"IPv4 disabled":   `{"disableIPv4": true, "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`,
		"no rules left":   `{"ipsets": {"ipset:a": ["add 192.0.2.0/24"]}, "grants": [{"src": ["*"], "dst": ["ipset:a"], "ip": ["*"]}]}`,
		"users and src":   `{"acls": [{"action": "accept", "src": ["*"], "users": ["*"], "dst": ["*:*"]}]}`,
		// headscale reads null as absent (allow-all); the marker would
		// make the lab deny everything.
		"null acls":   `{"acls": null}`,
		"null grants": `{"grants": null, "acls": [{"action": "accept", "src": ["*"], "dst": ["*:*"]}]}`,
		// The wait couldn't tell this rule from the marker.
		"marker address": `{"hosts": {"doc": "192.0.2.77"}, "grants": [{"src": ["doc"], "dst": ["*"], "ip": ["*"]}]}`,
		"marker as /32":  `{"grants": [{"src": ["192.0.2.77/32"], "dst": ["*"], "ip": ["*"]}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			l := lower(t, src)
			if !l.Refused() || l.Policy != nil {
				t.Fatalf("not refused: %+v", l)
			}
			for _, r := range l.Refusals {
				if r.Engine != Name || r.Reason == "" {
					t.Errorf("refusal %+v", r)
				}
			}
		})
	}
}

func TestLowerRoleAutogroupsInRules(t *testing.T) {
	const src = `{
	"grants": [
		{"src": ["autogroup:admin"], "dst": ["tag:prod"], "ip": ["tcp:22"]},
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:443"]},
	],
	"acls": [
		{"action": "accept", "src": ["autogroup:it-admin"], "dst": ["tag:prod:80"]},
	],
}`
	alice := model.User{Login: "alice@example.com", Roles: []string{"admin"}}
	bob := model.User{Login: "bob@example.com", Roles: []string{"member"}}
	carol := model.User{Login: "carol@example.com", Roles: []string{"it-admin", "admin"}}

	t.Run("every user's roles listed", func(t *testing.T) {
		l := lower(t, src, alice, bob, carol)
		p := typed(t, l.Policy)
		if !slices.Equal(p.Grants[0].Src, []string{"alice@example.com", "carol@example.com"}) {
			t.Errorf("admins: %v", p.Grants[0].Src)
		}
		if !slices.Equal(p.ACLs[0].Dst, []string{"tag:prod:80"}) || !slices.Equal(p.ACLs[0].Src, []string{"carol@example.com"}) {
			t.Errorf("it-admins: %+v", p.ACLs[0])
		}
		if len(l.Changes) != 0 {
			t.Errorf("an exact lowering refused tests: %+v", l.Changes)
		}
		if len(l.Rewrites) != 2 || l.Rewrites[0].Pointer == "" {
			t.Errorf("rewrites %+v", l.Rewrites)
		}
	})
	t.Run("a user without roles", func(t *testing.T) {
		l := lower(t, src, alice, model.User{Login: "dave@example.com"})
		ch := changes(l)
		if ch["/grants/0"].Capability != engine.RoleAutogroups || ch["/acls/0"].Capability != engine.RoleAutogroups {
			t.Fatalf("changes %+v", l.Changes)
		}
		if p := typed(t, l.Policy); len(p.Grants) != 1 || len(p.ACLs) != 0 {
			t.Errorf("kept %+v", p)
		}
		if l.Refused() {
			t.Error("group:eng's rule is still there; the run shouldn't be refused")
		}
	})
	t.Run("an owner who isn't an admin", func(t *testing.T) {
		l := lower(t, src, alice, model.User{Login: "olga@example.com", Roles: []string{"owner"}}, carol)
		if changes(l)["/grants/0"].Capability != engine.RoleAutogroups {
			t.Fatalf("autogroup:admin with an owner lowered: %+v", l.Rewrites)
		}
		// autogroup:it-admin isn't ambiguous.
		if p := typed(t, l.Policy); !slices.Equal(p.ACLs[0].Src, []string{"carol@example.com"}) {
			t.Errorf("it-admins %v", p.ACLs[0].Src)
		}
	})
	t.Run("a role no one holds", func(t *testing.T) {
		l := lower(t, src, bob)
		p := typed(t, l.Policy)
		if len(p.Grants) != 1 || len(p.ACLs) != 0 {
			t.Fatalf("kept %+v", p)
		}
		if len(l.Changes) != 0 || l.Refused() {
			t.Errorf("a rule that matches no lab device was refused: %+v", l)
		}
	})
}

func TestLowerAllRulesMatchNoLabDevice(t *testing.T) {
	// Every rule goes, exactly: the lab denies what production denies
	// among the lab's devices. "grants": [] is left, which denies all.
	l := lower(t, `{"grants": [{"src": ["autogroup:admin"], "dst": ["*"], "ip": ["*"]}]}`, model.User{Login: "bob@example.com", Roles: []string{"member"}})
	if l.Refused() {
		t.Fatalf("refused: %v", l.Err())
	}
	if p := typed(t, l.Policy); p.Grants == nil || len(p.Grants) != 0 {
		t.Errorf("grants %v", p.Grants)
	}
}

func TestLowerKeepsTheFilesSpelling(t *testing.T) {
	src := `{
	// Old-style keys and fields.
	"ACLs": [
		{"Action": "accept", "Users": ["autogroup:admin"], "Ports": ["tag:prod:22"]},
	],
	"TagOwners": {"tag:prod": ["autogroup:owner"]},
	"Tests": [{"src": "alice@example.com", "accept": ["tag:prod:22"]}],
}`
	l := lower(t, src, model.User{Login: "alice@example.com", Roles: []string{"admin"}})
	if l.Refused() {
		t.Fatalf("refused: %v", l.Err())
	}
	for _, op := range l.Patch {
		if strings.HasPrefix(op.Path, "/acls") || strings.HasPrefix(op.Path, "/tagOwners") || op.Path == "/tests" {
			t.Errorf("op %+v uses the typed copy's spelling", op)
		}
	}
	p := typed(t, l.Policy)
	if !slices.Equal(p.ACLs[0].Src, []string{"alice@example.com"}) || !slices.Equal(p.ACLs[0].Dst, []string{"tag:prod:22"}) || p.ACLs[0].Users != nil || p.ACLs[0].Ports != nil {
		t.Errorf("acl %+v", p.ACLs[0])
	}
	if p.Tests != nil {
		t.Error("tests kept")
	}
	if !strings.Contains(string(l.Policy), "// Old-style") {
		t.Error("comment lost")
	}
}

func TestLowerTailnetAddresses(t *testing.T) {
	l := lower(t, `{
	"hosts": {"db-host": "100.64.0.10", "lan": "198.51.100.0/24"},
	"grants": [
		{"src": ["100.64.0.0/10"], "dst": ["db-host"], "ip": ["tcp:5432"]},
		{"src": ["*"], "dst": ["lan"], "ip": ["*"]},
		{"src": ["100.64.0.1-100.64.0.9"], "dst": ["tag:prod"], "ip": ["*"]},
		{"src": ["fd7a:115c:a1e0::5"], "dst": ["tag:prod"], "ip": ["*"]},
	],
	"acls": [{"action": "accept", "src": ["*"], "dst": ["100.64.0.10:22"]}],
}`)
	ch := changes(l)
	for ptr, c := range map[string]engine.Capability{
		"/grants/0/dst/0": engine.PinnedIPv4,
		"/grants/2/src/0": engine.PinnedIPv4,
		"/grants/3/src/0": engine.PinnedIPv6,
		"/acls/0/dst/0":   engine.PinnedIPv4,
	} {
		if ch[ptr].Kind != engine.Inexact || ch[ptr].Capability != c {
			t.Errorf("%s: %+v", ptr, ch[ptr])
		}
	}
	// The whole range, and addresses outside it, aren't devices'.
	for _, ptr := range []string{"/grants/0/src/0", "/grants/1/dst/0"} {
		if _, ok := ch[ptr]; ok {
			t.Errorf("%s marked inexact", ptr)
		}
	}
	// A rule naming an address in the range could touch any lab device:
	// headscale decides who holds it.
	if c := ch["/grants/0/dst/0"]; !slices.Equal(c.Dst, []string{"*"}) || !slices.Equal(c.Src, []string{"100.64.0.0/10"}) {
		t.Errorf("scope %+v", c)
	}
	// Inexact places are kept.
	if p := typed(t, l.Policy); len(p.Grants) != 4 {
		t.Errorf("grants %d", len(p.Grants))
	}
}

func TestLowerKeepsRulesThatOnlyCoverTheMarker(t *testing.T) {
	// An exit-node rule, a wider source, a destination: none is the marker.
	for _, src := range []string{
		`{"grants": [{"src": ["*"], "dst": ["0.0.0.0/0"], "ip": ["*"]}]}`,
		`{"acls": [{"action": "accept", "src": ["*"], "dst": ["0.0.0.0/0:*", "192.0.2.0/24:22"]}]}`,
		`{"grants": [{"src": ["192.0.2.0/24"], "dst": ["*"], "ip": ["*"]}]}`,
	} {
		if l := lower(t, src); l.Refused() {
			t.Errorf("%s: %v", src, l.Err())
		}
	}
}

func TestLowerEmptyPostureListsAreExact(t *testing.T) {
	l := lower(t, `{"defaultSrcPosture": [], "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"], "srcPosture": []}], "ssh": [{"action": "accept", "src": ["tag:a"], "dst": ["tag:b"], "users": ["root"], "srcPosture": []}]}`)
	if l.Refused() || len(l.Changes) != 0 || len(l.Rewrites) != 3 {
		t.Fatalf("refused %v changes %+v rewrites %+v", l.Err(), l.Changes, l.Rewrites)
	}
	if strings.Contains(string(l.Policy), "srcPosture") || strings.Contains(string(l.Policy), "defaultSrcPosture") {
		t.Errorf("%s", l.Policy)
	}
}

func TestLowerNotesIgnoredPins(t *testing.T) {
	tn := tailnet(t, `{"grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`)
	tn.Devices = []model.Device{{Name: "db", Tags: []string{"tag:db"}, IPv4: "100.64.0.10"}}
	l, err := Lower(tn)
	if err != nil || len(l.Notes) != 1 || !strings.Contains(l.Notes[0], "#67") {
		t.Fatalf("%+v %v", l.Notes, err)
	}
}

func TestLowerRemovesExactSettings(t *testing.T) {
	l := lower(t, `{"derpMap": {"Regions": {}}, "disableIPv4": false, "oneCGNATRoute": "mac-always", "randomizeClientPort": true, "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`)
	p := typed(t, l.Policy)
	if p.DERPMap != nil || p.OneCGNATRoute != "" || !p.RandomizeClientPort {
		t.Errorf("copy %+v", p)
	}
	if len(l.Changes) != 0 || len(l.Rewrites) != 3 {
		t.Errorf("changes %+v rewrites %+v", l.Changes, l.Rewrites)
	}
}

func TestLowerPatchIsRFC6902(t *testing.T) {
	l := lower(t, everything)
	b, err := json.Marshal(l.Patch)
	if err != nil {
		t.Fatal(err)
	}
	var ops []map[string]any
	if err := json.Unmarshal(b, &ops); err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		switch op["op"] {
		case "add", "remove", "replace", "move":
		default:
			t.Errorf("op %v", op)
		}
		if op["note"] == "" {
			t.Errorf("op without a note %v", op)
		}
	}
}

func TestStripPortMatchesThePolicyPackage(t *testing.T) {
	// References come from the policy package; the lowering strips ports
	// the same way to match them.
	src := `{"acls": [{"action": "accept", "src": ["*"], "dst": ["tag:prod:443", "alice@example.com:*", "[fd7a:115c:a1e0::1]:22", "192.0.2.0/24:80-90", "host-a:22", "fd7a:115c:a1e0::2"]}], "hosts": {"host-a": "192.0.2.1"}}`
	f, err := policy.Parse([]byte(src))
	if err != nil {
		t.Fatal(err)
	}
	var want []string
	for _, r := range f.References() {
		if r.Role == "dst" {
			want = append(want, r.Value)
		}
	}
	var got []string
	for _, v := range f.Policy.ACLs[0].Dst {
		got = append(got, stripPort(v))
	}
	if !slices.Equal(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func FuzzLower(f *testing.F) {
	f.Add(everything)
	f.Add(`{"ACLs": [{"action": "accept", "users": ["autogroup:admin"], "ports": ["*:*"]}]}`)
	f.Add(`{"grants": [{"src": ["100.64.0.1"], "dst": ["autogroup:owner"], "ip": ["*"]}], "tagOwners": {"tag:a": ["autogroup:auditor"]}}`)
	users := []model.User{{Login: "alice@example.com", Roles: []string{"admin"}}, {Login: "olga@example.com", Roles: []string{"owner", "admin"}}}
	f.Fuzz(func(t *testing.T, src string) {
		pf, err := policy.Parse([]byte(src))
		if err != nil {
			return
		}
		l, err := Lower(model.Tailnet{Policy: pf, Users: users})
		if err != nil {
			t.Fatalf("lowering failed: %v", err)
		}
		if l.Refused() {
			return
		}
		got, err := engine.ApplyPatch([]byte(src), l.Patch)
		if err != nil || string(got) != string(l.Policy) {
			t.Fatalf("patch: %v", err)
		}
		// The copy holds no role autogroup, IP set, test or key headscale
		// doesn't take.
		cf, err := policy.Parse(l.Policy)
		if err != nil {
			t.Fatal(err)
		}
		if len(cf.Unknown) > 0 || cf.Policy.Tests != nil || cf.Policy.SSHTests != nil || cf.Policy.IPSets != nil || cf.Policy.Postures != nil {
			t.Fatalf("copy %s", l.Policy)
		}
		for _, r := range cf.References() {
			// In the places the lowering owns. A role autogroup elsewhere,
			// such as a group's member, isn't valid in Tailscale either.
			lowered := r.Role == "src" || r.Role == "dst" || r.Role == "via" || r.Role == "owner" || r.Role == "approver"
			if _, ok := roleAutogroups[r.Value]; ok && lowered {
				t.Fatalf("role autogroup left at %s", r.Pointer)
			}
			// In a rule. Elsewhere (a group's name, say) Tailscale and
			// headscale both reject it, and Start reports headscale's
			// refusal.
			if r.Kind == policy.IPSet && (r.Role == "src" || r.Role == "dst" || r.Role == "via") {
				t.Fatalf("IP set left at %s", r.Pointer)
			}
		}
	})
}
