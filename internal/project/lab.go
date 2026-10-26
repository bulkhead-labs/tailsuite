package project

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"regexp"
	"slices"
	"strings"

	"go.yaml.in/yaml/v3"
)

// LabVersion is the lab.yaml version this tailsuite reads.
const LabVersion = 1

// Lab is lab.yaml: the devices as the lab rebuilds them, what listens on
// them, and the profiles. It's never deployed, and production never
// reads it (0014).
type Lab struct {
	Version  int                  `yaml:"version"`
	Devices  map[string]LabDevice `yaml:"devices"`
	Users    map[string]LabUser   `yaml:"users"`
	Profiles map[string]Profile   `yaml:"profiles"`
	// Up is how `tailsuite up` starts by default; its flags override it.
	Up Up `yaml:"up"`
}

// Up is lab.yaml's defaults for `tailsuite up` (#81).
type Up struct {
	// Detach runs the lab in a background process, as --detach does.
	Detach bool `yaml:"detach"`
	// Clean tears down this project's earlier labs first, as --clean does.
	Clean bool `yaml:"clean"`
}

// LabUser is a user's entry in lab.yaml, by login. Its roles are what
// role autogroups (autogroup:admin…) lower to on headscale (0016).
type LabUser struct {
	Roles []string `yaml:"roles"`
}

// LabDevice is one device in lab.yaml.
type LabDevice struct {
	// User added the device; empty for a tagged device.
	User string `yaml:"user"`
	// OS is what the device reports: linux, macos, windows, ios or android.
	OS string `yaml:"os"`
	// Version is its Tailscale client's version.
	Version string `yaml:"version"`
	// IP is its Tailscale IPv4 address, pinned.
	IP string `yaml:"ip"`
	// Listen is what listens on it in the lab, explicitly (0014).
	Listen []Listen `yaml:"listen"`
}

// Listen is one listen: entry, such as {http: 80}.
type Listen struct {
	Proto string
	Port  int
}

// UnmarshalYAML reads a one-key mapping, protocol to port.
func (l *Listen) UnmarshalYAML(n *yaml.Node) error {
	var m map[string]int
	if err := n.Decode(&m); err != nil || len(m) != 1 {
		return fmt.Errorf("line %d: a listen entry is one protocol and its port, such as {http: 80}", n.Line)
	}
	for proto, port := range m {
		*l = Listen{Proto: proto, Port: port}
	}
	return nil
}

// Profile is a named way to run (0015).
type Profile struct {
	Engine   string   `yaml:"engine"`
	Only     []string `yaml:"only"`
	Skip     []string `yaml:"skip"`
	Unproven string   `yaml:"unproven"`
}

// Values the lab accepts. Listeners are what the node agent serves (decision
// 0020, in #53); more come with #56.
var (
	osNames      = []string{"linux", "macos", "windows", "ios", "android"}
	listenProtos = []string{"tcp", "http"}
	engines      = []string{"headscale", "tailscale"}
	roles        = []string{"owner", "admin", "it-admin", "network-admin", "billing-admin", "auditor", "member"}
	unproven     = []string{"report", "fail"}

	dnsLabel = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	version  = regexp.MustCompile(`^[0-9]+\.[0-9]+(\.[0-9]+)?$`)
	login    = regexp.MustCompile(`^[^@:\s]+@[^@\s]+$`) // not group:… or tag:…
	tagName  = regexp.MustCompile(`^tag:[A-Za-z0-9][A-Za-z0-9-]*$`)
	cgnat    = netip.MustParsePrefix("100.64.0.0/10")
)

// ReadLab reads lab.yaml strictly: an unknown key is an error, since the
// file is tailsuite's, and a typo would otherwise be ignored.
func ReadLab(r io.Reader) (Lab, error) {
	var lab Lab
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	if err := dec.Decode(&lab); err != nil {
		if errors.Is(err, io.EOF) {
			return lab, errors.New("lab.yaml is empty")
		}
		return lab, fmt.Errorf("lab.yaml: %w", err)
	}
	// One document only: a second would be ignored, typos and all.
	var more yaml.Node
	if err := dec.Decode(&more); !errors.Is(err, io.EOF) {
		return lab, errors.New("lab.yaml: more than one YAML document; want one")
	}
	return lab, lab.check()
}

func (lab Lab) check() error {
	var errs []error
	bad := func(format string, args ...any) { errs = append(errs, fmt.Errorf("lab.yaml: "+format, args...)) }
	if lab.Version != LabVersion {
		bad("version %d; this tailsuite reads version %d", lab.Version, LabVersion)
	}
	for _, name := range sortedKeys(lab.Devices) {
		d := lab.Devices[name]
		if !dnsLabel.MatchString(name) {
			bad("device %q: a name is a MagicDNS label: lower-case letters, digits and hyphens", name)
		}
		if d.User != "" && !login.MatchString(d.User) {
			bad("device %s: user %q isn't a login, such as alice@example.com", name, d.User)
		}
		if d.OS != "" && !slices.Contains(osNames, d.OS) {
			bad("device %s: os %q; want one of %s", name, d.OS, strings.Join(osNames, ", "))
		}
		if d.Version != "" && !version.MatchString(d.Version) {
			bad("device %s: version %q; want a client version, such as 1.102.5", name, d.Version)
		}
		if d.IP != "" {
			if a, err := netip.ParseAddr(d.IP); err != nil || !a.Is4() || !cgnat.Contains(a) {
				bad("device %s: ip %q; want a Tailscale IPv4 address, in 100.64.0.0/10", name, d.IP)
			}
		}
		seen := map[Listen]bool{}
		for _, l := range d.Listen {
			if !slices.Contains(listenProtos, l.Proto) {
				bad("device %s: listen %s isn't supported yet; want %s (#56 brings more)", name, l.Proto, strings.Join(listenProtos, ", "))
			}
			if l.Port < 1 || l.Port > 65535 {
				bad("device %s: listen %s port %d out of range", name, l.Proto, l.Port)
			}
			if seen[Listen{Port: l.Port}] {
				bad("device %s: two listeners on port %d", name, l.Port)
			}
			seen[Listen{Port: l.Port}] = true
		}
	}
	for _, name := range sortedKeys(lab.Users) {
		if !login.MatchString(name) {
			bad("user %q isn't a login, such as alice@example.com", name)
		}
		seen := map[string]bool{}
		for _, r := range lab.Users[name].Roles {
			if !slices.Contains(roles, r) {
				bad("user %s: role %q; want one of %s", name, r, strings.Join(roles, ", "))
			}
			if seen[r] {
				bad("user %s: role %s twice", name, r)
			}
			seen[r] = true
		}
	}
	for _, name := range sortedKeys(lab.Profiles) {
		p := lab.Profiles[name]
		if p.Engine != "" && !slices.Contains(engines, p.Engine) {
			bad("profile %s: engine %q; want one of %s", name, p.Engine, strings.Join(engines, ", "))
		}
		if p.Unproven != "" && !slices.Contains(unproven, p.Unproven) {
			bad("profile %s: unproven %q; want report or fail", name, p.Unproven)
		}
	}
	return errors.Join(errs...)
}

func readLabBytes(b []byte) (Lab, error) { return ReadLab(bytes.NewReader(b)) }

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	return keys
}
