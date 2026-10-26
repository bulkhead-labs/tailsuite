package engine

import (
	"encoding/json"
	"fmt"
	"net/netip"
	"slices"
	"strings"
)

// Filter is a node's compiled packet filter, as tailscaled holds it
// (`tailscale debug netmap`'s PacketFilterRules). Rules' CapGrant entries
// (application capabilities) aren't read: app grants are refused, so no
// result relies on them. The conformance check (#42) must read them.
type Filter struct {
	Rules []FilterRule `json:"rules"`
}

// FilterRule is one rule: traffic from SrcIPs to DstPorts is allowed.
type FilterRule struct {
	// SrcIPs are "*", an address, a prefix or a range ("a-b").
	SrcIPs []string `json:"srcIPs"`
	// DstPorts are where it may go.
	DstPorts []DstPort `json:"dstPorts"`
	// IPProto lists IP protocol numbers; empty means TCP, UDP and ICMP.
	IPProto []int `json:"ipProto,omitempty"`
}

// DstPort is a destination and its ports.
type DstPort struct {
	IP    string    `json:"ip"`
	Ports PortRange `json:"ports"`
}

// PortRange is an inclusive range of ports.
type PortRange struct {
	First uint16 `json:"first"`
	Last  uint16 `json:"last"`
}

// Netmap is what readiness needs from a node's network map.
type Netmap struct {
	// Self are the node's own tailnet addresses.
	Self []netip.Addr
	// Peers are the addresses of each peer the node has.
	Peers [][]netip.Addr
	// Filter is the node's compiled filter.
	Filter Filter
}

// ParseNetmap reads the JSON `tailscale debug netmap` prints.
func ParseNetmap(b []byte) (Netmap, error) {
	var raw struct {
		SelfNode struct {
			Addresses []string
		}
		Peers []struct {
			Addresses []string
		}
		PacketFilterRules []struct {
			SrcIPs   []string
			DstPorts []struct {
				IP    string
				Ports struct{ First, Last uint16 }
			}
			IPProto []int
		}
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return Netmap{}, fmt.Errorf("reading the netmap: %w", err)
	}
	var nm Netmap
	var err error
	if nm.Self, err = prefixAddrs(raw.SelfNode.Addresses); err != nil {
		return Netmap{}, err
	}
	for _, p := range raw.Peers {
		addrs, err := prefixAddrs(p.Addresses)
		if err != nil {
			return Netmap{}, err
		}
		nm.Peers = append(nm.Peers, addrs)
	}
	nm.Filter.Rules = []FilterRule{}
	for _, r := range raw.PacketFilterRules {
		fr := FilterRule{SrcIPs: r.SrcIPs, IPProto: r.IPProto}
		for _, d := range r.DstPorts {
			fr.DstPorts = append(fr.DstPorts, DstPort{IP: d.IP, Ports: PortRange{First: d.Ports.First, Last: d.Ports.Last}})
		}
		nm.Filter.Rules = append(nm.Filter.Rules, fr)
	}
	return nm, nil
}

// prefixAddrs reads a node's addresses, which a netmap writes as
// single-address prefixes ("100.64.0.1/32").
func prefixAddrs(ss []string) ([]netip.Addr, error) {
	var out []netip.Addr
	for _, s := range ss {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			return nil, fmt.Errorf("reading the netmap: address %q: %w", s, err)
		}
		out = append(out, p.Addr())
	}
	return out, nil
}

// SrcAdmits says whether a rule's sources include addr. A source it can't
// read admits everything. The readiness wait then expects more peers,
// never fewer: a peer it wrongly expects stops the run at the wait's
// bound (exit 3), while one it wrongly left out could let a probe start
// before the path exists, and time out like a deny.
func (r FilterRule) SrcAdmits(addr netip.Addr) bool {
	return slices.ContainsFunc(r.SrcIPs, func(s string) bool { return matchAddr(s, addr) })
}

// HasPort says whether the rule allows TCP to port on any destination.
func (r FilterRule) HasPort(port uint16) bool {
	if len(r.IPProto) > 0 && !slices.Contains(r.IPProto, 6) {
		return false
	}
	return slices.ContainsFunc(r.DstPorts, func(d DstPort) bool { return d.Ports.First <= port && port <= d.Ports.Last })
}

func matchAddr(s string, addr netip.Addr) bool {
	if s == "*" {
		return true
	}
	if lo, hi, ok := strings.Cut(s, "-"); ok {
		a, err1 := netip.ParseAddr(lo)
		b, err2 := netip.ParseAddr(hi)
		if err1 != nil || err2 != nil {
			return true // unreadable: see SrcAdmits
		}
		return a.Compare(addr) <= 0 && addr.Compare(b) <= 0
	}
	if p, err := netip.ParsePrefix(s); err == nil {
		return p.Contains(addr)
	}
	if a, err := netip.ParseAddr(s); err == nil {
		return a == addr
	}
	return true // unreadable: see SrcAdmits
}
