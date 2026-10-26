// Package runner runs a planned suite against a running lab, with real
// traffic, and turns what the node agents saw into PASS, FAIL, UNKNOWN or
// REFUSED (decisions 0001, 0015, 0016, 0020).
//
// It never decides allow or deny from the policy. A deny is proven only by
// traffic that didn't arrive, together with a positive control: proof, in
// the same run, that the target is alive, from an allowed source or from
// itself.
package runner

import (
	"context"
	"fmt"
	"net/netip"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agent"
	"github.com/bulkhead-labs/tailsuite/internal/agent/control"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

// Result is a test's or a pair's result (0001).
type Result string

// The four results.
const (
	Pass    Result = "PASS"
	Fail    Result = "FAIL"
	Unknown Result = "UNKNOWN"
	Refused Result = "REFUSED"
)

// Config is what a run needs.
type Config struct {
	Sandbox sandbox.Sandbox
	Lab     engine.Lab
	// Engine and Version name the engine, for results.
	Engine  string
	Version string
	Project *project.Project
	Options suite.Options
	// ProbeTimeout bounds each probe; zero means DefaultProbeTimeout.
	ProbeTimeout time.Duration
	// Parallel bounds probes in flight; zero means DefaultParallel.
	Parallel int

	// afterDeclare, for tests, runs once the listeners are declared.
	afterDeclare func()
}

// Defaults.
const (
	DefaultProbeTimeout = 3 * time.Second
	DefaultParallel     = 8
)

// Run is a finished run, as every output reports it.
type Run struct {
	Engine   string       `json:"engine"`
	Version  string       `json:"version"`
	Profile  string       `json:"profile"`
	Strict   bool         `json:"strict"`
	Tests    []TestResult `json:"tests"`
	Warnings []string     `json:"warnings,omitempty"`
	Summary  Summary      `json:"summary"`
	// Exit is the exit code (0015): 0 fine, 1 a FAIL, 2 nothing proven or
	// unproven results under strict.
	Exit int `json:"exit"`
}

// Summary counts the tests by result. Positive controls aren't tests, and
// aren't counted.
type Summary struct {
	Pass    int `json:"pass"`
	Fail    int `json:"fail"`
	Unknown int `json:"unknown"`
	Refused int `json:"refused"`
	Skipped int `json:"skipped"`
}

// TestResult is one test's result.
type TestResult struct {
	Name    string       `json:"name"`
	Source  string       `json:"source"`
	Where   string       `json:"where"`
	Labels  []string     `json:"labels"`
	Expect  string       `json:"expect"`
	Port    int          `json:"port,omitempty"`
	Result  Result       `json:"result,omitempty"`
	Reason  string       `json:"reason,omitempty"`
	Skipped string       `json:"skipped,omitempty"`
	Pairs   []PairResult `json:"pairs,omitempty"`
}

// PairResult is one device pair's result.
type PairResult struct {
	From   string       `json:"from"`
	To     string       `json:"to"`
	Probe  string       `json:"probe"`
	Result Result       `json:"result"`
	Reason string       `json:"reason,omitempty"`
	Halves []HalfResult `json:"halves,omitempty"`
}

// HalfResult is what one protocol of a pair saw.
type HalfResult struct {
	Protocol string `json:"protocol"`
	// Saw is reached, blocked or unknown.
	Saw     string             `json:"saw"`
	Reason  string             `json:"reason,omitempty"`
	Outcome *agent.ProbeResult `json:"outcome,omitempty"`
	// Control is the positive control behind a blocked half.
	Control *Control `json:"control,omitempty"`
}

// Control is a positive control: the target answered, from an allowed
// source or from itself (0001).
type Control struct {
	From    string             `json:"from"`
	Alive   bool               `json:"alive"`
	Outcome *agent.ProbeResult `json:"outcome,omitempty"`
}

// What a half saw.
const (
	sawReached = "reached"
	sawBlocked = "blocked"
	sawUnknown = "unknown"
)

type runner struct {
	cfg      Config
	nodes    map[string]engine.Node
	sessions map[string]*control.Session
	gens     map[string]uint64

	mu       sync.Mutex
	controls map[string]*Control // by "to:port:probe"
	filters  map[string]engine.Filter
}

// Execute runs the planned tests. A lab that can't be driven (a session
// that won't open, listeners that can't be declared) is an error: exit 3.
func Execute(ctx context.Context, cfg Config, planned []suite.Planned) (*Run, error) {
	if cfg.ProbeTimeout == 0 {
		cfg.ProbeTimeout = DefaultProbeTimeout
	}
	if cfg.Parallel == 0 {
		cfg.Parallel = DefaultParallel
	}
	r := &runner{cfg: cfg, nodes: map[string]engine.Node{}, sessions: map[string]*control.Session{},
		gens: map[string]uint64{}, controls: map[string]*Control{}, filters: map[string]engine.Filter{}}
	for _, n := range cfg.Lab.Nodes() {
		r.nodes[n.Name] = n
	}
	defer func() {
		for _, s := range r.sessions {
			_ = s.Close()
		}
	}()
	if err := r.declare(ctx); err != nil {
		return nil, err
	}
	if cfg.afterDeclare != nil {
		cfg.afterDeclare()
	}

	results := make([]TestResult, len(planned))
	type job struct{ ti, pi int }
	var jobs []job
	for ti, pt := range planned {
		results[ti] = TestResult{Name: pt.Name, Source: pt.Source, Where: pt.Where, Labels: pt.AllLabels, Expect: pt.Expect, Port: pt.Port, Skipped: pt.Skipped}
		if pt.Skipped != "" || pt.Unknown != "" {
			continue
		}
		results[ti].Pairs = make([]PairResult, len(pt.Pairs))
		for pi := range pt.Pairs {
			jobs = append(jobs, job{ti, pi})
		}
	}
	sem := make(chan struct{}, cfg.Parallel)
	var wg sync.WaitGroup
	for _, j := range jobs {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			results[j.ti].Pairs[j.pi] = r.pair(ctx, planned[j.ti], planned[j.ti].Pairs[j.pi])
		})
	}
	wg.Wait()

	// Listeners that changed under the probes make what they served
	// UNKNOWN (0020): another process declared a different set.
	moved := r.moved(ctx)
	for ti := range results {
		for pi := range results[ti].Pairs {
			pr := &results[ti].Pairs[pi]
			if why, ok := moved[pr.To]; ok && pr.Result != Refused {
				pr.Result, pr.Reason = Unknown, why
			}
			for hi := range pr.Halves {
				if c := pr.Halves[hi].Control; c != nil {
					if why, ok := moved[c.From]; ok && pr.Result != Refused {
						pr.Result, pr.Reason = Unknown, why
					}
				}
			}
		}
	}

	run := &Run{Engine: cfg.Engine, Version: cfg.Version, Profile: cfg.Options.Profile, Strict: cfg.Options.Strict}
	for ti, pt := range planned {
		tr := &results[ti]
		switch {
		case tr.Skipped != "":
			run.Summary.Skipped++
		case pt.Unknown != "":
			tr.Result, tr.Reason = Unknown, pt.Unknown
		default:
			tr.Result, tr.Reason = combine(tr.Pairs)
		}
		switch tr.Result {
		case Pass:
			run.Summary.Pass++
		case Fail:
			run.Summary.Fail++
		case Unknown:
			run.Summary.Unknown++
		case Refused:
			run.Summary.Refused++
		}
	}
	run.Tests = results
	run.Exit = exitCode(run.Summary, run.Strict)
	return run, nil
}

// declare makes every node's listeners exactly lab.yaml's, on one session
// per node that lasts the run, and records each set's generation.
func (r *runner) declare(ctx context.Context) error {
	names := make([]string, 0, len(r.nodes))
	for n := range r.nodes {
		names = append(names, n)
	}
	slices.Sort(names)
	for _, name := range names {
		s, err := control.Start(ctx, r.cfg.Sandbox, r.nodes[name].Sandbox)
		if err != nil {
			return fmt.Errorf("runner: %w", err)
		}
		r.sessions[name] = s
		want := []agent.Listener{}
		for _, l := range r.cfg.Project.Lab.Devices[name].Listen {
			want = append(want, agent.Listener{Proto: l.Proto, Port: l.Port})
		}
		got, gen, err := s.SetListeners(ctx, want)
		if err != nil {
			return fmt.Errorf("runner: declaring %s's listeners: %w", name, err)
		}
		for _, w := range want {
			if !slices.ContainsFunc(got, func(l agent.Listener) bool { return l.Proto == w.Proto && l.Port == w.Port }) {
				return fmt.Errorf("runner: %s couldn't open its %s listener on port %d", name, w.Proto, w.Port)
			}
		}
		r.gens[name] = gen
	}
	return nil
}

// moved reads each node's generation again, on the session that declared
// it. A changed generation, or a session that's gone, makes what that node
// served UNKNOWN.
func (r *runner) moved(ctx context.Context) map[string]string {
	out := map[string]string{}
	for name, s := range r.sessions {
		_, gen, err := s.Listeners(ctx)
		switch {
		case err != nil:
			out[name] = fmt.Sprintf("lost the session with %s's agent, so its listeners can't be vouched for: %v", name, err)
		case gen != r.gens[name]:
			out[name] = fmt.Sprintf("%s's listeners changed during the run (another tailsuite process declared a different set)", name)
		}
	}
	return out
}

func (r *runner) pair(ctx context.Context, pt suite.Planned, pp suite.PlannedPair) PairResult {
	pr := PairResult{From: pp.From, To: pp.To, Probe: pp.Probe}
	if len(pp.Needs.Refused) > 0 {
		pr.Result, pr.Reason = Refused, strings.Join(pp.Needs.Refused, "; ")
		return pr
	}
	for _, proto := range pt.Protocols {
		pr.Halves = append(pr.Halves, r.half(ctx, pt, pp, proto))
	}
	pr.Result, pr.Reason = judge(pt.Expect, pr.Halves)
	return pr
}

func (r *runner) half(ctx context.Context, pt suite.Planned, pp suite.PlannedPair, proto string) HalfResult {
	h := HalfResult{Protocol: proto}
	if pp.Probe == "ssh" {
		h.Saw, h.Reason = sawUnknown, "there's no SSH probe yet (#59)"
		return h
	}
	if proto != "tcp" {
		h.Saw, h.Reason = sawUnknown, fmt.Sprintf("there's no %s probe yet (#56)", proto)
		return h
	}
	res, err := r.probe(ctx, pp.From, pp.To, pt.Port, pp.Probe)
	if err != nil {
		h.Saw, h.Reason = sawUnknown, err.Error()
		return h
	}
	h.Outcome = &res
	h.Saw, h.Reason = read(res, pp.To)
	// A block the target's own filter says shouldn't happen isn't the
	// policy's: it's the lab (a peer not yet set up, a lost packet). It
	// can't be a proven deny.
	if h.Saw == sawBlocked && slices.Contains(r.admitted(ctx, pp.To, uint16(pt.Port)), pp.From) { //nolint:gosec // G115: checked when the suite loaded
		h.Saw, h.Reason = sawUnknown, "blocked, but "+pp.To+"'s compiled filter admits "+pp.From+" on that port, so the block isn't the policy's"
	}
	if h.Saw == sawBlocked {
		c := r.control(ctx, pp, pt.Port)
		h.Control = c
		if !c.Alive {
			h.Saw, h.Reason = sawUnknown, "blocked, but the positive control couldn't show "+pp.To+" alive on that port, so a dead service can't be ruled out"
		}
	}
	return h
}

func (r *runner) probe(ctx context.Context, from, to string, port int, proto string) (agent.ProbeResult, error) {
	src, ok := r.sessions[from]
	if !ok {
		return agent.ProbeResult{}, fmt.Errorf("no session with %s", from)
	}
	dst, ok := r.nodes[to]
	if !ok || !dst.IPv4.IsValid() {
		return agent.ProbeResult{}, fmt.Errorf("%s has no tailnet address in the lab", to)
	}
	spec := agent.ProbeSpec{Proto: proto, Host: dst.IPv4.String(), Port: port, Timeout: agent.Duration(r.cfg.ProbeTimeout)}
	if proto == "http" {
		spec.Path = "/"
	}
	return src.Probe(ctx, spec)
}

// read turns an agent's outcome into what the traffic did. A deny has two
// forms (0025): a drop between peers is a timeout; between nodes the
// policy gives no path, there's no route at all.
func read(res agent.ProbeResult, to string) (saw, reason string) {
	switch {
	case res.Outcome == agent.Success && res.Peer == to:
		return sawReached, ""
	case res.Outcome == agent.Success:
		return sawUnknown, fmt.Sprintf("something answered, but not %s (peer %q)", to, res.Peer)
	case res.Outcome == agent.Timeout && !res.Connected:
		return sawBlocked, "dropped: no answer"
	case res.Outcome == agent.Timeout:
		return sawUnknown, "the connection opened, then nothing answered"
	case res.Outcome == agent.Error && strings.Contains(res.Detail, "network is unreachable"):
		return sawBlocked, "no route: the policy gives the two devices no path, so they aren't peers"
	case res.Outcome == agent.Refused:
		return sawUnknown, "the target refused the connection: nothing listened, though lab.yaml declares a listener there"
	default:
		return sawUnknown, "the probe failed: " + res.Detail
	}
}

// control proves the target is alive on the port (0001): from a lab node
// the target's compiled filter admits on that port, other than the
// test's source, or else from the target itself, whose traffic to its own
// address never meets the filter. It's done once per target and port.
func (r *runner) control(ctx context.Context, pp suite.PlannedPair, port int) *Control {
	key := fmt.Sprintf("%s:%d:%s", pp.To, port, pp.Probe)
	r.mu.Lock()
	if c, ok := r.controls[key]; ok {
		r.mu.Unlock()
		return c
	}
	r.mu.Unlock()

	c := &Control{}
	try := func(from string) bool {
		res, err := r.probe(ctx, from, pp.To, port, pp.Probe)
		if err != nil {
			return false
		}
		c.From, c.Outcome = from, &res
		saw, _ := read(res, pp.To)
		c.Alive = saw == sawReached
		return c.Alive
	}
	allowed := r.admitted(ctx, pp.To, uint16(port)) //nolint:gosec // G115: a port, checked when the suite loaded
	ok := false
	for _, from := range allowed {
		if from != pp.From && from != pp.To && try(from) {
			ok = true
			break
		}
	}
	if !ok {
		try(pp.To)
	}
	r.mu.Lock()
	r.controls[key] = c
	r.mu.Unlock()
	return c
}

// admitted lists the lab nodes whose addresses the target's compiled
// filter admits on port, by name.
func (r *runner) admitted(ctx context.Context, to string, port uint16) []string {
	r.mu.Lock()
	f, ok := r.filters[to]
	r.mu.Unlock()
	if !ok {
		var err error
		if f, err = r.cfg.Lab.CompiledFilter(ctx, r.nodes[to]); err != nil {
			return nil
		}
		r.mu.Lock()
		r.filters[to] = f
		r.mu.Unlock()
	}
	addrs := map[string]netip.Addr{}
	for name, n := range r.nodes {
		addrs[name] = n.IPv4
	}
	return filterAdmits(f, port, addrs, r.nodes[to].IPv4)
}

// filterAdmits lists, by name, the nodes a filter certainly admits on a
// port to the target's address.
func filterAdmits(f engine.Filter, port uint16, nodes map[string]netip.Addr, target netip.Addr) []string {
	var out []string
	for name, a := range nodes {
		if slices.ContainsFunc(f.Rules, func(fr engine.FilterRule) bool {
			return reachesOn(fr, port, target) && admitsOnly(fr, a)
		}) {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// reachesOn says whether a rule allows TCP to port on the target's
// address, not only on some destination.
func reachesOn(fr engine.FilterRule, port uint16, target netip.Addr) bool {
	if len(fr.IPProto) > 0 && !slices.Contains(fr.IPProto, 6) {
		return false
	}
	for _, d := range fr.DstPorts {
		if d.Ports.First > port || port > d.Ports.Last {
			continue
		}
		if d.IP == "*" || admitsOnly(engine.FilterRule{SrcIPs: []string{d.IP}}, target) {
			return true
		}
	}
	return false
}

// admitsOnly is SrcAdmits without its benefit of the doubt: a control
// source is one the filter certainly admits.
func admitsOnly(fr engine.FilterRule, a netip.Addr) bool {
	for _, s := range fr.SrcIPs {
		if s == "*" {
			return true
		}
		if p, err := netip.ParsePrefix(s); err == nil && p.Contains(a) {
			return true
		}
		if b, err := netip.ParseAddr(s); err == nil && a == b {
			return true
		}
		if lo, hi, ok := strings.Cut(s, "-"); ok {
			l, e1 := netip.ParseAddr(lo)
			h, e2 := netip.ParseAddr(hi)
			if e1 == nil && e2 == nil && l.Compare(a) <= 0 && a.Compare(h) <= 0 {
				return true
			}
		}
	}
	return false
}

// judge turns a pair's halves into its result (0015). For a test with two
// protocols, an allow passes when either half got through and fails when
// both were blocked; a deny passes when both were blocked and fails when
// either got through. Anything between is UNKNOWN.
func judge(expect string, halves []HalfResult) (Result, string) {
	count := func(saw string) int {
		n := 0
		for _, h := range halves {
			if h.Saw == saw {
				n++
			}
		}
		return n
	}
	reasons := func() string {
		var out []string
		for _, h := range halves {
			if h.Reason != "" {
				out = append(out, h.Protocol+": "+h.Reason)
			}
		}
		return strings.Join(out, "; ")
	}
	reached, blocked := count(sawReached), count(sawBlocked)
	switch expect {
	case suite.Allow:
		switch {
		case reached > 0:
			return Pass, ""
		case blocked == len(halves):
			return Fail, "expected allow, and the traffic was blocked: " + reasons()
		}
	case suite.Deny:
		switch {
		case reached > 0:
			return Fail, "expected deny, and the traffic got through"
		case blocked == len(halves):
			return Pass, ""
		}
	default:
		return Refused, "expect " + expect + " needs SSH (M3)"
	}
	return Unknown, reasons()
}

// combine turns a test's pairs into its result (0015): any FAIL makes it a
// FAIL; otherwise any REFUSED makes it REFUSED; otherwise any UNKNOWN; it's
// a PASS only when every pair passed.
func combine(pairs []PairResult) (Result, string) {
	for _, want := range []Result{Fail, Refused, Unknown} {
		for _, p := range pairs {
			if p.Result == want {
				return want, p.From + " → " + p.To + ": " + p.Reason
			}
		}
	}
	if len(pairs) == 0 {
		return Unknown, "no device pair to probe"
	}
	return Pass, ""
}

// exitCode is 0015's: 1 for a FAIL; 2 when nothing was proven, or when
// anything is unproven under strict; else 0.
func exitCode(s Summary, strict bool) int {
	switch {
	case s.Fail > 0:
		return 1
	case s.Pass == 0:
		return 2
	case strict && s.Unknown+s.Refused > 0:
		return 2
	}
	return 0
}
