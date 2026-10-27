package docker

import (
	"archive/tar"
	"errors"
	"io"
	"slices"
	"testing"

	"github.com/moby/moby/api/types/system"
	"github.com/moby/moby/client"

	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

const image = "tailscale/tailscale:v1.102.5@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"

func TestCreateOptionsTailnetNode(t *testing.T) {
	spec := sandbox.NodeSpec{
		Name:     "laptop",
		Role:     sandbox.RoleTailnetNode,
		Image:    image,
		Networks: []sandbox.Network{{Name: "lan", ID: "net-lan"}, {Name: "wan", ID: "net-wan"}},
		Env:      map[string]string{"TS_STATE_DIR": "/var/lib/tailscale"},
		Cmd:      []string{"tailscaled"},
	}
	opts := createOptions("0a1b2c3d", spec)

	if opts.Name != "tailsuite-0a1b2c3d-laptop" {
		t.Errorf("Name = %q", opts.Name)
	}
	if opts.Config.Hostname != "laptop" || opts.Config.Image != image {
		t.Errorf("Hostname, Image = %q, %q", opts.Config.Hostname, opts.Config.Image)
	}
	wantEnv := []string{"TS_STATE_DIR=/var/lib/tailscale", "TS_USERSPACE=false"}
	if !slices.Equal(opts.Config.Env, wantEnv) {
		t.Errorf("Env = %q, want %q", opts.Config.Env, wantEnv)
	}
	if spec.Env["TS_USERSPACE"] != "" {
		t.Error("createOptions changed the caller's Env map")
	}
	wantLabels := map[string]string{
		sandbox.LabelRun:  "0a1b2c3d",
		sandbox.LabelName: "laptop",
		sandbox.LabelRole: "tailnet-node",
	}
	for k, v := range wantLabels {
		if opts.Config.Labels[k] != v {
			t.Errorf("label %s = %q, want %q", k, opts.Config.Labels[k], v)
		}
	}
	host := opts.HostConfig
	if !slices.Equal(host.CapAdd, []string{"NET_ADMIN"}) {
		t.Errorf("CapAdd = %q, want NET_ADMIN only", host.CapAdd)
	}
	if host.Privileged {
		t.Error("a node must not be privileged")
	}
	if host.Init == nil || !*host.Init {
		t.Error("a node has no init process to reap orphans")
	}
	if len(host.Devices) != 1 || host.Devices[0].PathOnHost != "/dev/net/tun" || host.Devices[0].PathInContainer != "/dev/net/tun" {
		t.Errorf("Devices = %+v, want /dev/net/tun", host.Devices)
	}
	if host.Memory != sandbox.DefaultNodeMemory || host.MemorySwap != host.Memory {
		t.Errorf("Memory, MemorySwap = %d, %d, want %d with no swap", host.Memory, host.MemorySwap, sandbox.DefaultNodeMemory)
	}
	eps := opts.NetworkingConfig.EndpointsConfig
	if len(eps) != 1 || eps["net-lan"] == nil || !slices.Equal(eps["net-lan"].Aliases, []string{"laptop"}) {
		t.Errorf("EndpointsConfig = %+v, want only the first network, aliased laptop", eps)
	}
}

func TestCreateOptionsService(t *testing.T) {
	opts := createOptions("0a1b2c3d", sandbox.NodeSpec{
		Name:     "control",
		Role:     sandbox.RoleService,
		Image:    image,
		Networks: []sandbox.Network{{Name: "lan", ID: "net-lan"}},
		Memory:   256 << 20,
	})
	if len(opts.HostConfig.CapAdd) != 0 || len(opts.HostConfig.Devices) != 0 {
		t.Errorf("a service got %q and %+v", opts.HostConfig.CapAdd, opts.HostConfig.Devices)
	}
	if slices.Contains(opts.Config.Env, "TS_USERSPACE=false") {
		t.Error("a service got TS_USERSPACE")
	}
	if opts.HostConfig.Memory != 256<<20 {
		t.Errorf("Memory = %d", opts.HostConfig.Memory)
	}
}

func TestTarFiles(t *testing.T) {
	r, err := tarFiles([]sandbox.File{
		{Path: "/opt/tailsuite/bin/agent", Mode: 0o755, Content: []byte("binary")},
		{Path: "/opt/tailsuite/agent.json", Mode: 0o644, Content: []byte("{}")},
		{Path: "/etc/motd", Mode: 0o644, Content: nil},
	})
	if err != nil {
		t.Fatal(err)
	}
	type entry struct {
		name string
		dir  bool
		mode int64
		body string
	}
	var got []entry
	tr := tar.NewReader(r)
	for {
		hdr, err := tr.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		body, err := io.ReadAll(tr)
		if err != nil {
			t.Fatal(err)
		}
		got = append(got, entry{hdr.Name, hdr.Typeflag == tar.TypeDir, hdr.Mode, string(body)})
	}
	// No directory entries: Docker would apply their mode and owner to
	// directories that already exist.
	want := []entry{
		{"opt/tailsuite/bin/agent", false, 0o755, "binary"},
		{"opt/tailsuite/agent.json", false, 0o644, "{}"},
		{"etc/motd", false, 0o644, ""},
	}
	if !slices.Equal(got, want) {
		t.Errorf("archive:\n got %+v\nwant %+v", got, want)
	}
}

func TestEnvListIsSorted(t *testing.T) {
	got := envList(map[string]string{"B": "2", "A": "1", "C": "x=y"})
	if !slices.Equal(got, []string{"A=1", "B=2", "C=x=y"}) {
		t.Errorf("envList = %q", got)
	}
}

func TestCheckEngineVersion(t *testing.T) {
	for _, v := range []string{"26.0.0", "26.1.5+dfsg1", "26.0.1-rc1", "27.3.1", "29.0.0", "25.0.5", "25.0.14", "23.0.11", "23.0.17"} {
		if err := checkEngineVersion(v); err != nil {
			t.Errorf("%s: %v", v, err)
		}
	}
	for _, v := range []string{"26.0.0-rc1", "26.0.0-rc3", "25.0.4", "25.0.0", "24.0.9", "24.0.0", "23.0.10", "20.10.24+dfsg1", "19.03.15", "", "dev", "26.1", "26.x.0"} {
		if err := checkEngineVersion(v); err == nil {
			t.Errorf("%s: accepted", v)
		}
	}
}

func TestCheckEngineSkipsPodman(t *testing.T) {
	podman := client.ServerVersionResult{Version: "5.2.0", Components: []system.ComponentVersion{{Name: "Podman Engine", Version: "5.2.0"}}}
	if err := checkEngine(podman); err != nil {
		t.Errorf("podman: %v", err)
	}
	old := client.ServerVersionResult{Version: "24.0.7", Components: []system.ComponentVersion{{Name: "Engine", Version: "24.0.7"}}}
	if err := checkEngine(old); err == nil {
		t.Error("Docker 24.0.7 was accepted")
	}
}

func TestProjectLabel(t *testing.T) {
	s := &Sandbox{runID: "0a1b2c3d", project: "0123456789abcdef"}
	if l := s.withProject(labels(s.runID, "db", sandbox.RoleTailnetNode)); l[sandbox.LabelProject] != "0123456789abcdef" || l[sandbox.LabelRun] != "0a1b2c3d" {
		t.Errorf("labels = %v", l)
	}
	if l := (&Sandbox{runID: "0a1b2c3d"}).withProject(labels("0a1b2c3d", "db", "")); len(l) != 2 {
		t.Errorf("a sandbox with no project labelled one: %v", l)
	}
}
