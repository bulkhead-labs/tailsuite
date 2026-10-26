package engine

import (
	"context"
	"errors"
	"net/netip"
	"strings"
	"testing"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/model/policy"
)

// A netmap as headscale 0.29.5 and tailscale v1.102.5 printed it, cut
// down, with an allow-all rule and two markers (spike, 2026-10-26).
const netmapJSON = `{
	"SelfNode": {"Addresses": ["100.64.0.2/32", "fd7a:115c:a1e0::2/128"]},
	"Peers": [{"Name": "a", "Addresses": ["100.64.0.1/32", "fd7a:115c:a1e0::1/128"]}],
	"PacketFilterRules": [
		{"DstPorts": [{"IP": "*", "Ports": {"First": 0, "Last": 65535}}], "SrcIPs": ["100.64.0.0-100.115.91.255", "100.115.94.0-100.127.255.255", "fd7a:115c:a1e0::/48"]},
		{"DstPorts": [{"IP": "*", "Ports": {"First": 41997, "Last": 41997}}], "IPProto": [6], "SrcIPs": ["192.0.2.77"]},
		{"DstPorts": [{"IP": "*", "Ports": {"First": 41996, "Last": 41996}}], "IPProto": [6], "SrcIPs": ["100.99.99.99"]}
	]
}`

func TestParseNetmap(t *testing.T) {
	nm, err := ParseNetmap([]byte(netmapJSON))
	if err != nil {
		t.Fatal(err)
	}
	if len(nm.Self) != 2 || nm.Self[0] != netip.MustParseAddr("100.64.0.2") || len(nm.Peers) != 1 || len(nm.Filter.Rules) != 3 {
		t.Fatalf("got %+v", nm)
	}
	if !nm.Filter.HasMarker(41997) || nm.Filter.HasMarker(41996) || nm.Filter.HasMarker(41998) {
		t.Error("marker: want 41997 only (41996's source isn't the marker's)")
	}
	r := nm.Filter.Rules[0]
	for addr, want := range map[string]bool{"100.64.0.1": true, "100.115.92.1": false, "fd7a:115c:a1e0::9": true, "192.0.2.77": false} {
		if got := r.SrcAdmits(netip.MustParseAddr(addr)); got != want {
			t.Errorf("allow-all admits %s: %v", addr, got)
		}
	}
	if nm.Filter.Rules[1].SrcAdmits(netip.MustParseAddr("100.64.0.1")) {
		t.Error("the marker admits a node")
	}
	// An empty filter is empty, not missing.
	nm, err = ParseNetmap([]byte(`{"SelfNode": {"Addresses": []}, "PacketFilterRules": null}`))
	if err != nil || nm.Filter.Rules == nil || len(nm.Filter.Rules) != 0 {
		t.Errorf("empty filter: %+v, %v", nm.Filter, err)
	}
}

func TestUnreadableSourceAdmitsEverything(t *testing.T) {
	// Expecting too many peers stops the run at the bound; too few could
	// let a probe start before the path exists.
	for _, src := range []string{"somethingnew", "some-range"} {
		r := FilterRule{SrcIPs: []string{src}}
		if !r.SrcAdmits(netip.MustParseAddr("100.64.0.1")) {
			t.Errorf("the unreadable source %q admits nothing", src)
		}
	}
}

func markedSections(t *testing.T, src string) policy.Policy {
	t.Helper()
	out, ops, err := WithMarker([]byte(src), 41234)
	if err != nil {
		t.Fatal(err)
	}
	for _, op := range ops {
		if op.Note == "" {
			t.Errorf("op %+v has no note", op)
		}
	}
	f, err := policy.Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "// kept") {
		t.Error("comment lost")
	}
	return f.Policy
}

func TestWithMarker(t *testing.T) {
	t.Run("neither section: the default made explicit", func(t *testing.T) {
		p := markedSections(t, "// kept\n{\"tagOwners\": {\"tag:a\": []}}")
		if len(p.Grants) != 2 || p.Grants[0].Src[0] != "*" || p.Grants[1].Src[0] != MarkerSource || p.ACLs != nil {
			t.Fatalf("grants %+v", p.Grants)
		}
	})
	t.Run("empty grants: no default (0021)", func(t *testing.T) {
		p := markedSections(t, "// kept\n{\"grants\": []}")
		if len(p.Grants) != 1 || p.Grants[0].Src[0] != MarkerSource || p.Grants[0].IP[0] != "tcp:41234" {
			t.Fatalf("grants %+v", p.Grants)
		}
	})
	t.Run("empty acls: no default", func(t *testing.T) {
		p := markedSections(t, "// kept\n{\"acls\": []}")
		if len(p.Grants) != 1 || p.ACLs == nil || len(p.ACLs) != 0 {
			t.Fatalf("%+v", p)
		}
	})
	t.Run("a null section", func(t *testing.T) {
		for _, src := range []string{`{"acls": null}`, `{"grants": null}`} {
			if _, _, err := WithMarker([]byte(src), 41234); err == nil {
				t.Errorf("%s: marked", src)
			}
		}
	})
	t.Run("the file's spelling", func(t *testing.T) {
		out, ops, err := WithMarker([]byte("// kept\n{\"Grants\": [{\"src\": [\"*\"], \"dst\": [\"*\"], \"ip\": [\"*\"]}]}"), 41234)
		if err != nil {
			t.Fatal(err)
		}
		if ops[0].Path != "/Grants/-" || strings.Contains(string(out), `"grants"`) {
			t.Fatalf("ops %+v\n%s", ops, out)
		}
	})
}

func TestNewMarkerPortAvoidsCurrentMarkers(t *testing.T) {
	var current []Filter
	for p := markerPortMin; p < markerPortMax-1; p++ {
		current = append(current, Filter{Rules: []FilterRule{{SrcIPs: []string{"192.0.2.77"}, DstPorts: []DstPort{{IP: "*", Ports: PortRange{uint16(p), uint16(p)}}}}}})
	}
	if got, err := NewMarkerPort(current); err != nil || got != markerPortMax-1 {
		t.Errorf("got %d, %v; the only free port is %d", got, err, markerPortMax-1)
	}
	// A rule from the marker's address on every port leaves none: an
	// error, not a loop.
	all := []Filter{{Rules: []FilterRule{{SrcIPs: []string{"192.0.2.77"}, DstPorts: []DstPort{{IP: "*", Ports: PortRange{0, 65535}}}}}}}
	if _, err := NewMarkerPort(all); err == nil {
		t.Error("a port with every port taken")
	}
}

func TestWaitReady(t *testing.T) {
	a := netip.MustParseAddr("100.64.0.1")
	b := netip.MustParseAddr("100.64.0.2")
	c := netip.MustParseAddr("100.64.0.3")
	marker := FilterRule{SrcIPs: []string{"192.0.2.77"}, DstPorts: []DstPort{{IP: "*", Ports: PortRange{41000, 41000}}}, IPProto: []int{6}}
	fromA := FilterRule{SrcIPs: []string{a.String()}, DstPorts: []DstPort{{IP: b.String(), Ports: PortRange{80, 80}}}}
	w := Wait{Bound: 300 * time.Millisecond, Poll: 10 * time.Millisecond}
	names := []string{"a", "b", "c"}

	// a may reach b, so a and b are each other's peers; c is no one's.
	nets := func(bHasA bool, cMarked bool) []Netmap {
		bm := Netmap{Self: []netip.Addr{b}, Filter: Filter{Rules: []FilterRule{fromA, marker}}}
		if bHasA {
			bm.Peers = [][]netip.Addr{{a}}
		}
		cm := Netmap{Self: []netip.Addr{c}, Filter: Filter{Rules: []FilterRule{}}}
		if cMarked {
			cm.Filter.Rules = append(cm.Filter.Rules, marker)
		}
		return []Netmap{
			{Self: []netip.Addr{a}, Peers: [][]netip.Addr{{b}}, Filter: Filter{Rules: []FilterRule{marker}}},
			bm, cm,
		}
	}
	read := func(nm []Netmap) func(context.Context, int) (Netmap, error) {
		return func(_ context.Context, i int) (Netmap, error) { return nm[i], nil }
	}
	if err := WaitReady(context.Background(), names, read(nets(true, true)), 41000, w); err != nil {
		t.Errorf("ready lab: %v", err)
	}
	for name, nm := range map[string][]Netmap{
		"a peer missing": nets(false, true),
		"no marker":      nets(true, false),
	} {
		err := WaitReady(context.Background(), names, read(nm), 41000, w)
		var nr *NotReadyError
		if !errors.As(err, &nr) || !errors.Is(err, ErrNotReady) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Ready on a later poll.
	polls := 0
	err := WaitReady(context.Background(), names, func(ctx context.Context, i int) (Netmap, error) {
		if i == 0 {
			polls++
		}
		return nets(polls > 3, true)[i], nil
	}, 41000, w)
	if err != nil || polls < 4 {
		t.Errorf("late peer: %v after %d polls", err, polls)
	}
}
