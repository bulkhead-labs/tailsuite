package suite

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/engine/headscale"
	"github.com/bulkhead-labs/tailsuite/internal/model"
	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
	"github.com/bulkhead-labs/tailsuite/internal/project"
)

// Everything here is synthetic.

func startup(t *testing.T) (*project.Project, engine.Lowering) {
	t.Helper()
	p, err := project.Load("testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	low, err := headscale.Lower(p.Tailnet)
	if err != nil {
		t.Fatal(err)
	}
	return p, low
}

func build(t *testing.T) []Planned {
	t.Helper()
	p, low := startup(t)
	tests, err := ReadDir(p.Dir)
	if err != nil {
		t.Fatal(err)
	}
	tests = append(tests, PolicyTests(p.Tailnet.Policy)...)
	planned, err := Build(p, tests, headscale.New(headscale.Config{}).Capabilities(), low)
	if err != nil {
		t.Fatal(err)
	}
	return planned
}

func byName(t *testing.T, ps []Planned, name string) Planned {
	t.Helper()
	i := slices.IndexFunc(ps, func(p Planned) bool { return p.Name == name })
	if i < 0 {
		t.Fatalf("no test %q", name)
	}
	return ps[i]
}

func TestReadFileRefusesWhatDoesntMakeSense(t *testing.T) {
	for name, src := range map[string]string{
		"unknown key":    "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 1, expect: allow, colour: red}\n",
		"no version":     "tests:\n  - {name: a, from: x, to: y, port: 1, expect: allow}\n",
		"two documents":  "version: 1\ntests: []\n---\nversion: 1\n",
		"no name":        "version: 1\ntests:\n  - {from: x, to: y, port: 1, expect: allow}\n",
		"bad port":       "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 70000, expect: allow}\n",
		"check, not SSH": "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 22, expect: check}\n",
		"udp probe":      "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 53, probe: udp, expect: allow}\n",
		"posture when":   "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 1, expect: allow, when: {custom:tier: gold}}\n",
		"our label":      "version: 1\ntests:\n  - {name: a, from: x, to: y, port: 1, expect: allow, labels: [needs:ssh]}\n",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := ReadFile([]byte(src), "tests/x.yaml"); err == nil || !strings.Contains(err.Error(), "tests/x.yaml") {
				t.Fatalf("got %v", err)
			}
		})
	}
	ts, err := ReadFile([]byte("version: 1\ntests:\n  - {name: a, from: x, to: y, port: 22, ssh: root, expect: check}\n"), "tests/x.yaml")
	if err != nil || ts[0].Where != "tests/x.yaml:3" || ts[0].Source != SourceTests {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestPolicyTests(t *testing.T) {
	p, _ := startup(t)
	ts := PolicyTests(p.Tailnet.Policy)
	if len(ts) != 3 {
		t.Fatalf("%d tests: %+v", len(ts), ts)
	}
	a := ts[0]
	if a.Name != "policy tests[0]: alice@example.com → tag:prod:80" || a.To != "tag:prod" || a.Port != 80 || a.Expect != Allow || a.Where != "/tests/0/accept/0" {
		t.Errorf("%+v", a)
	}
	// An omitted proto is TCP and UDP, as Tailscale reads it.
	if !slices.Equal(a.Protocols, []string{"tcp", "udp"}) {
		t.Errorf("protocols %v", a.Protocols)
	}
	if ts[1].Expect != Deny || ts[1].To != "tag:db" {
		t.Errorf("%+v", ts[1])
	}
}

func TestResolve(t *testing.T) {
	p, _ := startup(t)
	r := NewResolver(p.Tailnet)
	for id, want := range map[string][]string{
		"laptop":            {"laptop"},
		"group:eng":         {"bobs-laptop", "laptop"},
		"alice@example.com": {"laptop"},
		"tag:prod":          {"server"},
		"*":                 {"bobs-laptop", "db", "laptop", "server"},
		"autogroup:member":  {"bobs-laptop", "laptop"},
		"autogroup:tagged":  {"db", "server"},
		"autogroup:admin":   {"laptop"},
	} {
		m, err := r.Resolve(id)
		got := slices.Clone(m.Devices)
		slices.Sort(got)
		if err != nil || !slices.Equal(got, want) {
			t.Errorf("%s: %v, %v; want %v", id, got, err, want)
		}
	}
	for _, id := range []string{"carol@example.com", "group:ops", "autogroup:internet", "tag:none", "somewhere"} {
		var re *ResolveError
		if _, err := r.Resolve(id); !errors.As(err, &re) {
			t.Errorf("%s resolved: %v", id, err)
		}
	}
}

func TestBuild(t *testing.T) {
	planned := build(t)

	// The deny between devices no rule could touch runs.
	d := byName(t, planned, "laptops can't reach the database")
	if len(d.Pairs) != 1 || len(d.Pairs[0].Needs.Refused) != 0 || d.Pairs[0].Probe != "tcp" {
		t.Errorf("%+v", d)
	}
	// group:eng is two laptops. Bob's could be touched by the rule
	// headscale's copy leaves out (its IP set might hold the server, as
	// far as the planner can tell), so that pair is REFUSED; Alice's runs.
	e := byName(t, planned, "engineers reach the web tier")
	if len(e.Pairs) != 2 {
		t.Fatalf("pairs %+v", e.Pairs)
	}
	for _, pp := range e.Pairs {
		refused := len(pp.Needs.Refused) > 0
		if refused != (pp.From == "bobs-laptop") {
			t.Errorf("%s: refused %v: %v", pp.From, refused, pp.Needs.Refused)
		}
		if pp.Probe != "http" {
			t.Errorf("probe %s", pp.Probe)
		}
	}
	if !slices.Contains(e.AllLabels, "needs:users-groups") || !slices.Contains(e.AllLabels, "probe:http") || !slices.Contains(e.AllLabels, "source:tests") || !slices.Contains(e.AllLabels, "smoke") {
		t.Errorf("labels %v", e.AllLabels)
	}
	// A policy test naming no lab device is UNKNOWN, not a load error.
	c := byName(t, planned, "policy tests[1]: carol@example.com → tag:prod:80")
	if !strings.Contains(c.Unknown, "carol@example.com") {
		t.Errorf("%+v", c)
	}
}

func TestBuildLoadErrors(t *testing.T) {
	p, low := startup(t)
	caps := headscale.New(headscale.Config{}).Capabilities()
	for name, tst := range map[string]Test{
		"no listener":   {Name: "a", From: "laptop", To: "db", Port: 6543, Expect: Deny, Source: SourceTests},
		"no device":     {Name: "a", From: "carol@example.com", To: "db", Port: 5432, Expect: Deny, Source: SourceTests},
		"same device":   {Name: "a", From: "db", To: "tag:db", Port: 5432, Expect: Deny, Source: SourceTests},
		"no such group": {Name: "a", From: "group:ops", To: "db", Port: 5432, Expect: Deny, Source: SourceTests},
	} {
		t.Run(name, func(t *testing.T) {
			tst.Protocols = []string{"tcp"}
			_, err := Build(p, []Test{tst}, caps, low)
			var le *LoadError
			if !errors.As(err, &le) {
				t.Fatalf("got %v", err)
			}
		})
	}
	dup := Test{Name: "a", From: "laptop", To: "db", Port: 5432, Expect: Deny, Source: SourceTests, Protocols: []string{"tcp"}}
	if _, err := Build(p, []Test{dup, dup}, caps, low); err == nil || !strings.Contains(err.Error(), "taken") {
		t.Errorf("duplicate names: %v", err)
	}
	// The error says which listen: entry to add.
	_, err := Build(p, []Test{{Name: "a", From: "laptop", To: "db", Port: 6543, Expect: Deny, Source: SourceTests, Protocols: []string{"tcp"}}}, caps, low)
	if err == nil || !strings.Contains(err.Error(), "listen: [{ tcp: 6543 }]") {
		t.Errorf("%v", err)
	}
}

func TestOptionsAndSelect(t *testing.T) {
	p, _ := startup(t)
	o, err := Resolve(p.Lab, Options{})
	if err != nil || o.Profile != "default" || o.Engine != "headscale" || o.Unproven != "report" || o.Strict {
		t.Fatalf("%+v %v", o, err)
	}
	o, err = Resolve(p.Lab, Options{Profile: "nightly", Engine: "headscale"})
	if err != nil || o.Engine != "headscale" || !o.Strict {
		t.Fatalf("nightly: %+v %v", o, err)
	}
	if _, err := Resolve(p.Lab, Options{Profile: "nightlee"}); err == nil {
		t.Error("a misspelt profile ran")
	}

	planned := build(t)
	o, _ = Resolve(p.Lab, Options{Profile: "pr"})
	warn, err := Select(planned, o)
	if err != nil || len(warn) != 0 {
		t.Fatal(err, warn)
	}
	if s := byName(t, planned, "the web tier reaches the database").Skipped; s != "skip slow" {
		t.Errorf("skipped %q", s)
	}

	planned = build(t)
	if _, err := Select(planned, Options{Only: []string{"smok"}}); err == nil {
		t.Error("an --only typo ran nothing, silently")
	}
	if _, err := Select(planned, Options{Run: "^nothing$"}); err == nil {
		t.Error("a --run that matches nothing ran nothing, silently")
	}
	if warn, err := Select(planned, Options{Skip: []string{"slwo"}}); err != nil || len(warn) != 1 {
		t.Errorf("a --skip typo: %v %v", warn, err)
	}
	planned = build(t)
	if _, err := Select(planned, Options{Only: []string{"smoke"}, Skip: []string{"smoke"}}); err != nil {
		t.Fatal(err)
	}
	if byName(t, planned, "engineers reach the web tier").Skipped != "skip smoke" {
		t.Error("--skip didn't win over --only")
	}
}

func TestWarnings(t *testing.T) {
	p, _ := startup(t)
	planned := build(t)
	p.Lab.Profiles = map[string]project.Profile{"pr": {Engine: "headscale", Skip: []string{"slow"}}}
	w := Warnings(planned, p.Lab, "headscale")
	if !slices.ContainsFunc(w, func(s string) bool {
		return strings.Contains(s, "the web tier reaches the database") && strings.Contains(s, "no profile")
	}) {
		t.Errorf("%v", w)
	}
}

func TestNeedsOverApproximate(t *testing.T) {
	p, low := startup(t)
	pl := NewPlanner(p.Tailnet, headscale.New(headscale.Config{}).Capabilities(), low)
	tst := Test{Name: "x", From: "laptop", To: "server", Port: 80, Expect: Allow}
	n := pl.Needs(tst, Pair{From: "laptop", To: "server"})
	if len(n.Refused) != 0 || !slices.Contains(n.All, engine.UsersGroups) {
		t.Errorf("%+v", n)
	}
	// An SSH test, and one with a condition, are refused on headscale in M1.
	tst.SSH = "root"
	if n := pl.Needs(tst, Pair{From: "laptop", To: "server"}); !slices.Contains(n.All, engine.SSH) || len(n.Refused) == 0 {
		t.Errorf("ssh %+v", n)
	}
	tst.SSH, tst.When = "", map[string]string{"os": "windows"}
	if n := pl.Needs(tst, Pair{From: "laptop", To: "server"}); !slices.Contains(n.All, engine.Posture) || len(n.Refused) == 0 {
		t.Errorf("when %+v", n)
	}
}

func TestNeedsRefuseWhatTheLoweringChanged(t *testing.T) {
	// autogroup:admin in a rule, with a user whose roles lab.yaml doesn't
	// list: headscale's lowering can't be exact, and removes the rule.
	// Role autogroups are lowered on headscale, so only the change
	// refuses the pair.
	p, _ := startup(t)
	p.Tailnet.Users = append(p.Tailnet.Users, model.User{Login: "dave@example.com"})
	f, err := policy.Parse([]byte(`{"tagOwners": {"tag:prod": []}, "grants": [{"src": ["autogroup:admin"], "dst": ["tag:prod"], "ip": ["tcp:80"]}, {"src": ["*"], "dst": ["*"], "ip": ["tcp:9000"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p.Tailnet.Policy = f
	low, err := headscale.Lower(p.Tailnet)
	if err != nil || len(low.Changes) != 1 {
		t.Fatalf("%+v %v", low.Changes, err)
	}
	pl := NewPlanner(p.Tailnet, headscale.New(headscale.Config{}).Capabilities(), low)
	n := pl.Needs(Test{Name: "x", From: "laptop", To: "server", Port: 80, Expect: Allow}, Pair{From: "laptop", To: "server"})
	if len(n.Refused) != 1 || !strings.Contains(n.Refused[0], "leaves out") {
		t.Errorf("%+v", n)
	}
	// A pair the removed rule can't touch still runs.
	n = pl.Needs(Test{Name: "y", From: "server", To: "db", Port: 9000, Expect: Allow}, Pair{From: "server", To: "db"})
	if len(n.Refused) != 0 {
		t.Errorf("%+v", n)
	}
}

func TestSSHPortDefaultsTo22(t *testing.T) {
	// 0015's own example has no port.
	ts, err := ReadFile([]byte("version: 1\ntests:\n  - {name: root on prod, from: group:eng, to: tag:prod, ssh: root, expect: check}\n"), "tests/x.yaml")
	if err != nil || ts[0].Port != 22 {
		t.Fatalf("%+v %v", ts, err)
	}
}

func TestDefaultSrcPostureIsANeed(t *testing.T) {
	p, low := startup(t)
	f, err := policy.Parse([]byte(`{"postures": {"posture:a": ["node:os == 'linux'"]}, "defaultSrcPosture": ["posture:a"], "grants": [{"src": ["*"], "dst": ["*"], "ip": ["*"]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	p.Tailnet.Policy = f
	pl := NewPlanner(p.Tailnet, headscale.New(headscale.Config{}).Capabilities(), low)
	n := pl.Needs(Test{Name: "x", From: "laptop", To: "db", Port: 5432, Expect: Deny}, Pair{From: "laptop", To: "db"})
	if !slices.Contains(n.All, engine.Posture) || !slices.ContainsFunc(n.Refused, func(r string) bool { return strings.Contains(r, "refuses posture") }) {
		t.Errorf("%+v", n)
	}
}
