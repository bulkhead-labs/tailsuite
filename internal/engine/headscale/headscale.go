// Package headscale is the headscale engine (decision 0002): a lab whose
// control plane is headscale, in a container of its own, with TLS from the
// run's own CA and its embedded DERP server (decision 0003), on an
// internal network.
//
// The policy is applied in headscale's database mode, through its CLI, so
// a change needs no restart. headscale checks a policy's own tests against
// live devices, so the policy is set once the nodes have joined (research
// 0005); its copy leaves the tests out anyway (0016).
package headscale

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/bulkhead-labs/tailsuite/internal/agent/control"
	"github.com/bulkhead-labs/tailsuite/internal/engine"
	"github.com/bulkhead-labs/tailsuite/internal/sandbox"
)

// Name is the engine's name in lab.yaml's profiles.
const Name = "headscale"

// The pinned images. headscale 0.29.5 was released 2026-10-13 and its
// digest pushed then; it carries two fixes a lab relies on (a rejected
// policy no longer leaves its filter active, and clients no longer upload
// logs while logtail is off). 0.29.6 is #68.
const (
	HeadscaleVersion = "0.29.5"
	HeadscaleImage   = "headscale/headscale:0.29.5@sha256:780ed2f9e85719a2211277433f2c8afb45105d0d25031949a6848a485f9e216d"
	TailscaleImage   = "tailscale/tailscale:v1.102.5@sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065"
)

// ControlName is the control plane's node, and the name in its
// certificate. A device can't have it.
const ControlName = "tailsuite-headscale"

// Paths inside the nodes.
const (
	configPath = "/etc/headscale/config.yaml"
	certPath   = "/etc/headscale/tls.pem"
	keyPath    = "/etc/headscale/tls.key"
	caPath     = "/opt/tailsuite/lab-ca.pem"
)

const controlMemory = 256 << 20

// Config configures the engine.
type Config struct {
	// Sandbox runs the lab. Start creates everything in it, and the lab's
	// Teardown tears it down.
	Sandbox sandbox.Sandbox
	// Agent is the node agent's binary, for the Docker host's Linux
	// architecture (#52 embeds it; until then, tests build it).
	Agent []byte
	// Wait bounds the readiness wait. Zero means DefaultWait.
	Wait engine.Wait
}

// DefaultWait bounds how long a policy may take to reach every node.
// headscale took up to 2.5 s in research 0004.
var DefaultWait = engine.Wait{Bound: 30 * time.Second, Poll: 250 * time.Millisecond}

// Timeouts for starting things. They're bounds on starting, never on
// results.
const (
	controlTimeout = 30 * time.Second
	joinTimeout    = 60 * time.Second
)

// Engine is the headscale engine.
type Engine struct {
	cfg Config
}

// New returns the engine.
func New(cfg Config) *Engine {
	if cfg.Wait.Bound == 0 {
		cfg.Wait = DefaultWait
	}
	return &Engine{cfg: cfg}
}

var _ engine.Engine = (*Engine)(nil)

// Name returns "headscale".
func (e *Engine) Name() string { return Name }

// Version names headscale's version.
func (e *Engine) Version() string { return "headscale " + HeadscaleVersion }

// Capabilities returns headscale's rows of the matrix.
func (e *Engine) Capabilities() engine.Capabilities { return slices.Clone(capabilities) }

// Start brings up the lab and returns once it's ready. If it fails, it
// tears down what it made.
func (e *Engine) Start(ctx context.Context, plan engine.LabPlan, l engine.Lowering) (_ engine.Lab, err error) {
	if l.Engine != Name {
		return nil, fmt.Errorf("headscale: a lowering for %q", l.Engine)
	}
	if err := l.Err(); err != nil {
		return nil, err
	}
	if err := checkPlan(plan); err != nil {
		return nil, err
	}
	if e.cfg.Sandbox == nil || len(e.cfg.Agent) == 0 {
		return nil, errors.New("headscale: the engine needs a sandbox and the node agent")
	}
	lab := &Lab{sb: e.cfg.Sandbox, wait: e.cfg.Wait}
	defer func() {
		if err != nil {
			tctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), sandbox.TeardownTimeout)
			defer cancel()
			err = errors.Join(err, lab.Teardown(tctx))
		}
	}()
	if err := lab.start(ctx, plan, e.cfg.Agent); err != nil {
		return nil, err
	}
	if _, err := lab.Apply(ctx, l.Policy); err != nil {
		return nil, err
	}
	return lab, nil
}

// Attach returns a lab that's already running in the engine's sandbox,
// such as one `tailsuite up` started in another process. nodes are the
// sandbox's nodes; devices names the lab's tailnet nodes, in the plan's
// order, and each must be among them and answer with its netmap. The lab
// keeps the policy it holds until the next Apply.
func (e *Engine) Attach(ctx context.Context, nodes []sandbox.Node, devices []string) (*Lab, error) {
	if e.cfg.Sandbox == nil {
		return nil, errors.New("headscale: the engine needs a sandbox")
	}
	byName := map[string]sandbox.Node{}
	for _, n := range nodes {
		byName[n.Name] = n
	}
	control, ok := byName[ControlName]
	if !ok {
		return nil, fmt.Errorf("headscale: run %s has no control plane", e.cfg.Sandbox.RunID())
	}
	lab := &Lab{sb: e.cfg.Sandbox, wait: e.cfg.Wait, control: control}
	if _, err := lab.headscale(ctx, nil, "health"); err != nil {
		return nil, fmt.Errorf("headscale: run %s's control plane isn't healthy: %w", e.cfg.Sandbox.RunID(), err)
	}
	for _, d := range devices {
		n, ok := byName[d]
		if !ok || n.Role != sandbox.RoleTailnetNode {
			return nil, fmt.Errorf("headscale: run %s has no node %s", e.cfg.Sandbox.RunID(), d)
		}
		node := engine.Node{Name: d, Sandbox: n}
		nm, err := lab.Netmap(ctx, node)
		if err != nil {
			return nil, err
		}
		for _, a := range nm.Self {
			switch {
			case a.Is4():
				node.IPv4 = a
			case a.Is6():
				node.IPv6 = a
			}
		}
		if !node.IPv4.IsValid() {
			return nil, fmt.Errorf("headscale: node %s has no tailnet IPv4 address", d)
		}
		lab.nodes = append(lab.nodes, node)
	}
	for _, n := range nodes {
		if n.Role == sandbox.RoleTailnetNode && !slices.Contains(devices, n.Name) {
			return nil, fmt.Errorf("headscale: run %s has a node %s the plan doesn't", e.cfg.Sandbox.RunID(), n.Name)
		}
	}
	return lab, nil
}

func checkPlan(plan engine.LabPlan) error {
	if len(plan.Devices) == 0 {
		return errors.New("headscale: the plan has no devices")
	}
	var errs []error
	seen := map[string]bool{}
	for _, d := range plan.Devices {
		switch {
		case d.Name == ControlName:
			errs = append(errs, fmt.Errorf("headscale: a device can't be named %s, which is the control plane's", ControlName))
		case seen[d.Name]:
			errs = append(errs, fmt.Errorf("headscale: device %s twice", d.Name))
		case d.User == "" && len(d.Tags) == 0:
			errs = append(errs, fmt.Errorf("headscale: device %s has neither a user nor tags", d.Name))
		}
		if err := sandbox.CheckName(d.Name); err != nil {
			errs = append(errs, fmt.Errorf("headscale: device %w", err))
		}
		seen[d.Name] = true
	}
	return errors.Join(errs...)
}

// Lab is a running headscale lab.
type Lab struct {
	sb      sandbox.Sandbox
	wait    engine.Wait
	net     sandbox.Network
	control sandbox.Node
	nodes   []engine.Node

	mu      sync.Mutex
	applied []byte
	marker  []engine.PatchOp
	port    uint16 // the last Apply's marker port
}

var _ engine.Lab = (*Lab)(nil)

// Nodes returns the lab's tailnet nodes, in the plan's order.
func (l *Lab) Nodes() []engine.Node { return slices.Clone(l.nodes) }

// Control is the control plane's node.
func (l *Lab) Control() sandbox.Node { return l.control }

// Applied returns the policy headscale holds, as last applied through
// Apply, and the operations Apply added to it: the readiness marker and,
// if needed, the explicit default.
func (l *Lab) Applied() ([]byte, []engine.PatchOp) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return slices.Clone(l.applied), slices.Clone(l.marker)
}

// Teardown removes the lab. It's idempotent.
func (l *Lab) Teardown(ctx context.Context) error { return l.sb.Teardown(ctx) }

func (l *Lab) start(ctx context.Context, plan engine.LabPlan, agentBinary []byte) error {
	net, err := l.sb.CreateNetwork(ctx, sandbox.NetworkSpec{Name: "tailnet"})
	if err != nil {
		return fmt.Errorf("headscale: %w", err)
	}
	l.net = net
	tlsFiles, err := newLabTLS(ControlName, time.Now())
	if err != nil {
		return fmt.Errorf("headscale: %w", err)
	}
	l.control, err = l.sb.StartNode(ctx, sandbox.NodeSpec{
		Name:     ControlName,
		Role:     sandbox.RoleService,
		Image:    HeadscaleImage,
		Networks: []sandbox.Network{net},
		Cmd:      []string{"serve"},
		Files: []sandbox.File{
			{Path: configPath, Mode: 0o644, Content: []byte(config(ControlName))},
			{Path: certPath, Mode: 0o644, Content: tlsFiles.Cert},
			{Path: keyPath, Mode: 0o600, Content: tlsFiles.Key},
		},
		Memory: controlMemory,
	})
	if err != nil {
		return fmt.Errorf("headscale: starting the control plane: %w", err)
	}
	if err := l.waitHealthy(ctx); err != nil {
		return err
	}

	users, err := l.createUsers(ctx, plan)
	if err != nil {
		return err
	}

	l.nodes = make([]engine.Node, len(plan.Devices))
	for i, d := range plan.Devices {
		n, err := l.sb.StartNode(ctx, sandbox.NodeSpec{
			Name:       d.Name,
			Image:      TailscaleImage,
			Networks:   []sandbox.Network{net},
			Entrypoint: control.Entrypoint(d.Name, "tailscaled", "--state=mem:", "--tun=tailscale0", "--no-logs-no-support"),
			Env:        map[string]string{"SSL_CERT_FILE": caPath},
			Files: []sandbox.File{
				control.AgentFile(agentBinary),
				{Path: caPath, Mode: 0o644, Content: tlsFiles.CA},
			},
		})
		if err != nil {
			return fmt.Errorf("headscale: starting node %s: %w", d.Name, err)
		}
		l.nodes[i] = engine.Node{Name: d.Name, Sandbox: n}
	}

	// Join every node at once; each takes a few seconds.
	errs := make([]error, len(plan.Devices))
	var wg sync.WaitGroup
	for i, d := range plan.Devices {
		wg.Go(func() { errs[i] = l.join(ctx, i, d, users[d.User]) })
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		return err
	}
	return nil
}

// config is headscale's configuration: TLS from the lab CA, the embedded
// DERP server and nothing else, the policy in the database, and nothing
// that reaches outside the lab (no update checks, no DERP map from
// Tailscale, no log uploads, no DNS pushed to nodes).
func config(host string) string {
	return `# Written by tailsuite for one lab run.
server_url: https://` + host + `:443
listen_addr: 0.0.0.0:443
metrics_listen_addr: 127.0.0.1:9090
grpc_listen_addr: 127.0.0.1:50443
noise:
  private_key_path: /var/lib/headscale/noise_private.key
prefixes:
  v4: 100.64.0.0/10
  v6: fd7a:115c:a1e0::/48
  allocation: sequential
derp:
  server:
    enabled: true
    region_id: 999
    region_code: lab
    region_name: tailsuite lab
    stun_listen_addr: 0.0.0.0:3478
    private_key_path: /var/lib/headscale/derp_server_private.key
    automatically_add_embedded_derp_region: true
  urls: []
  paths: []
  auto_update_enabled: false
disable_check_updates: true
database:
  type: sqlite
  sqlite:
    path: /var/lib/headscale/db.sqlite
    write_ahead_log: true
tls_cert_path: ` + certPath + `
tls_key_path: ` + keyPath + `
log:
  level: info
policy:
  mode: database
dns:
  magic_dns: false
  override_local_dns: false
unix_socket: /var/run/headscale/headscale.sock
unix_socket_permission: "0770"
logtail:
  enabled: false
`
}

// run runs a command in a node and returns its stdout. A non-zero exit is
// an error carrying its stderr.
func (l *Lab) run(ctx context.Context, n sandbox.Node, stdin []byte, args ...string) ([]byte, error) {
	var stdout, stderr bytes.Buffer
	spec := sandbox.ExecSpec{Cmd: args, Stdout: &stdout, Stderr: &stderr}
	if stdin != nil {
		spec.Stdin = bytes.NewReader(stdin)
	}
	code, err := l.sb.Exec(ctx, n, spec)
	if err != nil {
		return nil, err
	}
	if code != 0 {
		return nil, &execError{cmd: args[0] + " " + strings.Join(redact(args[1:]), " "), code: code, stderr: strings.TrimSpace(stderr.String())}
	}
	return stdout.Bytes(), nil
}

type execError struct {
	cmd    string
	code   int
	stderr string
}

func (e *execError) Error() string {
	return fmt.Sprintf("%s exited %d: %s", e.cmd, e.code, e.stderr)
}

// redact keeps auth keys out of errors. They only ever travel on stdin,
// but an error should never be the place that proves it wrong.
func redact(args []string) []string {
	out := slices.Clone(args)
	for i, a := range out {
		if strings.Contains(a, "auth-key=") && !strings.HasSuffix(a, "file:/dev/stdin") {
			out[i] = "--auth-key=REDACTED"
		}
	}
	return out
}

func (l *Lab) headscale(ctx context.Context, stdin []byte, args ...string) ([]byte, error) {
	return l.run(ctx, l.control, stdin, append([]string{"headscale"}, args...)...)
}

func (l *Lab) waitHealthy(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	for {
		_, err := l.headscale(ctx, nil, "health")
		if err == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("headscale: the control plane didn't become healthy within %s: %w", controlTimeout, err)
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// createUsers creates a headscale user for each login the plan's devices
// belong to, and returns their IDs. headscale matches a policy's login
// against a user's email; the user's own name is tailsuite's, chosen so no
// policy names it.
func (l *Lab) createUsers(ctx context.Context, plan engine.LabPlan) (map[string]uint64, error) {
	ids := map[string]uint64{}
	n := 0
	for _, d := range plan.Devices {
		if d.User == "" {
			continue
		}
		if _, ok := ids[d.User]; ok {
			continue
		}
		n++
		out, err := l.headscale(ctx, nil, "users", "create", "tailsuite-user-"+strconv.Itoa(n), "--email", d.User, "-o", "json")
		if err != nil {
			return nil, fmt.Errorf("headscale: creating the user %s: %w", d.User, err)
		}
		var u struct {
			ID json.Number `json:"id"`
		}
		if err := json.Unmarshal(out, &u); err != nil {
			return nil, fmt.Errorf("headscale: reading the user %s: %w", d.User, err)
		}
		id, err := strconv.ParseUint(u.ID.String(), 10, 64)
		if err != nil {
			return nil, fmt.Errorf("headscale: the user %s's ID %q: %w", d.User, u.ID, err)
		}
		ids[d.User] = id
	}
	return ids, nil
}

// join registers a node with a single-use key of its own, sent on stdin,
// then checks that its tailscaled runs a kernel TUN device and that its
// agent answers.
func (l *Lab) join(ctx context.Context, i int, d engine.PlanDevice, userID uint64) error {
	n := l.nodes[i]
	ctx, cancel := context.WithTimeout(ctx, joinTimeout)
	defer cancel()
	args := []string{"preauthkeys", "create", "--expiration", "10m", "-o", "json"}
	if len(d.Tags) > 0 {
		args = append(args, "--tags", strings.Join(d.Tags, ","))
	} else {
		args = append(args, "--user", strconv.FormatUint(userID, 10))
	}
	out, err := l.headscale(ctx, nil, args...)
	if err != nil {
		return fmt.Errorf("headscale: a key for %s: %w", d.Name, err)
	}
	var key struct {
		Key string `json:"key"`
	}
	if err := json.Unmarshal(out, &key); err != nil || key.Key == "" {
		return fmt.Errorf("headscale: reading the key for %s", d.Name)
	}
	if _, err := l.run(ctx, n.Sandbox, []byte(key.Key), "tailscale", "up",
		"--login-server=https://"+ControlName,
		"--auth-key=file:/dev/stdin",
		"--hostname="+d.Name,
		"--accept-dns=false",
		"--accept-routes=false",
		"--timeout="+joinTimeout.String(),
	); err != nil {
		return fmt.Errorf("headscale: joining %s: %w", d.Name, err)
	}
	if err := sandbox.CheckKernelTUN(ctx, l.sb, n.Sandbox, "tailscale0"); err != nil {
		return fmt.Errorf("headscale: %w", err)
	}
	s, err := control.Start(ctx, l.sb, n.Sandbox)
	if err != nil {
		return fmt.Errorf("headscale: %w", err)
	}
	if err := s.Close(); err != nil {
		return fmt.Errorf("headscale: %w", err)
	}
	nm, err := l.Netmap(ctx, n)
	if err != nil {
		return err
	}
	// Each join writes only its own node.
	for _, a := range nm.Self {
		switch {
		case a.Is4():
			l.nodes[i].IPv4 = a
		case a.Is6():
			l.nodes[i].IPv6 = a
		}
	}
	if !l.nodes[i].IPv4.IsValid() {
		return fmt.Errorf("headscale: node %s has no tailnet IPv4 address", d.Name)
	}
	return nil
}

// Netmap reads a node's network map.
func (l *Lab) Netmap(ctx context.Context, n engine.Node) (engine.Netmap, error) {
	out, err := l.run(ctx, n.Sandbox, nil, "tailscale", "debug", "netmap")
	if err != nil {
		return engine.Netmap{}, fmt.Errorf("headscale: node %s's netmap: %w", n.Name, err)
	}
	return engine.ParseNetmap(out)
}

// CompiledFilter is the packet filter the node's tailscaled holds.
func (l *Lab) CompiledFilter(ctx context.Context, n engine.Node) (engine.Filter, error) {
	nm, err := l.Netmap(ctx, n)
	if err != nil {
		return engine.Filter{}, err
	}
	return nm.Filter, nil
}

// Apply applies an engine's copy of the policy with a fresh readiness
// marker, and waits until every node shows it. It returns the policy as
// applied. A policy headscale rejects is a *engine.Refusal.
func (l *Lab) Apply(ctx context.Context, pol []byte) ([]byte, error) {
	current := make([]engine.Filter, len(l.nodes))
	for i, n := range l.nodes {
		f, err := l.CompiledFilter(ctx, n)
		if err != nil {
			return nil, err
		}
		current[i] = f
	}
	port, err := engine.NewMarkerPort(current)
	if err != nil {
		return nil, fmt.Errorf("headscale: %w", err)
	}
	withMarker, ops, err := engine.WithMarker(pol, port)
	if err != nil {
		return nil, fmt.Errorf("headscale: %w", err)
	}
	if err := l.SetPolicy(ctx, withMarker); err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.applied, l.marker, l.port = withMarker, ops, port
	l.mu.Unlock()
	if err := l.WaitReady(ctx, port); err != nil {
		return nil, err
	}
	return withMarker, nil
}

// StillHolds checks that every node still holds the policy this lab's
// last Apply set, by its readiness marker. Another process applying a
// policy to the same lab changes the marker: results read while it did
// weren't read under this run's policy.
func (l *Lab) StillHolds(ctx context.Context) error {
	l.mu.Lock()
	port := l.port
	l.mu.Unlock()
	if port == 0 {
		return errors.New("headscale: nothing was applied through this lab")
	}
	for _, n := range l.nodes {
		f, err := l.CompiledFilter(ctx, n)
		if err != nil {
			return err
		}
		if !f.HasMarker(port) {
			return fmt.Errorf("node %s no longer holds this run's policy: another process applied one to the lab", n.Name)
		}
	}
	return nil
}

// WaitReady waits, bounded, for every node to show the apply whose marker
// is on port.
func (l *Lab) WaitReady(ctx context.Context, port uint16) error {
	names := make([]string, len(l.nodes))
	for i, n := range l.nodes {
		names[i] = n.Name
	}
	return engine.WaitReady(ctx, names, func(ctx context.Context, i int) (engine.Netmap, error) {
		return l.Netmap(ctx, l.nodes[i])
	}, port, l.wait)
}

// SetPolicy sets headscale's policy exactly as given, with no marker and
// no wait: Apply is the way to change a lab's policy. It's here for
// checks that must see how headscale reads a policy as written, such as
// 0021's. A policy headscale rejects is a *engine.Refusal naming
// headscale's message.
func (l *Lab) SetPolicy(ctx context.Context, pol []byte) error {
	_, err := l.headscale(ctx, pol, "policy", "set", "-f", "/dev/stdin")
	var ee *execError
	if errors.As(err, &ee) {
		// A refusal only if headscale is otherwise well: a CLI that can't
		// reach the server fails the same way.
		if _, herr := l.headscale(ctx, nil, "health"); herr != nil {
			return fmt.Errorf("headscale: setting the policy: %w; and headscale isn't healthy: %w", err, herr)
		}
		return &engine.Refusal{Engine: Name, Reason: "headscale rejected the policy: " + ee.stderr}
	}
	if err != nil {
		return fmt.Errorf("headscale: setting the policy: %w", err)
	}
	return nil
}
