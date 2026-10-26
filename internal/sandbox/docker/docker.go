// Package docker is the Docker sandbox (decision 0003). It speaks the
// Docker Engine API through the official client, so Podman's compatible
// API serves too.
package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"path"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	cerrdefs "github.com/containerd/errdefs"
	"github.com/moby/moby/api/pkg/stdcopy"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/network"
	"github.com/moby/moby/client"

	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// Sandbox is a sandbox.Sandbox on a Docker Engine.
type Sandbox struct {
	cli   *client.Client
	runID string
	arch  string

	mu       sync.Mutex
	networks map[string]sandbox.Network
	nodes    map[string]sandbox.Node
}

var _ sandbox.Sandbox = (*Sandbox)(nil)

// New connects to the Docker Engine named by the environment (DOCKER_HOST,
// or the default socket) and returns a sandbox for a new run.
func New(ctx context.Context) (*Sandbox, error) {
	runID, err := sandbox.NewRunID()
	if err != nil {
		return nil, err
	}
	cli, err := Connect(ctx)
	if err != nil {
		return nil, err
	}
	v, err := cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker: %w", err)
	}
	return &Sandbox{
		cli:      cli,
		runID:    runID,
		arch:     v.Arch,
		networks: map[string]sandbox.Network{},
		nodes:    map[string]sandbox.Node{},
	}, nil
}

// Connect returns a client for the Docker Engine named by the environment,
// after checking that it answers, and that its internal networks keep DNS
// inside the lab.
func Connect(ctx context.Context) (*client.Client, error) {
	cli, err := client.New(client.FromEnv) // negotiates the API version by default
	if err != nil {
		return nil, fmt.Errorf("docker: %w", err)
	}
	if _, err := cli.Ping(ctx, client.PingOptions{NegotiateAPIVersion: true}); err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker: can't reach the Docker Engine at %s (is it running? DOCKER_HOST chooses another): %w", cli.DaemonHost(), err)
	}
	v, err := cli.ServerVersion(ctx, client.ServerVersionOptions{})
	if err == nil {
		err = checkEngine(v)
	}
	if err != nil {
		_ = cli.Close()
		return nil, fmt.Errorf("docker: %w", err)
	}
	return cli, nil
}

// checkEngine refuses a Docker Engine whose internal networks forward DNS
// queries out of the lab, through the host's resolver (CVE-2024-29018).
// Podman's networking is its own, and isn't checked (decision 0019).
func checkEngine(v client.ServerVersionResult) error {
	for _, c := range v.Components {
		if c.Name == "Podman Engine" {
			return nil
		}
	}
	return checkEngineVersion(v.Version)
}

// checkEngineVersion accepts the Docker Engine versions with the fix for
// CVE-2024-29018: 26.0.0 and later, 25.0.5 and later in 25.x, and 23.0.11
// and later in 23.x. 24.x never got it.
func checkEngineVersion(version string) error {
	core, pre, _ := strings.Cut(strings.SplitN(version, "+", 2)[0], "-")
	var nums [3]int
	parts := strings.Split(core, ".")
	ok := len(parts) == 3
	for i := 0; ok && i < 3; i++ {
		n, err := strconv.Atoi(parts[i])
		ok = err == nil && n >= 0
		nums[i] = n
	}
	if !ok {
		return fmt.Errorf("engine version %q: can't tell whether it has the fix for CVE-2024-29018, so the lab couldn't be kept inside its networks", version)
	}
	major, minor, patch := nums[0], nums[1], nums[2]
	safe := major > 26 ||
		(major == 26 && (minor > 0 || patch > 0 || pre == "")) ||
		(major == 25 && (minor > 0 || patch >= 5)) ||
		(major == 23 && (minor > 0 || patch >= 11))
	if !safe {
		return fmt.Errorf("this Docker Engine (%s) can send DNS queries from the lab's internal networks out through the host (CVE-2024-29018); tailsuite needs 26.0.0 or later, 25.0.5+ or 23.0.11+", version)
	}
	return nil
}

// Close closes the connection to the Docker Engine. It doesn't tear down.
func (s *Sandbox) Close() error { return s.cli.Close() }

// Arch is the Docker host's architecture, as Go names it (amd64, arm64):
// the one its Linux nodes run, so the node agent must be built for it.
func (s *Sandbox) Arch() string { return s.arch }

// RunID implements sandbox.Sandbox.
func (s *Sandbox) RunID() string { return s.runID }

// CreateNetwork implements sandbox.Sandbox. A network without Internet is
// internal: Docker gives it no route out of the host.
func (s *Sandbox) CreateNetwork(ctx context.Context, spec sandbox.NetworkSpec) (sandbox.Network, error) {
	if err := sandbox.CheckName(spec.Name); err != nil {
		return sandbox.Network{}, fmt.Errorf("network: %w", err)
	}
	s.mu.Lock()
	_, dup := s.networks[spec.Name]
	s.mu.Unlock()
	if dup {
		return sandbox.Network{}, fmt.Errorf("network %s already exists in run %s", spec.Name, s.runID)
	}
	res, err := s.cli.NetworkCreate(ctx, sandbox.ResourceName(s.runID, spec.Name), client.NetworkCreateOptions{
		Driver:   "bridge",
		Internal: !spec.Internet,
		Labels:   labels(s.runID, spec.Name, ""),
	})
	if err != nil {
		return sandbox.Network{}, fmt.Errorf("creating network %s: %w", spec.Name, err)
	}
	n := sandbox.Network{Name: spec.Name, ID: res.ID}
	s.mu.Lock()
	s.networks[spec.Name] = n
	s.mu.Unlock()
	return n, nil
}

// StartNode implements sandbox.Sandbox. It pulls the image if it's missing.
func (s *Sandbox) StartNode(ctx context.Context, spec sandbox.NodeSpec) (sandbox.Node, error) {
	if spec.Role == "" {
		spec.Role = sandbox.RoleTailnetNode
	}
	if err := sandbox.CheckNodeSpec(spec); err != nil {
		return sandbox.Node{}, err
	}
	s.mu.Lock()
	_, dup := s.nodes[spec.Name]
	var unknown []string
	for _, n := range spec.Networks {
		if got, ok := s.networks[n.Name]; !ok || got.ID != n.ID {
			unknown = append(unknown, n.Name)
		}
	}
	s.mu.Unlock()
	if dup {
		return sandbox.Node{}, fmt.Errorf("node %s already exists in run %s", spec.Name, s.runID)
	}
	if len(unknown) > 0 {
		return sandbox.Node{}, fmt.Errorf("node %s: networks not created by run %s: %s", spec.Name, s.runID, strings.Join(unknown, ", "))
	}

	if err := s.ensureImage(ctx, spec.Image); err != nil {
		return sandbox.Node{}, err
	}
	created, err := s.cli.ContainerCreate(ctx, createOptions(s.runID, spec))
	if err != nil {
		return sandbox.Node{}, fmt.Errorf("creating node %s: %w", spec.Name, err)
	}
	node, err := s.finishStart(ctx, created.ID, spec)
	if err != nil {
		// Don't leave a half-built node behind; Teardown would get it too.
		_, _ = s.cli.ContainerRemove(context.WithoutCancel(ctx), created.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true})
		return sandbox.Node{}, err
	}
	s.mu.Lock()
	s.nodes[spec.Name] = node
	s.mu.Unlock()
	return node, nil
}

func (s *Sandbox) finishStart(ctx context.Context, id string, spec sandbox.NodeSpec) (sandbox.Node, error) {
	for _, n := range spec.Networks[1:] {
		if _, err := s.cli.NetworkConnect(ctx, n.ID, client.NetworkConnectOptions{
			Container:      id,
			EndpointConfig: &network.EndpointSettings{Aliases: []string{spec.Name}},
		}); err != nil {
			return sandbox.Node{}, fmt.Errorf("connecting node %s to network %s: %w", spec.Name, n.Name, err)
		}
	}
	if len(spec.Files) > 0 {
		archive, err := tarFiles(spec.Files)
		if err != nil {
			return sandbox.Node{}, fmt.Errorf("node %s: %w", spec.Name, err)
		}
		if _, err := s.cli.CopyToContainer(ctx, id, client.CopyToContainerOptions{DestinationPath: "/", Content: archive}); err != nil {
			return sandbox.Node{}, fmt.Errorf("copying files into node %s: %w", spec.Name, err)
		}
	}
	if _, err := s.cli.ContainerStart(ctx, id, client.ContainerStartOptions{}); err != nil {
		return sandbox.Node{}, fmt.Errorf("starting node %s: %w", spec.Name, err)
	}
	inspect, err := s.cli.ContainerInspect(ctx, id, client.ContainerInspectOptions{})
	if err != nil {
		return sandbox.Node{}, fmt.Errorf("inspecting node %s: %w", spec.Name, err)
	}
	node := sandbox.Node{Name: spec.Name, ID: id, Role: spec.Role, Addrs: map[string]netip.Addr{}}
	if settings := inspect.Container.NetworkSettings; settings != nil {
		for _, n := range spec.Networks {
			if ep := settings.Networks[sandbox.ResourceName(s.runID, n.Name)]; ep != nil {
				node.Addrs[n.Name] = ep.IPAddress
			}
		}
	}
	return node, nil
}

func (s *Sandbox) ensureImage(ctx context.Context, ref string) error {
	if _, err := s.cli.ImageInspect(ctx, ref); err == nil {
		return nil
	} else if !cerrdefs.IsNotFound(err) {
		return fmt.Errorf("inspecting image %s: %w", ref, err)
	}
	pull, err := s.cli.ImagePull(ctx, ref, client.ImagePullOptions{})
	if err != nil {
		return fmt.Errorf("pulling image %s: %w", ref, err)
	}
	defer func() { _ = pull.Close() }()
	if err := pull.Wait(ctx); err != nil {
		return fmt.Errorf("pulling image %s: %w", ref, err)
	}
	return nil
}

// Exec implements sandbox.Sandbox. If ctx is cancelled first, Exec stops
// waiting and returns the error; the Docker Engine can't stop an exec'd
// process, so it runs on until the node is torn down.
func (s *Sandbox) Exec(ctx context.Context, node sandbox.Node, spec sandbox.ExecSpec) (int, error) {
	if len(spec.Cmd) == 0 {
		return -1, errors.New("exec: no command")
	}
	s.mu.Lock()
	known, ok := s.nodes[node.Name]
	s.mu.Unlock()
	if !ok || known.ID != node.ID {
		return -1, fmt.Errorf("exec: node %s wasn't started by run %s", node.Name, s.runID)
	}
	created, err := s.cli.ExecCreate(ctx, node.ID, client.ExecCreateOptions{
		Cmd:          spec.Cmd,
		AttachStdin:  spec.Stdin != nil,
		AttachStdout: true,
		AttachStderr: true,
	})
	if err != nil {
		return -1, fmt.Errorf("exec in node %s: %w", node.Name, err)
	}
	attached, err := s.cli.ExecAttach(ctx, created.ID, client.ExecAttachOptions{})
	if err != nil {
		return -1, fmt.Errorf("exec in node %s: %w", node.Name, err)
	}
	defer attached.Close()

	// Closing the connection is the only way to unblock the copies.
	stop := context.AfterFunc(ctx, attached.Close)
	defer stop()

	if spec.Stdin != nil {
		go func() {
			_, _ = io.Copy(attached.Conn, spec.Stdin)
			_ = attached.CloseWrite()
		}()
	}
	stdout, stderr := orDiscard(spec.Stdout), orDiscard(spec.Stderr)
	if _, err := stdcopy.StdCopy(stdout, stderr, attached.Reader); err != nil {
		if ctx.Err() != nil {
			return -1, fmt.Errorf("exec in node %s: %w", node.Name, ctx.Err())
		}
		return -1, fmt.Errorf("exec in node %s: reading output: %w", node.Name, err)
	}
	return s.exitCode(ctx, node, created.ID)
}

// exitWait bounds how long Exec waits for the Docker Engine to record an
// exit after the command's output has closed. It's usually milliseconds;
// the bound is for a daemon that never records one.
const exitWait = 30 * time.Second

// exitCode waits for the Docker Engine to record that an exec'd process has
// exited, after its output has closed.
func (s *Sandbox) exitCode(ctx context.Context, node sandbox.Node, execID string) (int, error) {
	ctx, cancel := context.WithTimeout(ctx, exitWait)
	defer cancel()
	for {
		res, err := s.cli.ExecInspect(ctx, execID, client.ExecInspectOptions{})
		if err != nil {
			return -1, fmt.Errorf("exec in node %s: %w", node.Name, err)
		}
		if !res.Running {
			// The daemon reports a PID for every exec that started. Without
			// one, the exit code is the daemon's, not the command's: 126 for
			// a command the image doesn't have, or the client's default 0.
			if res.PID == 0 {
				return -1, fmt.Errorf("exec in node %s: the command never started (the daemon reports exit %d)", node.Name, res.ExitCode)
			}
			return res.ExitCode, nil
		}
		select {
		case <-ctx.Done():
			return -1, fmt.Errorf("exec in node %s: waiting for its exit code: %w", node.Name, ctx.Err())
		case <-time.After(20 * time.Millisecond):
		}
	}
}

// Teardown implements sandbox.Sandbox.
func (s *Sandbox) Teardown(ctx context.Context) error {
	err := TeardownRun(ctx, s.cli, s.runID)
	if err == nil {
		s.mu.Lock()
		clear(s.networks)
		clear(s.nodes)
		s.mu.Unlock()
	}
	return err
}

// teardownPasses bounds how many times TeardownRun looks again. A node
// whose create was in flight when the run was interrupted can appear after
// the first pass has listed the run.
const teardownPasses = 5

// TeardownRun removes every container and network labelled with a run's
// ID, as `tailsuite down` does after a crash. It's idempotent. It looks
// again after each pass, until a pass finds nothing, at most
// teardownPasses times.
func TeardownRun(ctx context.Context, cli *client.Client, runID string) error {
	if err := sandbox.CheckRunID(runID); err != nil {
		return err
	}
	for pass := range teardownPasses {
		found, err := teardownPass(ctx, cli, runID)
		if err != nil || found == 0 {
			return err
		}
		if pass < teardownPasses-1 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(200 * time.Millisecond):
			}
		}
	}
	return fmt.Errorf("run %s: still finding nodes or networks after %d passes", runID, teardownPasses)
}

// teardownPass removes what it finds of a run, and says how much it found.
func teardownPass(ctx context.Context, cli *client.Client, runID string) (int, error) {
	filter := client.Filters{}.Add("label", sandbox.LabelRun+"="+runID)
	var errs []error

	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filter})
	if err != nil {
		return 0, fmt.Errorf("listing run %s's nodes: %w", runID, err)
	}
	for _, c := range containers.Items {
		if _, err := cli.ContainerRemove(ctx, c.ID, client.ContainerRemoveOptions{Force: true, RemoveVolumes: true}); err != nil && !gone(err) {
			errs = append(errs, fmt.Errorf("removing node %s: %w", c.Labels[sandbox.LabelName], err))
		}
	}

	networks, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: filter})
	if err != nil {
		return 0, errors.Join(append(errs, fmt.Errorf("listing run %s's networks: %w", runID, err))...)
	}
	for _, n := range networks.Items {
		if _, err := cli.NetworkRemove(ctx, n.ID, client.NetworkRemoveOptions{}); err != nil && !gone(err) {
			errs = append(errs, fmt.Errorf("removing network %s: %w", n.Labels[sandbox.LabelName], err))
		}
	}
	return len(containers.Items) + len(networks.Items), errors.Join(errs...)
}

// Runs lists the IDs of runs that still have containers or networks.
func Runs(ctx context.Context, cli *client.Client) ([]string, error) {
	filter := client.Filters{}.Add("label", sandbox.LabelRun)
	seen := map[string]bool{}
	containers, err := cli.ContainerList(ctx, client.ContainerListOptions{All: true, Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("listing lab nodes: %w", err)
	}
	for _, c := range containers.Items {
		seen[c.Labels[sandbox.LabelRun]] = true
	}
	networks, err := cli.NetworkList(ctx, client.NetworkListOptions{Filters: filter})
	if err != nil {
		return nil, fmt.Errorf("listing lab networks: %w", err)
	}
	for _, n := range networks.Items {
		seen[n.Labels[sandbox.LabelRun]] = true
	}
	ids := make([]string, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids, nil
}

// gone reports an error that means the resource is already removed, or
// being removed.
func gone(err error) bool {
	return cerrdefs.IsNotFound(err) || (cerrdefs.IsConflict(err) && strings.Contains(err.Error(), "already in progress"))
}

func labels(runID, name string, role sandbox.Role) map[string]string {
	l := map[string]string{sandbox.LabelRun: runID, sandbox.LabelName: name}
	if role != "" {
		l[sandbox.LabelRole] = string(role)
	}
	return l
}

// createOptions is the container a node spec asks for. A tailnet node gets
// the TUN device and NET_ADMIN, and TS_USERSPACE=false for the official
// image.
func createOptions(runID string, spec sandbox.NodeSpec) client.ContainerCreateOptions {
	env := map[string]string{}
	for k, v := range spec.Env {
		env[k] = v
	}
	memory := spec.Memory
	if memory == 0 {
		memory = sandbox.DefaultNodeMemory
	}
	// An init process (docker-init) as PID 1 reaps orphaned processes, such
	// as those exec'd commands leave, and passes signals on.
	withInit := true
	host := &container.HostConfig{
		Init:      &withInit,
		Resources: container.Resources{Memory: memory, MemorySwap: memory},
	}
	if spec.Role == sandbox.RoleTailnetNode {
		env["TS_USERSPACE"] = "false"
		host.CapAdd = []string{"NET_ADMIN"}
		host.Devices = []container.DeviceMapping{{PathOnHost: "/dev/net/tun", PathInContainer: "/dev/net/tun", CgroupPermissions: "rwm"}}
	}
	first := spec.Networks[0]
	return client.ContainerCreateOptions{
		Name: sandbox.ResourceName(runID, spec.Name),
		Config: &container.Config{
			Hostname:   spec.Name,
			Image:      spec.Image,
			Env:        envList(env),
			Entrypoint: spec.Entrypoint,
			Cmd:        spec.Cmd,
			Labels:     labels(runID, spec.Name, spec.Role),
		},
		HostConfig: host,
		NetworkingConfig: &network.NetworkingConfig{EndpointsConfig: map[string]*network.EndpointSettings{
			first.ID: {Aliases: []string{spec.Name}},
		}},
	}
}

func envList(env map[string]string) []string {
	list := make([]string, 0, len(env))
	for k, v := range env {
		list = append(list, k+"="+v)
	}
	slices.Sort(list)
	return list
}

// tarFiles packs files into a tar archive to extract at /. It holds no
// directory entries: Docker would apply their mode and owner to a directory
// that already exists, such as /tmp. Docker creates missing parents itself,
// root-owned with mode 0755, and leaves existing ones alone.
func tarFiles(files []sandbox.File) (io.Reader, error) {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for _, f := range files {
		name := strings.TrimPrefix(path.Clean(f.Path), "/")
		if name == "" || name == "." {
			return nil, fmt.Errorf("file path %q names no file", f.Path)
		}
		hdr := &tar.Header{
			Typeflag: tar.TypeReg,
			Name:     name,
			Mode:     int64(f.Mode.Perm()),
			Size:     int64(len(f.Content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return nil, err
		}
		if _, err := tw.Write(f.Content); err != nil {
			return nil, err
		}
	}
	if err := tw.Close(); err != nil {
		return nil, err
	}
	return &buf, nil
}

func orDiscard(w io.Writer) io.Writer {
	if w == nil {
		return io.Discard
	}
	return w
}
