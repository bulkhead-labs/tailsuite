package policy

import (
	"bytes"
	"encoding/json"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func load(t *testing.T, name string) *File {
	t.Helper()
	raw, err := os.ReadFile("testdata/" + name + ".hujson")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// Research 0005's three synthetic policies parse, with nothing unknown:
// the trial's types left attrConfig and SSH recording out on purpose,
// and these types cover them.
func TestResearch0005Policies(t *testing.T) {
	for _, name := range []string{"default", "segmented", "everything"} {
		f := load(t, name)
		if len(f.Unknown) != 0 {
			t.Errorf("%s: unknown keys %+v", name, f.Unknown)
		}
	}
}

// The byte offsets of /grants/0 match what research 0005's trial measured
// on the same three files, with another parser.
func TestSpanMatchesResearch0005(t *testing.T) {
	for name, want := range map[string][2]int{
		"default":    {174, 215},
		"segmented":  {405, 467},
		"everything": {623, 722},
	} {
		f := load(t, name)
		start, end, ok := f.Span("/grants/0")
		if !ok || start != want[0] || end != want[1] {
			t.Errorf("%s: /grants/0 at %d-%d (%v), want %d-%d", name, start, end, ok, want[0], want[1])
		}
		var g Grant
		if err := json.Unmarshal(standardize(t, f.Raw[start:end]), &g); err != nil || len(g.Src) == 0 {
			t.Errorf("%s: the span doesn't hold the grant: %q", name, f.Raw[start:end])
		}
	}
}

func standardize(t *testing.T, b []byte) []byte {
	t.Helper()
	f, err := Parse(append(append([]byte(`{"x":`), b...), '}'))
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]json.RawMessage
	_ = json.Unmarshal(mustJSON(t, f), &m)
	return m["x"]
}

func mustJSON(t *testing.T, f *File) []byte {
	t.Helper()
	tree := f.tree.Clone()
	tree.Standardize()
	return tree.Pack()
}

func TestUnknownKeys(t *testing.T) {
	f := load(t, "unknown")
	got := map[string]string{}
	for _, u := range f.Unknown {
		got[u.Pointer] = u.Reason
		if s := string(f.Raw[u.Start:u.End]); !strings.Contains(s, strings.TrimPrefix(u.Pointer[strings.LastIndex(u.Pointer, "/"):], "/")) {
			t.Errorf("%s: its span %q doesn't hold the key", u.Pointer, s)
		}
	}
	want := map[string]string{
		"/acls":                           DuplicateKey + " (overridden by ACLs)",
		"/grants/0/futureField":           NotUnderstood,
		"/postures/posture:new/futureKey": NotUnderstood,
		"/futureSection":                  NotUnderstood,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("unknown keys:\n got %v\nwant %v", got, want)
	}
	// Understood in other spellings, or at their zero values.
	if len(f.Policy.ACLs) != 1 {
		t.Errorf("the typed copy didn't keep the last acls: %+v", f.Policy.ACLs)
	}
	if ptr, ok := f.FilePointer("/acls/0/src/0"); !ok || ptr != "/ACLs/0/src/0" {
		t.Errorf("the overriding ACLs isn't where /acls points: %q, %v", ptr, ok)
	}
	if len(f.Policy.Groups) != 2 || f.Policy.Groups["group:Eng"] == nil {
		t.Errorf("Groups, and its two case-distinct keys: %v", f.Policy.Groups)
	}
	if p := f.Policy.Postures; !p["posture:old"].Legacy || p["posture:new"].Legacy {
		t.Errorf("posture syntaxes: %+v", p)
	}
}

func TestFilePointerKeepsTheFilesSpelling(t *testing.T) {
	f, err := Parse([]byte(`{"ACLs": [{"Action": "accept", "Src": ["group:eng"], "Dst": ["tag:db:5432"]}], "Groups": {"group:eng": ["alice@example.com"]}}`))
	if err != nil {
		t.Fatal(err)
	}
	for canonical, want := range map[string]string{
		"/acls/0/src/0":     "/ACLs/0/Src/0",
		"/groups/group:eng": "/Groups/group:eng",
		"/grants":           "",
		"/acls/1":           "",
		"/groups/group:ENG": "",
	} {
		got, ok := f.FilePointer(canonical)
		if (want == "") == ok || got != want {
			t.Errorf("FilePointer(%q) = %q, %v; want %q", canonical, got, ok, want)
		}
	}
	if len(f.Unknown) != 0 {
		t.Errorf("capitalised keys reported unknown: %+v", f.Unknown)
	}
}

// Research 0005 counted the users each policy names: 6 in the segmented
// policy, 4 in the other. All are found, the one in attrConfig included.
func TestReferencesFindEveryUser(t *testing.T) {
	for name, want := range map[string][]string{
		"default": nil,
		"segmented": {
			"/groups/group:eng/0", "/groups/group:eng/1", "/groups/group:ops/0",
			"/grants/4/src/0", "/tests/0/src", "/tests/1/src",
		},
		"everything": {
			"/groups/group:eng/0", "/groups/group:eng/1",
			"/sshTests/0/src", "/attrConfig/custom:team/broadcastToPeers/0",
		},
	} {
		f := load(t, name)
		var got []string
		for _, r := range f.References() {
			if r.Kind == User {
				got = append(got, r.Pointer)
			}
		}
		slices.Sort(got)
		slices.Sort(want)
		if !slices.Equal(got, want) {
			t.Errorf("%s: users at\n got %v\nwant %v", name, got, want)
		}
	}
}

func TestReferenceKindsAndPorts(t *testing.T) {
	f := load(t, "segmented")
	byPointer := map[string]Ref{}
	for _, r := range f.References() {
		byPointer[r.Pointer] = r
	}
	for ptr, want := range map[string]Ref{
		"/grants/0/dst/0":     {Value: "tag:prod", Kind: Tag, Role: "dst"},
		"/grants/4/dst/0":     {Value: "office-printer", Kind: Host, Role: "dst"},
		"/tagOwners/tag:db/0": {Value: "group:ops", Kind: Group, Role: "owner"},
		"/tests/0/accept/0":   {Value: "tag:prod", Kind: Tag, Role: "dst"},
		"/tests/0/deny/0":     {Value: "tag:db", Kind: Tag, Role: "dst"},
	} {
		got := byPointer[ptr]
		want.Pointer = ptr
		if got != want {
			t.Errorf("%s: %+v, want %+v", ptr, got, want)
		}
	}
	if ids := f.Identities(); len(ids) == 0 || slices.ContainsFunc(ids, func(r Ref) bool { return r.Kind == Host }) {
		t.Errorf("Identities = %+v", ids)
	}
}

func TestStripPort(t *testing.T) {
	for in, want := range map[string]string{
		"tag:prod:443":        "tag:prod",
		"alice@example.com:*": "alice@example.com",
		"*:*":                 "*",
		"192.0.2.0/24:80-90":  "192.0.2.0/24",
		"[fd7a:115c::1]:22":   "fd7a:115c::1",
		"office-printer:631":  "office-printer",
		"autogroup:member:22": "autogroup:member",
		"fd7a:115c::1":        "fd7a:115c::1",
	} {
		if got := stripPort(in); got != want {
			t.Errorf("stripPort(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindOf(t *testing.T) {
	hosts := map[string]string{"printer": "192.0.2.10"}
	for in, want := range map[string]Kind{
		"*": Wildcard, "group:eng": Group, "tag:prod": Tag, "autogroup:self": Autogroup,
		"ipset:office": IPSet, "alice@example.com": User, "alice@github": User,
		"printer": Host, "192.0.2.1": Address, "192.0.2.0/24": Address,
		"192.0.2.1-192.0.2.9": Address, "fd7a:115c::/48": Address, "svc:web": Other, "nothing": Other,
	} {
		if got := KindOf(in, hosts); got != want {
			t.Errorf("KindOf(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestBadShapesAreErrors(t *testing.T) {
	for _, src := range []string{
		`{"grants": {}}`,
		`{"groups": {"group:eng": "alice@example.com"}}`,
		`{"postures": {"posture:x": 3}}`,
		`{"acls": [`,
		`not json`,
	} {
		if _, err := Parse([]byte(src)); err == nil {
			t.Errorf("Parse(%s) accepted it", src)
		}
	}
}

func TestPostureRoundTrip(t *testing.T) {
	for _, src := range []string{
		`["node:os == 'macos'"]`,
		`{"assertions":["node:os == 'macos'"],"onFailure":{"showAlerts":["warn-in-client"],"endUserMessage":"Update"}}`,
	} {
		var p Posture
		if err := json.Unmarshal([]byte(src), &p); err != nil {
			t.Fatal(err)
		}
		out, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if p.Legacy != strings.HasPrefix(src, "[") || !bytes.HasPrefix(out, []byte(src[:1])) {
			t.Errorf("%s came back as %s", src, out)
		}
	}
}

func TestRawIsKeptByteForByte(t *testing.T) {
	raw, err := os.ReadFile("testdata/everything.hujson")
	if err != nil {
		t.Fatal(err)
	}
	f, err := Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 'X' // the caller's buffer changing doesn't change the file
	if !bytes.HasPrefix(f.Raw, []byte("//")) || len(f.Raw) != len(raw) {
		t.Error("Raw isn't an exact copy of the file")
	}
}

// A repeated key never merges into the typed copy: Go's decoder would
// decode the second occurrence into the first, giving a rule the file
// doesn't state. These are the review's three inputs.
func TestDuplicatesDontMerge(t *testing.T) {
	f, err := Parse([]byte(`{
		"acls": [{"action": "accept", "users": ["alice@example.com"], "proto": "tcp", "dst": ["tag:a:22"]}],
		"ACLs": [{"action": "accept", "src": ["tag:b"], "dst": ["tag:a:22"]}],
		"groups": {"group:a": ["alice@example.com"]},
		"groups": {"group:b": ["bob@example.com"]},
		"autoApprovers": {"routes": {"192.0.2.0/24": ["tag:r"]}},
		"autoApprovers": {"exitNode": ["tag:x"]},
	}`))
	if err != nil {
		t.Fatal(err)
	}
	p := f.Policy
	if len(p.ACLs) != 1 || p.ACLs[0].Proto != "" || p.ACLs[0].Users != nil || !slices.Equal(p.ACLs[0].Src, []string{"tag:b"}) {
		t.Errorf("acls merged: %+v", p.ACLs)
	}
	if len(p.Groups) != 1 || p.Groups["group:b"] == nil {
		t.Errorf("groups merged: %v", p.Groups)
	}
	if p.AutoApprovers.Routes != nil || !slices.Equal(p.AutoApprovers.ExitNode, []string{"tag:x"}) {
		t.Errorf("autoApprovers merged: %+v", p.AutoApprovers)
	}
	for _, r := range f.References() {
		if r.Value == "alice@example.com" {
			t.Errorf("a reference into an overridden occurrence: %+v", r)
		}
	}
	if len(f.Unknown) != 3 {
		t.Errorf("unknown = %+v, want the three overridden occurrences", f.Unknown)
	}
}

// The reference's own nodeAttrs app example, routes included, is
// understood, and the connectors' tags are found.
func TestNodeAttrsAppIsCarried(t *testing.T) {
	f, err := Parse([]byte(`{"nodeAttrs": [{"target": ["*"], "app": {"tailscale.com/app-connectors": [
		{"name": "github", "connectors": ["tag:github-connector"], "domains": ["github.com"], "routes": ["192.0.2.0/24"]}
	]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Unknown) != 0 {
		t.Errorf("unknown = %+v", f.Unknown)
	}
	if !slices.ContainsFunc(f.References(), func(r Ref) bool {
		return r.Value == "tag:github-connector" && r.Role == "payload" && r.Pointer == "/nodeAttrs/0/app/tailscale.com~1app-connectors/0/connectors/0"
	}) {
		t.Errorf("the connector's tag isn't referenced: %+v", f.References())
	}
}

// Identities inside application payloads, which tailsuite doesn't
// interpret, are still found, so a rewrite can't miss them.
func TestPayloadIdentities(t *testing.T) {
	f, err := Parse([]byte(`{"grants": [{"src": ["tag:a"], "dst": ["tag:b"], "app": {"example.com/cap/x": [{"users": ["alice@example.com"], "count": 3}]}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, r := range f.Identities() {
		if r.Value == "alice@example.com" && r.Role == "payload" && r.Kind == User {
			found = true
		}
	}
	if !found {
		t.Errorf("alice in a payload wasn't found: %+v", f.Identities())
	}
}

func TestDefinitionsAndExternalReferences(t *testing.T) {
	f, err := Parse([]byte(`{
		"groups": {"group:eng": []},
		"tagOwners": {"tag:prod": []},
		"externalTailnets": {"partner": {"allowExternalReferencesTo": ["tag:shared"]}},
	}`))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]Ref{
		"/groups/group:eng":   {Value: "group:eng", Kind: Group, Role: "defines"},
		"/tagOwners/tag:prod": {Value: "tag:prod", Kind: Tag, Role: "defines"},
		"/externalTailnets/partner/allowExternalReferencesTo/0": {Value: "tag:shared", Kind: Tag, Role: "external"},
	}
	got := map[string]Ref{}
	for _, r := range f.References() {
		got[r.Pointer] = r
	}
	for ptr, w := range want {
		w.Pointer = ptr
		if got[ptr] != w {
			t.Errorf("%s: %+v, want %+v", ptr, got[ptr], w)
		}
	}
}
