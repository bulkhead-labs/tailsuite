package suite

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/project"
)

// Planned is a test as a run sees it, before anything is probed.
type Planned struct {
	Test
	// AllLabels are the user's labels and tailsuite's (source:, probe:,
	// needs:), sorted.
	AllLabels []string `json:"labels"`
	// Skipped says why a profile or flag left the test out; empty when
	// it runs. A skipped test is counted, never dropped (0015).
	Skipped string `json:"skipped,omitempty"`
	// Unknown is set when the whole test can't be proven in this lab,
	// such as a policy test naming no lab device.
	Unknown string        `json:"unknown,omitempty"`
	Pairs   []PlannedPair `json:"pairs,omitempty"`
}

// PlannedPair is one device pair of a test.
type PlannedPair struct {
	Pair
	// Probe is the protocol to probe with: the test's, or what listens
	// on the port in lab.yaml.
	Probe string `json:"probe"`
	Needs Needs  `json:"-"`
}

// LoadError is a suite that can't run: exit 3 (0015). It lists every
// problem found.
type LoadError struct{ Problems []error }

func (e *LoadError) Error() string {
	msgs := make([]string, len(e.Problems))
	for i, p := range e.Problems {
		msgs[i] = p.Error()
	}
	return "the suite can't load:\n  " + strings.Join(msgs, "\n  ")
}

// Build plans every test against the project and one engine: it resolves
// identities, picks each pair's probe, computes needs, and labels. Tests
// from tests/ that can't be resolved are load errors; the policy's own
// are UNKNOWN instead, since the user didn't write them for the lab.
func Build(p *project.Project, tests []Test, caps engine.Capabilities, low engine.Lowering) ([]Planned, error) {
	r := NewResolver(p.Tailnet)
	pl := NewPlanner(p.Tailnet, caps, low)
	var out []Planned
	var problems []error
	seen := map[string]string{}
	for _, t := range tests {
		if first, dup := seen[t.Name]; dup {
			problems = append(problems, fmt.Errorf("%s: test %q: the name is taken, by the test at %s; names are unique in a suite", t.Where, t.Name, first))
			continue
		}
		seen[t.Name] = t.Where
		pt := Planned{Test: t}
		fail := func(err error) {
			if t.Source == SourcePolicy {
				if pt.Unknown == "" {
					pt.Unknown = err.Error()
				}
				return
			}
			problems = append(problems, fmt.Errorf("%s: test %q: %w", t.Where, t.Name, err))
		}
		from, errFrom := r.Resolve(t.From)
		to, errTo := r.Resolve(t.To)
		switch {
		case errFrom != nil:
			fail(fmt.Errorf("from %w", errFrom))
		case errTo != nil:
			fail(fmt.Errorf("to %w", errTo))
		case t.Port == 0:
			fail(fmt.Errorf("its port can't be probed: a test needs one port"))
		default:
			pairs := r.Pairs(from, to)
			if len(pairs) == 0 {
				fail(errors.New("its source and destination are the same device, whose traffic to itself never meets a filter"))
			}
			for _, pair := range pairs {
				probe, err := probeFor(p.Lab, t, pair.To)
				if err != nil {
					fail(err)
					continue
				}
				pt.Pairs = append(pt.Pairs, PlannedPair{Pair: pair, Probe: probe, Needs: pl.Needs(t, pair)})
			}
		}
		pt.AllLabels = labels(pt)
		out = append(out, pt)
	}
	if len(problems) > 0 {
		return nil, &LoadError{Problems: problems}
	}
	return out, nil
}

// probeFor picks a pair's probe: the test's own, or the protocol of what
// lab.yaml says listens on the port. A port where nothing listens can't
// tell a deny from a dead service.
func probeFor(lab project.Lab, t Test, device string) (string, error) {
	if t.SSH != "" {
		return "ssh", nil
	}
	for _, l := range lab.Devices[device].Listen {
		if l.Port == t.Port {
			if t.Probe != "" {
				return t.Probe, nil
			}
			return l.Proto, nil
		}
	}
	return "", fmt.Errorf("nothing listens on %s port %d in the lab; add it to lab.yaml, such as `listen: [{ tcp: %d }]` under devices.%s", device, t.Port, t.Port, device)
}

func labels(pt Planned) []string {
	set := map[string]bool{"source:" + pt.Source: true}
	for _, l := range pt.Labels {
		set[l] = true
	}
	for _, p := range pt.Pairs {
		set["probe:"+p.Probe] = true
		for _, c := range p.Needs.All {
			set["needs:"+string(c)] = true
		}
	}
	out := make([]string, 0, len(set))
	for l := range set {
		out = append(out, l)
	}
	slices.Sort(out)
	return out
}

// Options choose what runs (0015). Flags override the profile.
type Options struct {
	Profile  string
	Engine   string
	Only     []string
	Skip     []string
	Run      string
	Strict   bool
	Unproven string // report or fail
}

// Resolve applies a profile from lab.yaml under flags. With no profile
// named, the one called default applies; with none, headscale, every
// test, and unproven: report.
func Resolve(lab project.Lab, flags Options) (Options, error) {
	name := flags.Profile
	prof, ok := lab.Profiles[name]
	switch {
	case name == "":
		name = "default"
		prof = lab.Profiles["default"]
	case !ok:
		return Options{}, fmt.Errorf("no profile %q in lab.yaml", name)
	}
	o := Options{Profile: name, Engine: prof.Engine, Only: prof.Only, Skip: prof.Skip, Unproven: prof.Unproven}
	if flags.Engine != "" {
		o.Engine = flags.Engine
	}
	if len(flags.Only) > 0 {
		o.Only = flags.Only
	}
	if len(flags.Skip) > 0 {
		o.Skip = flags.Skip
	}
	o.Run = flags.Run
	if o.Engine == "" {
		o.Engine = "headscale"
	}
	if o.Unproven == "" {
		o.Unproven = "report"
	}
	o.Strict = flags.Strict || o.Unproven == "fail"
	return o, nil
}

// Select marks what the options leave out. An --only label or a --run
// pattern that matches no test is an error (exit 3): a typo would
// otherwise run nothing. A --skip label no test has is only a warning.
func Select(tests []Planned, o Options) (warnings []string, err error) {
	var re *regexp.Regexp
	if o.Run != "" {
		if re, err = regexp.Compile(o.Run); err != nil {
			return nil, &LoadError{Problems: []error{fmt.Errorf("--run %q: %w", o.Run, err)}}
		}
	}
	has := func(label string) bool {
		return slices.ContainsFunc(tests, func(t Planned) bool { return slices.Contains(t.AllLabels, label) })
	}
	var problems []error
	for _, l := range o.Only {
		if !has(l) {
			problems = append(problems, fmt.Errorf("only %q matches no test (profile %s, or --only)", l, o.Profile))
		}
	}
	for _, l := range o.Skip {
		if !has(l) {
			warnings = append(warnings, fmt.Sprintf("skip %q matches no test", l))
		}
	}
	if re != nil && !slices.ContainsFunc(tests, func(t Planned) bool { return re.MatchString(t.Name) }) {
		problems = append(problems, fmt.Errorf("--run %q matches no test name", o.Run))
	}
	if len(problems) > 0 {
		return warnings, &LoadError{Problems: problems}
	}
	for i := range tests {
		t := &tests[i]
		switch {
		case re != nil && !re.MatchString(t.Name):
			t.Skipped = "--run " + o.Run
		case len(o.Only) > 0 && !slices.ContainsFunc(o.Only, func(l string) bool { return slices.Contains(t.AllLabels, l) }):
			t.Skipped = "only " + strings.Join(o.Only, ", ")
		}
		for _, l := range o.Skip {
			if slices.Contains(t.AllLabels, l) {
				t.Skipped = "skip " + l // --skip wins over --only
			}
		}
	}
	return warnings, nil
}

// Warnings are 0015's: a test that no profile ever runs, and a test that
// every profile's engine refuses, so nothing ever proves it. Needs were
// computed for one engine, so the second is said only when every profile
// uses that engine.
func Warnings(tests []Planned, lab project.Lab, engineName string) []string {
	profiles := lab.Profiles
	if len(profiles) == 0 {
		profiles = map[string]project.Profile{"default": {}}
	}
	sameEngine := true
	for _, p := range profiles {
		e := p.Engine
		if e == "" {
			e = "headscale"
		}
		if e != engineName {
			sameEngine = false
		}
	}
	var out []string
	for _, t := range tests {
		runs := false
		for _, p := range profiles {
			if selects(t, p.Only, p.Skip) {
				runs = true
			}
		}
		if !runs {
			out = append(out, fmt.Sprintf("test %q: no profile in lab.yaml runs it", t.Name))
			continue
		}
		if sameEngine && len(t.Pairs) > 0 && !slices.ContainsFunc(t.Pairs, func(p PlannedPair) bool { return len(p.Needs.Refused) == 0 }) {
			out = append(out, fmt.Sprintf("test %q: every profile's engine (%s) refuses it, so nothing ever proves it", t.Name, engineName))
		}
	}
	return out
}

func selects(t Planned, only, skip []string) bool {
	for _, l := range skip {
		if slices.Contains(t.AllLabels, l) {
			return false
		}
	}
	return len(only) == 0 || slices.ContainsFunc(only, func(l string) bool { return slices.Contains(t.AllLabels, l) })
}
