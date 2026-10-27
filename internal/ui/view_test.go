package ui

import (
	"slices"
	"strings"
	"testing"

	"github.com/bulkhead-labs/tailsuite/internal/project"
)

func TestTheViewDrawsThePolicysPaths(t *testing.T) {
	p, err := project.Load("../suite/testdata/startup")
	if err != nil {
		t.Fatal(err)
	}
	v := NewView(p)
	if len(v.Devices) != 4 {
		t.Errorf("devices: %+v", v.Devices)
	}
	want := []Path{
		{From: "bobs-laptop", To: "server", Ports: []string{"tcp:80"}},
		{From: "laptop", To: "server", Ports: []string{"tcp:80"}},
		{From: "server", To: "db", Ports: []string{"tcp:5432"}},
	}
	if !slices.EqualFunc(v.Paths, want, func(a, b Path) bool {
		return a.From == b.From && a.To == b.To && slices.Equal(a.Ports, b.Ports)
	}) {
		t.Errorf("paths = %+v, want %+v", v.Paths, want)
	}
	if len(v.Notes) != 1 || !strings.Contains(v.Notes[0], "ipset:office") {
		t.Errorf("notes = %q, want the IP set the map can't draw", v.Notes)
	}
}

func TestCutPort(t *testing.T) {
	for in, want := range map[string][2]string{"tag:db:5432": {"tag:db", "5432"}, "*:*": {"*", "*"}, "fd7a::1:22": {"fd7a::1", "22"}} {
		h, p, ok := cutPort(in)
		if !ok || h != want[0] || p != want[1] {
			t.Errorf("cutPort(%q) = %q %q %v", in, h, p, ok)
		}
	}
	if _, _, ok := cutPort("db:"); ok {
		t.Error("cutPort accepted an empty port")
	}
}
