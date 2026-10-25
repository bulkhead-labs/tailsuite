# 0019: The Docker sandbox, and agents controlled through it

Status: **accepted** (2026-10-25). Built in #4. Amends
[0003](0003-docker-one-container-per-node.md): agents are controlled
through the sandbox's exec channel, not over a separate network.

## Context

- **0003:** Docker is the sandbox, Podman works through the same API, and
  there's one container per node, with kernel TUN and userspace refused.
  "Agents are controlled over a separate network, never over the tailnet."
- **#4** left one choice open: the Docker SDK directly, or
  testcontainers-go. It also asked for unique names, and teardown that
  always runs, even on interrupt.
- **The lab outlives a command.** `tailsuite up` starts a lab and leaves
  it running; `tailsuite down` removes it, after a crash too (#12).
- **CLAUDE.md rule 3:** never send traffic outside the lab.

## Decision

### The Docker SDK, directly

- The sandbox speaks the Docker Engine API through the official client,
  `github.com/moby/moby/client` v0.6.2 with `moby/moby/api` v1.56.1.
  - `github.com/docker/docker` stopped at v28.5.2 (2025-11-05), and the
    client moved to its own module.
  - It negotiates the API version, from 1.40 to 1.56.
- **Not testcontainers-go.** Its reaper (Ryuk) removes containers when the
  process that made them exits. That suits tests, not a lab that `up`
  leaves running for `down` to remove. Without the reaper, it adds little
  over the client, and a second set of dependencies.
- **It connects where `DOCKER_HOST` says, or to the default socket.**
  Docker contexts (Colima, OrbStack, Rancher Desktop) are #49.
- **It refuses a Docker Engine whose internal networks leak DNS:**
  anything before 26.0.0 except 25.0.5+ and 23.0.11+ (24.x was never
  fixed).
  - Those engines forward DNS queries from an internal network out
    through the host's resolver, when that resolver is on a loopback
    address (CVE-2024-29018). A lab node's lookup of Tailscale's control
    server would leave the lab.
  - Docker Desktop isn't affected, and reports a fixed version anyway.
  - **Podman isn't checked:** its networking and DNS (aardvark-dns) are
    its own, and the advisory doesn't cover them.

### The interface

`internal/sandbox`:

```go
type Sandbox interface {
	RunID() string
	CreateNetwork(ctx, NetworkSpec) (Network, error)
	StartNode(ctx, NodeSpec) (Node, error)
	Exec(ctx, Node, ExecSpec) (exitCode int, err error)
	Teardown(ctx) error // idempotent
}
```

- **Every resource carries the run's ID.**
  - Names are `tailsuite-<run>-<name>`. A run ID is 8 random hex digits,
    and names are DNS labels.
  - Labels are `com.bulkheadlabs.tailsuite.run`, `.name` and `.role`.
- **Teardown finds a run by its label**, so it needs no state file.
  - It force-removes the containers, then the networks, and is idempotent.
  - It looks again until a pass finds nothing, up to five passes, 200 ms
    apart. A node whose create was in flight when a run was interrupted
    can appear after the first pass.
  - `TeardownRun` does the same from a run ID alone, for `tailsuite down`
    after a crash. `Runs` lists the runs that still have anything.
- **`sandbox.Run(ctx, sb, fn)` always tears down:** when `fn` returns,
  fails or panics. `fn` must return when `ctx` is cancelled, as by an
  interrupt through `signal.NotifyContext`; then `Run` tears down too.
  - Teardown gets a context of its own, bounded to two minutes, so a
    cancelled `ctx` doesn't stop it.
  - A process killed outright (SIGKILL) can't tear down. That's what
    `TeardownRun` is for.
- **Images are pinned by digest.** `StartNode` refuses a reference without
  `@sha256:`. It pulls a missing image.
- **Lab networks are internal by default:** no route out of the host, and
  on the engines the sandbox accepts, no DNS out of it either. So a
  headscale lab can't send traffic outside the lab even by mistake (rule
  3). A network opts into the internet only for the Tailscale engine,
  which needs Tailscale's control plane.
- **A node has 128 MiB of memory and no swap**, unless its spec says
  otherwise. The 2026-10-23 benchmark measured 20–31 MiB per tailscaled.

### Kernel TUN, and userspace refused twice

- **Every tailnet node gets `/dev/net/tun` and `NET_ADMIN`, and nothing
  more:** not privileged. It also gets `TS_USERSPACE=false`, because the
  official image defaults to userspace.
  - A node is a tailnet node unless its spec says it's a service, such as
    the control plane. So TUN is the default, and a spec opts out of it,
    never in.
- **Refused before anything is created:** a tailnet node whose
  configuration asks for userspace networking.
  - That's `TS_USERSPACE` set to anything but `false`, or
    `userspace-networking` in its command, its entrypoint or
    `TS_TAILSCALED_EXTRA_ARGS`.
- **Checked once it runs:** `CheckKernelTUN` reads
  `/sys/class/net/tailscale0/tun_flags` in the node.
  - In userspace mode there's no such interface (`cat` exits 1), and the
    result is `ErrUserspace`. Any other failure is a failed check, not a
    diagnosis.
  - **Engines must call it on every tailnet node once tailscaled is up,
    and fail the run if it fails** (#5, #6). The spec check can't see what
    an image's own scripts do.

### Agents are controlled through exec, not a network (amends 0003)

- **`Exec` runs a command in a node through the sandbox's API**, with
  stdin, stdout and stderr, and returns its exit code.
  - It runs only in the run's own nodes.
  - An exit code is trusted only from a command the daemon says started
    (it reports a PID). A command that never started, such as one the
    image doesn't have, is an error. The daemon gives it exit 126, which a
    caller could read as a failed probe.
- **The node agent (#7) is controlled the same way.** Each tailsuite
  process that needs an agent opens its own exec session to it, and the
  session's stdin and stdout carry the agent's protocol. So there is no
  control network.
  - `tailsuite up` and `tailsuite test` are separate processes, and the
    lab outlives both. So the agent's state, such as its listeners, can't
    belong to any one session (#7 decides how).
- **It meets 0003's reason**, that a deny rule must never cut off the
  test's own control traffic. That traffic doesn't touch any network in
  the lab, the tailnet included.

## Why

- **The exec channel works wherever the sandbox does.** That's Docker on
  Linux, Docker Desktop on macOS, Podman, and a remote daemon. A control
  network reachable from the host would need published ports, and those
  behave differently on each:
  - Docker Desktop can't route to container addresses;
  - published ports land on the remote machine when `DOCKER_HOST` points
    elsewhere;
  - Docker doesn't publish ports from internal networks, which are the
    default here.
- **It needs no authentication of its own.** Only someone who can use the
  Docker API can exec into a node, and they already control the
  containers. A published port on 127.0.0.1 would let any local process
  command an agent unless the agent checked a token.
- **It carries over to later sandboxes.** A VM (Lima) or a real macOS
  machine for verify runs (0009) gives the same thing over SSH: a
  process's stdin and stdout.
- **Labels make teardown work after a crash**, with nothing to read from
  disk first.
- **Internal networks turn rule 3 into a property of the lab**, rather
  than a habit of the code.

## Evidence (checked 2026-10-25)

- **Versions** (Go module proxy):
  - `moby/moby/client` v0.6.2, 2026-10-08;
  - `moby/moby/api` v1.56.1, 2026-10-01;
  - `docker/docker`'s last release, v28.5.2, 2025-11-05;
  - testcontainers-go v0.44.0, 2026-08-07.
- **The test image:** `tailscale/tailscale:v1.102.5`, pinned by its index
  digest `sha256:c507f3a2…b4065`.
  - It was released 2026-09-29, and the digest was pushed 2026-09-24.
  - v1.104.1, released 2026-10-07, has a digest pushed 2026-10-13: too
    new for the two-week rule.
  - The engines pin their own client versions (#5, #6).
- **Integration tests** (`go test -tags integration ./internal/sandbox/docker/`),
  against Docker 26.1.5 (API 1.45). All ten pass in about 8 s:
  - a tailnet node's tailscaled creates `tailscale0` as a kernel TUN
    device, both when started directly and through the official image's
    own entrypoint, which honours `TS_USERSPACE=false`;
  - an internal network gives no default route, and a network with
    `Internet` does (the positive control);
  - a userspace tailnet node is refused, and `CheckKernelTUN` flags a
    service that runs tailscaled in userspace mode;
  - `Exec` carries stdin, stdout, stderr and exit codes, and stops
    waiting when its context ends. It refuses another run's node of the
    same name, and a command the image doesn't have is an error, not
    exit 126;
  - files are copied in before a node starts, and `/tmp` keeps its mode
    1777 when one is copied there;
  - names and labels are right, and duplicates and other runs' networks
    are refused;
  - tearing one run down leaves another alone, and a second teardown
    succeeds;
  - SIGINT to a process with a running lab leaves nothing behind.
- **The tests fail when the code is broken.** Six faults were planted,
  one at a time:
  - without the node ID check, `Exec` ran in another run's `db`, and the
    test caught it;
  - with only a 0 refused from a command that never started, the
    missing-command test got 126;
  - with directory entries in the archive, `/tmp` became 0755, and the
    test caught it;
  - without the TUN device, tailscaled exits, and the TUN test fails;
  - without `sandbox.Run`, the interrupt test finds the run still there;
  - with the network not internal, the test finds a default route via
    the bridge's gateway.
- **CVE-2024-29018**
  ([GHSA-mq39-4gv4-mvpx](https://github.com/moby/moby/security/advisories/GHSA-mq39-4gv4-mvpx),
  published 2024-03-20): affected ranges `< 23.0.11`, `>= 25.0.0, < 25.0.5`,
  and 26.0.0's first release candidates. "Moby releases 26.0.0-rc3, 25.0.5
  and 23.0.11 are patched"; there's no 24.x fix. Unit tests cover the
  versions on both sides of each line, and that Podman is let through.
- **govulncheck v1.8.0** found no vulnerable dependency. It found one
  vulnerability in Go 1.27.2's standard library, GO-2026-6809 in
  `crypto/x509`, reachable through the Docker client. It's fixed in
  1.27.3, which can be pinned from 2026-11-02 (#50, and
  [0018](0018-go-toolchain-flake-ci.md)).

## Alternatives

- **testcontainers-go.** Built for tests, with a reaper that ends the lab
  with the process.
- **Shelling out to the `docker` CLI.** No Go dependencies, but it parses
  text output, needs the CLI installed, and costs a process per call.
- **A control network, as 0003 said.** A published port per agent on
  127.0.0.1, with a token. It works on Linux, needs care on Docker Desktop,
  breaks with a remote daemon, and can't be used on internal networks.
- **A Unix socket per agent in a bind-mounted directory.** It needs no
  network, but it doesn't cross Docker Desktop's VM boundary.

## Given up

- **The exec channel depends on the Docker API**, so every message to an
  agent passes through the daemon. That's fine at tens of nodes. Equivalence
  classes (#22) keep labs small.
- **A create the daemon finishes after teardown's last pass is left
  behind.** Looking again narrows that window but can't close it. `Runs`
  lists such a run, and `TeardownRun` removes it.
- **Podman isn't checked** for leaking DNS from internal networks.
- **A cancelled `Exec` leaves its process running**, because the Docker
  Engine can't stop an exec'd process. It ends when the node is torn
  down.
- **Copied files arrive without their parent directories' modes.**
  Docker creates a missing parent root-owned, with mode 0755. The archive
  holds no directory entries, because Docker would apply them to
  directories that already exist: copying a file into `/tmp` would make
  `/tmp` 0755.
- **The integration tests need Docker**, so they sit behind a build tag.
  CI lints and compiles them. Running them in CI is #13's integration
  tier.
- **20 more Go modules**, most of them OpenTelemetry and OCI types that
  the Docker client needs.
- **Docker contexts aren't followed yet** (#49). On Colima or Rancher
  Desktop, users set `DOCKER_HOST` until then.
