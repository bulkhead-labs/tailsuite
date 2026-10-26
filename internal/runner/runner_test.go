package runner

import (
	"bytes"
	"encoding/xml"
	"errors"
	"net/netip"
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

func TestRead(t *testing.T) {
	for name, c := range map[string]struct {
		res  agent.ProbeResult
		want string
	}{
		"reached":              {agent.ProbeResult{Outcome: agent.Success, Connected: true, Peer: "db"}, sawReached},
		"the wrong node":       {agent.ProbeResult{Outcome: agent.Success, Connected: true, Peer: "server"}, sawUnknown},
		"something else":       {agent.ProbeResult{Outcome: agent.Success, Connected: true}, sawUnknown},
		"dropped":              {agent.ProbeResult{Outcome: agent.Timeout}, sawBlocked},
		"connected then quiet": {agent.ProbeResult{Outcome: agent.Timeout, Connected: true}, sawUnknown},
		"no route":             {agent.ProbeResult{Outcome: agent.Error, Detail: "dial tcp 100.64.0.2:5432: connect: network is unreachable"}, sawBlocked},
		"refused":              {agent.ProbeResult{Outcome: agent.Refused}, sawUnknown},
		"other error":          {agent.ProbeResult{Outcome: agent.Error, Detail: "no buffer space"}, sawUnknown},
	} {
		if got, _ := read(c.res, "db"); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestJudge(t *testing.T) {
	h := func(saws ...string) []HalfResult {
		var out []HalfResult
		for i, s := range saws {
			out = append(out, HalfResult{Protocol: []string{"tcp", "udp"}[i], Saw: s})
		}
		return out
	}
	for name, c := range map[string]struct {
		expect string
		halves []HalfResult
		want   Result
	}{
		"allow, reached":                 {suite.Allow, h(sawReached), Pass},
		"allow, blocked":                 {suite.Allow, h(sawBlocked), Fail},
		"allow, unknown":                 {suite.Allow, h(sawUnknown), Unknown},
		"deny, blocked":                  {suite.Deny, h(sawBlocked), Pass},
		"deny, reached":                  {suite.Deny, h(sawReached), Fail},
		"deny, unknown":                  {suite.Deny, h(sawUnknown), Unknown},
		"accept, tcp reached, udp ?":     {suite.Allow, h(sawReached, sawUnknown), Pass},
		"accept, tcp blocked, udp ?":     {suite.Allow, h(sawBlocked, sawUnknown), Unknown},
		"deny, tcp blocked, udp ?":       {suite.Deny, h(sawBlocked, sawUnknown), Unknown},
		"deny, tcp reached, udp ?":       {suite.Deny, h(sawReached, sawUnknown), Fail},
		"accept, both blocked":           {suite.Allow, h(sawBlocked, sawBlocked), Fail},
		"deny, both blocked":             {suite.Deny, h(sawBlocked, sawBlocked), Pass},
		"check without an SSH probe yet": {suite.Check, h(sawUnknown), Refused},
	} {
		if got, _ := judge(c.expect, c.halves); got != c.want {
			t.Errorf("%s: %s, want %s", name, got, c.want)
		}
	}
}

func TestCombineAndExit(t *testing.T) {
	p := func(rs ...Result) []PairResult {
		var out []PairResult
		for _, r := range rs {
			out = append(out, PairResult{From: "a", To: "b", Result: r})
		}
		return out
	}
	for want, pairs := range map[Result][]PairResult{
		Fail:    p(Pass, Refused, Fail, Unknown),
		Refused: p(Pass, Unknown, Refused),
		Unknown: p(Pass, Unknown),
		Pass:    p(Pass, Pass),
	} {
		if got, _ := combine(pairs); got != want {
			t.Errorf("combine: %s, want %s", got, want)
		}
	}
	for _, c := range []struct {
		s      Summary
		strict bool
		want   int
	}{
		{Summary{Pass: 3}, false, 0},
		{Summary{Pass: 3, Unknown: 1}, false, 0},
		{Summary{Pass: 3, Unknown: 1}, true, 2},
		{Summary{Pass: 3, Fail: 1}, true, 1},
		{Summary{Unknown: 2, Refused: 1}, false, 2}, // nothing proven
		{Summary{Skipped: 4}, false, 2},
	} {
		if got := exitCode(c.s, c.strict); got != c.want {
			t.Errorf("%+v strict %v: %d, want %d", c.s, c.strict, got, c.want)
		}
	}
}

func sampleRun(strict bool) *Run {
	return &Run{
		Engine: "headscale", Version: "headscale 0.29.5", Profile: "pr", Strict: strict,
		Tests: []TestResult{
			{Name: "engineers reach the web tier", Source: "tests", Result: Pass},
			{Name: "laptops can't reach the database", Source: "tests", Result: Fail, Reason: "expected deny, and the traffic got through"},
			{Name: "policy tests[0]: alice@example.com → tag:db:5432", Source: "policy", Result: Unknown, Reason: "udp: there's no udp probe yet (#56)"},
			{Name: "bob reaches the office", Source: "tests", Result: Refused, Reason: "headscale refuses ip-sets"},
			{Name: "slow one", Source: "tests", Skipped: "skip slow"},
		},
		Summary: Summary{Pass: 1, Fail: 1, Unknown: 1, Refused: 1, Skipped: 1},
		Exit:    1,
	}
}

func TestWriteJUnit(t *testing.T) {
	for _, strict := range []bool{false, true} {
		var b bytes.Buffer
		if err := WriteJUnit(&b, sampleRun(strict)); err != nil {
			t.Fatal(err)
		}
		var doc junitSuites
		if err := xml.Unmarshal(b.Bytes(), &doc); err != nil {
			t.Fatal(err)
		}
		s := doc.Suites[0]
		if s.Tests != 5 || s.Failures != 1 {
			t.Errorf("strict %v: %+v", strict, s)
		}
		// UNKNOWN and REFUSED are skipped, or errors under strict; never a
		// failure, and never a pass.
		wantErrors, wantSkipped := 0, 3
		if strict {
			wantErrors, wantSkipped = 2, 1
		}
		if s.Errors != wantErrors || s.Skipped != wantSkipped {
			t.Errorf("strict %v: errors %d skipped %d", strict, s.Errors, s.Skipped)
		}
	}
}

func TestWriteText(t *testing.T) {
	var b bytes.Buffer
	if err := WriteText(&b, sampleRun(false)); err != nil {
		t.Fatal(err)
	}
	out := b.String()
	for _, want := range []string{"Not proven in this lab", "UNKNOWN", "REFUSED", "1 passed, 1 failed, 1 unknown, 1 refused, 1 skipped", "FAIL\n", "skipped slow one (skip slow)"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
	// An unproven test is listed apart, and never among the passes.
	head := out[:strings.Index(out, "Not proven")]
	if strings.Contains(head, "PASS    policy") {
		t.Errorf("%s", out)
	}
}

func TestFilterAdmits(t *testing.T) {
	db := netip.MustParseAddr("100.64.0.3")
	nodes := map[string]netip.Addr{"laptop": netip.MustParseAddr("100.64.0.1"), "server": netip.MustParseAddr("100.64.0.2"), "db": db}
	f := engine.Filter{Rules: []engine.FilterRule{
		// tag:prod → tag:db on 5432, compiled for db.
		{SrcIPs: []string{"100.64.0.2"}, DstPorts: []engine.DstPort{{IP: db.String(), Ports: engine.PortRange{First: 5432, Last: 5432}}}},
		// A rule to another address on the same port admits no one to db.
		{SrcIPs: []string{"100.64.0.1"}, DstPorts: []engine.DstPort{{IP: "192.0.2.9", Ports: engine.PortRange{First: 5432, Last: 5432}}}},
		// The marker admits no node.
		{SrcIPs: []string{"192.0.2.77"}, DstPorts: []engine.DstPort{{IP: "*", Ports: engine.PortRange{First: 41000, Last: 41000}}}, IPProto: []int{6}},
	}}
	if got := filterAdmits(f, 5432, nodes, db); !slices.Equal(got, []string{"server"}) {
		t.Errorf("5432: %v", got)
	}
	if got := filterAdmits(f, 80, nodes, db); len(got) != 0 {
		t.Errorf("80: %v", got)
	}
	// An unreadable source gets no benefit of the doubt here.
	f.Rules = append(f.Rules, engine.FilterRule{SrcIPs: []string{"something-new"}, DstPorts: []engine.DstPort{{IP: "*", Ports: engine.PortRange{First: 0, Last: 65535}}}})
	if got := filterAdmits(f, 80, nodes, db); len(got) != 0 {
		t.Errorf("unreadable: %v", got)
	}
}

func TestLabsFault(t *testing.T) {
	laptop, db := netip.MustParseAddr("100.64.0.1"), netip.MustParseAddr("100.64.0.3")
	rule := func(src, dstIP string, proto ...int) engine.Filter {
		return engine.Filter{Rules: []engine.FilterRule{{SrcIPs: []string{src}, DstPorts: []engine.DstPort{{IP: dstIP, Ports: engine.PortRange{First: 5000, Last: 6000}}}, IPProto: proto}}}
	}
	for name, c := range map[string]struct {
		f     engine.Filter
		err   error
		fault bool
	}{
		"no rule for the source":     {rule("100.64.0.2", db.String()), nil, false},
		"the source is admitted":     {rule(laptop.String(), db.String()), nil, true},
		"a range admits it":          {rule("100.64.0.0-100.64.0.9", "*"), nil, true},
		"an unreadable source":       {rule("something-new", "*"), nil, true},
		"an unreadable destination":  {rule(laptop.String(), "something-new"), nil, true},
		"another destination":        {rule(laptop.String(), "192.0.2.9"), nil, false},
		"UDP only":                   {rule(laptop.String(), "*", 17), nil, false},
		"a CIDR destination with db": {rule(laptop.String(), "100.64.0.0/24"), nil, true},
		"an unreadable filter":       {engine.Filter{}, errors.New("exec failed"), true},
		"an empty filter":            {engine.Filter{}, nil, false},
		"another port only":          {engine.Filter{Rules: []engine.FilterRule{{SrcIPs: []string{"*"}, DstPorts: []engine.DstPort{{IP: "*", Ports: engine.PortRange{First: 80, Last: 80}}}}}}, nil, false},
	} {
		if got := labsFault(c.f, c.err, 5432, laptop, db) != ""; got != c.fault {
			t.Errorf("%s: fault %v, want %v", name, got, c.fault)
		}
	}
}
