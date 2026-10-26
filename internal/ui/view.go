package ui

import (
	"fmt"
	"path/filepath"
	"slices"
	"strings"

	"github.com/bulkhead-labs/tailsuite/internal/project"
	"github.com/bulkhead-labs/tailsuite/internal/suite"
)

// View is the map drawn before any traffic: the lab's devices, and the
// paths the policy's rules say exist. None of it is proven; the page
// labels it unverified, and tests' results replace it path by path.
type View struct {
	Project string   `json:"project"`
	Devices []Device `json:"devices"`
	Paths   []Path   `json:"paths"`
	// Notes are rule entries the map can't draw, such as an IP set.
	Notes []string `json:"notes,omitempty"`
}

// Device is one lab device.
type Device struct {
	Name string   `json:"name"`
	User string   `json:"user,omitempty"`
	Tags []string `json:"tags,omitempty"`
	OS   string   `json:"os,omitempty"`
}

// Path is one source device reaching one destination, as the policy
// writes it: unverified.
type Path struct {
	From  string   `json:"from"`
	To    string   `json:"to"`
	Ports []string `json:"ports"`
}

// NewView draws the map from a project.
func NewView(p *project.Project) View {
	v := View{Project: filepath.Base(p.Dir)}
	for _, d := range p.Tailnet.Devices {
		v.Devices = append(v.Devices, Device{Name: d.Name, User: d.User, Tags: d.Tags, OS: d.OS})
	}
	if p.Tailnet.Policy == nil {
		return v
	}
	r := suite.NewResolver(p.Tailnet)
	paths := map[suite.Pair][]string{}
	resolve := func(rule, id string) (suite.Match, bool) {
		m, err := r.Resolve(id)
		if err != nil || len(m.Devices) == 0 {
			if note := fmt.Sprintf("%s: %s isn't drawn: it names no lab device", rule, id); !slices.Contains(v.Notes, note) {
				v.Notes = append(v.Notes, note)
			}
			return suite.Match{}, false
		}
		return m, true
	}
	add := func(rule string, srcs []string, dst string, ports []string) {
		to, ok := resolve(rule, dst)
		if !ok {
			return
		}
		for _, s := range srcs {
			from, ok := resolve(rule, s)
			if !ok {
				continue
			}
			for _, pr := range r.Pairs(from, to) {
				for _, port := range ports {
					if !slices.Contains(paths[pr], port) {
						paths[pr] = append(paths[pr], port)
					}
				}
			}
		}
	}
	pol := p.Tailnet.Policy.Policy
	for i, g := range pol.Grants {
		ports := g.IP
		if len(g.App) > 0 && len(ports) == 0 {
			ports = []string{"app"}
		}
		for _, d := range g.Dst {
			add(fmt.Sprintf("grant %d", i+1), g.Src, d, ports)
		}
	}
	for i, a := range pol.ACLs {
		if a.Action != "accept" {
			continue
		}
		srcs := append(slices.Clone(a.Src), a.Users...)
		for _, d := range append(slices.Clone(a.Dst), a.Ports...) {
			host, port, ok := cutPort(d)
			if !ok {
				v.Notes = append(v.Notes, fmt.Sprintf("acl %d: %s isn't drawn: no port", i+1, d))
				continue
			}
			add(fmt.Sprintf("acl %d", i+1), srcs, host, []string{port})
		}
	}
	for pr, ports := range paths {
		slices.Sort(ports)
		v.Paths = append(v.Paths, Path{From: pr.From, To: pr.To, Ports: ports})
	}
	slices.SortFunc(v.Paths, func(a, b Path) int {
		if c := strings.Compare(a.From, b.From); c != 0 {
			return c
		}
		return strings.Compare(a.To, b.To)
	})
	return v
}

// cutPort splits an ACL destination, host:port, at its last colon, so an
// IPv6 address keeps its own.
func cutPort(d string) (host, port string, ok bool) {
	i := strings.LastIndexByte(d, ':')
	if i <= 0 || i == len(d)-1 {
		return "", "", false
	}
	return d[:i], d[i+1:], true
}
