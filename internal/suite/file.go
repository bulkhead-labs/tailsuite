// Package suite is a project's test suite (decision 0015): tailsuite's own
// tests from tests/*.yaml, and the policy's own tests and sshTests, mapped
// onto the same shape. It resolves each test's identities to lab devices,
// computes what each device pair needs (0016), labels the tests, and
// selects them by profile and flags.
package suite

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// FileVersion is the tests/*.yaml version this tailsuite reads.
const FileVersion = 1

// Expectations.
const (
	Allow = "allow"
	Deny  = "deny"
	Check = "check" // an SSH test only: the server demands a fresh login
)

// Sources, as the source: labels say.
const (
	SourceTests  = "tests"
	SourcePolicy = "policy"
)

// Test is one test, from a file in tests/ or from the policy.
type Test struct {
	Name   string   `json:"name"`
	Labels []string `json:"labels,omitempty"` // the user's own
	From   string   `json:"from"`
	To     string   `json:"to"`
	Port   int      `json:"port,omitempty"`
	// Probe forces a protocol; empty means whatever listens on the port.
	Probe string `json:"probe,omitempty"`
	// SSH is the login an SSH test tries.
	SSH    string `json:"ssh,omitempty"`
	Expect string `json:"expect"`
	// When pins a condition the source device meets, such as
	// {"os": "windows"}.
	When map[string]string `json:"when,omitempty"`

	// Source is SourceTests or SourcePolicy.
	Source string `json:"source"`
	// Where is the file and line, or the policy pointer, it came from.
	Where string `json:"where"`
	// Protocols are the halves a policy test asserts ("tcp", "udp"); a
	// test from tests/ is TCP only.
	Protocols []string `json:"protocols,omitempty"`
}

type fileDoc struct {
	Version int        `yaml:"version"`
	Tests   []fileTest `yaml:"tests"`
}

type fileTest struct {
	Name   string            `yaml:"name"`
	Labels []string          `yaml:"labels"`
	From   string            `yaml:"from"`
	To     string            `yaml:"to"`
	Port   int               `yaml:"port"`
	Probe  string            `yaml:"probe"`
	SSH    string            `yaml:"ssh"`
	Expect string            `yaml:"expect"`
	When   map[string]string `yaml:"when"`
	line   int
}

// testKeys are a test's keys. A node's Decode doesn't inherit the
// decoder's KnownFields, so UnmarshalYAML checks them itself.
var testKeys = []string{"name", "labels", "from", "to", "port", "probe", "ssh", "expect", "when"}

// UnmarshalYAML keeps the test's line, for errors and results, and
// refuses a key it doesn't know: a typo would otherwise be ignored.
func (t *fileTest) UnmarshalYAML(n *yaml.Node) error {
	if n.Kind != yaml.MappingNode {
		return fmt.Errorf("line %d: a test is a mapping of name, from, to, port and expect", n.Line)
	}
	for i := 0; i+1 < len(n.Content); i += 2 {
		if k := n.Content[i].Value; !slices.Contains(testKeys, k) {
			return fmt.Errorf("line %d: field %s not found in a test; want %s", n.Content[i].Line, k, strings.Join(testKeys, ", "))
		}
	}
	type plain fileTest
	var p plain
	if err := n.Decode(&p); err != nil {
		return err
	}
	*t = fileTest(p)
	t.line = n.Line
	return nil
}

// Probes a test may force. UDP and ICMP come after M1 (#56).
var probes = []string{"tcp", "http"}

// whenKeys are the conditions a test may pin. The lab can build an OS;
// the rest come with posture (M4).
var whenKeys = []string{"os"}

// ReadDir reads every tests/*.yaml file in a project, sorted by name. A
// missing tests/ folder is an empty suite.
func ReadDir(dir string) ([]Test, error) {
	paths, err := filepath.Glob(filepath.Join(dir, "tests", "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(paths)
	var all []Test
	var errs []error
	for _, p := range paths {
		b, err := os.ReadFile(p) //nolint:gosec // G304: the user's own project
		if err != nil {
			return nil, fmt.Errorf("tests: %w", err)
		}
		rel, _ := filepath.Rel(dir, p)
		ts, err := ReadFile(b, filepath.ToSlash(rel))
		if err != nil {
			errs = append(errs, err)
			continue
		}
		all = append(all, ts...)
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return all, nil
}

// ReadFile reads one test file strictly: an unknown key, a second YAML
// document, or a test that doesn't make sense is an error naming the file
// and line.
func ReadFile(b []byte, name string) ([]Test, error) {
	dec := yaml.NewDecoder(bytes.NewReader(b))
	dec.KnownFields(true)
	var doc fileDoc
	if err := dec.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return nil, fmt.Errorf("%s: empty; want version: 1 and tests", name)
		}
		return nil, fmt.Errorf("%s: %w", name, err)
	}
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return nil, fmt.Errorf("%s: more than one YAML document; want one", name)
	}
	if doc.Version != FileVersion {
		return nil, fmt.Errorf("%s: version %d; this tailsuite reads version %d", name, doc.Version, FileVersion)
	}
	var out []Test
	var errs []error
	for _, ft := range doc.Tests {
		where := fmt.Sprintf("%s:%d", name, ft.line)
		bad := func(format string, args ...any) {
			errs = append(errs, fmt.Errorf("%s: test %q: "+format, append([]any{where, ft.Name}, args...)...))
		}
		switch {
		case strings.TrimSpace(ft.Name) == "":
			errs = append(errs, fmt.Errorf("%s: a test needs a name", where))
			continue
		case ft.From == "" || ft.To == "":
			bad("needs from and to")
		case ft.Port < 1 || ft.Port > 65535:
			bad("port %d; want 1 to 65535", ft.Port)
		}
		switch {
		case ft.SSH != "":
			if ft.Expect != Allow && ft.Expect != Deny && ft.Expect != Check {
				bad("expect %q; an SSH test expects allow, deny or check", ft.Expect)
			}
			if ft.Probe != "" {
				bad("an SSH test has no probe: its probe is SSH")
			}
		case ft.Expect != Allow && ft.Expect != Deny:
			bad("expect %q; want allow or deny (check is for SSH tests)", ft.Expect)
		}
		if ft.Probe != "" && !slices.Contains(probes, ft.Probe) {
			bad("probe %q isn't supported yet; want %s (#56 brings more)", ft.Probe, strings.Join(probes, " or "))
		}
		for k := range ft.When {
			if !slices.Contains(whenKeys, k) {
				bad("when: %q; want %s (posture attributes come in M4)", k, strings.Join(whenKeys, ", "))
			}
		}
		for _, l := range ft.Labels {
			if strings.Contains(l, ":") {
				bad("label %q: a label with \":\" is tailsuite's own (source:, probe:, needs:)", l)
			}
		}
		out = append(out, Test{
			Name: ft.Name, Labels: ft.Labels, From: ft.From, To: ft.To, Port: ft.Port,
			Probe: ft.Probe, SSH: ft.SSH, Expect: ft.Expect, When: ft.When,
			Source: SourceTests, Where: where, Protocols: []string{"tcp"},
		})
	}
	if err := errors.Join(errs...); err != nil {
		return nil, err
	}
	return out, nil
}
