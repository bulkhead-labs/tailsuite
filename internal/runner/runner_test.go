package runner

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
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
	for _, want := range []string{"Not proven in this lab", "UNKNOWN", "REFUSED", "1 passed, 1 failed, 1 unknown, 1 refused, 1 skipped", "FAIL\n"} {
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
