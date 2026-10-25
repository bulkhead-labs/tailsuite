# 0020: The node agent: it lives with the node, and sessions come and go

Status: **accepted** (2026-10-25). Built in #7. Builds on
[0019](0019-docker-sandbox.md), which put agent control on the sandbox's
exec channel.

## Context

- **#7:** one small Go agent in every node, controlled through the
  sandbox's exec channel. It runs TCP and HTTP listeners, and TCP-connect
  and HTTP-GET probes. Each probe reports success, refusal, timeout or
  error distinctly, with timings.
- **`tailsuite up` and `tailsuite test` are separate processes, and the
  lab outlives both.** The user journey runs `up` headless, then `test`
  ([plan](../plan.md#test-and-release-tiers)). A user may also keep `up`
  open for the UI while running `test` in another terminal.
- **Truth over mocks** ([0001](0001-truth-over-mocks.md)). A blocked path
  and a dead service look alike, and the benchmark produced two false
  denies: a stopped listener, and a client in userspace mode
  ([research 0004](../research/0004-benchmark-2026-10-23.md)). The agent's
  results are what the runner (#8) will build PASS and FAIL from.
- **A Tailscale filter drops denied packets silently**
  ([research 0001](../research/0001-landscape.md)). The sender sees no
  answer at all.

## Decision

### A separate, small binary

- **`tailsuite-agent`** (`cmd/tailsuite-agent`, logic in `internal/agent`)
  links only Go's standard library.
  - Stripped, it's 7.3 MB for linux/amd64 and 6.8 MB for linux/arm64.
  - The host copies it into each node through the sandbox, at
    `/opt/tailsuite/tailsuite-agent`.
- **The tailsuite binary will carry it** for both architectures, because a
  Mac's lab nodes are Linux. That's #52, with `tailsuite up` (#12) and the
  release (#2). Until then the tests build it on the fly.

### The agent lives with the node, and owns its listeners

- **`tailsuite-agent run --node <name> [-- <command>]` is the node's
  entrypoint.**
  - It serves sessions on a Unix socket inside the node,
    `/run/tailsuite/agent.sock`, in a directory only root can enter.
  - It runs the node's own command under it, such as tailscaled. It passes
    SIGTERM on, and exits with the command's exit code, so the container
    stops when the command does.
  - `control.Entrypoint` builds this, and the engines (#5, #6) give it
    their command.
- **The sandbox runs every node with an init process (docker-init) as PID
  1.** It reaps the orphans that exec'd commands leave, and passes signals
  to the agent.
- **A session is `tailsuite-agent connect`,** run through the sandbox's
  `Exec`. It bridges the session's stdin and stdout to the socket.
  - It waits up to 10 s for the agent to start listening, so a host can
    connect as soon as the node starts.
  - **Each tailsuite process opens its own sessions.** `control.Start` opens
    one, and checks the agent's hello: the protocol version, and that it's
    the right node.
- **Listeners belong to the agent, not to a session.** What `up` opens,
  `test` finds. They close when the agent exits, which is when the node
  stops.

### Listeners are declared, never added up

- **`set_listeners` makes a node's listeners exactly the set asked for:**
  - it closes those not wanted;
  - it keeps those already open;
  - it opens the rest;
  - a port whose protocol changes is reopened.
- **So it's idempotent.** `up` and `test` both declare the set from
  `lab.yaml`, at any time, in any order, and the result is the same.
- **A listener can't go stale.** If `lab.yaml` drops one, the next
  declaration closes it, so a probe can't succeed against a service the
  lab no longer has.
- `listeners` lists what's open. A port something else in the node holds
  is reported as an error, and the rest still open.

### The protocol

- **Newline-delimited JSON.** Every request has an ID, and its response
  carries the same ID.
- **Requests run concurrently,** so responses can come back in any order.
- **Ops:** `hello`, `set_listeners`, `listeners` and `probe`.
- **`ProtocolVersion` is 1.** The host refuses an agent that speaks
  another.
- **A request that isn't JSON gets an error, and the session goes on.**
- Durations travel as Go duration strings (`"1.5s"`).

### Listeners prove who answered

- **A TCP listener sends `tailsuite-agent <node>` on accept.** An HTTP
  listener answers 200, with the node's name in `X-Tailsuite-Node` and in
  the body.
- **A probe reports the name as `Peer`.** The runner can check that the
  node it meant to reach is the one that answered, not merely that
  something did.
- Listeners bind every interface unless told otherwise, as services in
  production usually do.

### Outcomes are never merged

| Outcome | Means | Typical cause |
|---|---|---|
| `success` | The connection opened, and for HTTP, a response came back | Allowed, and something listens |
| `refused` | The target answered that nothing listens on that port | The packets got through: no filter dropped them |
| `timeout` | No answer within the timeout | A Tailscale filter drop, or a node that's down |
| `error` | Anything else, with the detail | No route, a bad address |

- **`Connected` says whether the TCP connection opened.** An HTTP server
  that accepts and never answers is a connected timeout, which a drop
  never is.
- **`Connect` and `Total` are timings, for reporting only.** The plan's
  stability rules say results never depend on timings.
- **A probe never goes through a proxy**, and never follows a redirect:
  the probe is the traffic under test.
- The default timeout is 3 s per probe, and a probe can set its own.
- **The agent reports what it saw. It never decides allow or deny.** It
  doesn't know the policy, nor whether its target is alive. Pairing a
  deny with its positive control is the runner's job (#8, 0001).

## Why

- **Listeners that live with the node** fit a lab that outlives every
  command. `up` and `test` can't fight over a port, because neither owns
  it: the agent does.
- **Declaring the whole set** makes concurrent and repeated runs safe, and
  removes the stale listener that would make a probe pass against a
  service the lab no longer has.
- **The node's command runs under the agent,** so the agent is there for
  the node's whole life, and the container still stops when tailscaled
  does.
- **The standard library keeps the agent small**, and there's nothing in
  it to pin or audit but Go itself.
- **Concurrent requests** let one node probe many targets at once.
- **`Peer`** catches a probe that reached the wrong node, say through a
  mistaken address in the plan.
- **Distinct outcomes** are what let the runner tell a deny (`timeout`)
  from a dead service (`refused`), the confusion 0001's positive controls
  exist to catch.

## Evidence (checked 2026-10-25)

- **Integration tests** (`go test -tags integration ./internal/agent/control/`)
  ran against Docker 26.1.5, in nodes on an internal lab network, with the
  agent as each node's entrypoint. All five pass in about 11 s, and
  nothing is left behind:
  - from one node to another:
    - a TCP listener and an HTTP listener: `success`, with the peer's
      name;
    - a port with nothing on it: `refused`, for both protocols;
    - a port the target drops with iptables, as a Tailscale filter
      would: `timeout`, not connected;
    - TEST-NET-1 (192.0.2.1), which has no route there: `error`;
  - listeners one session opened still answer after it closes. Two
    sessions at once declare the same set, and nothing changes. Declaring
    none closes the listener for every session;
  - a node with every iptables chain set to DROP still answers over the
    exec channel. Control is out of band;
  - tailscaled runs under the agent in kernel mode: `CheckKernelTUN`
    passes;
  - a node without the agent fails `control.Start`.
- **Unit tests** run with `-race`, twice over. They cover:
  - the protocol, and 50 concurrent probes;
  - listeners outliving sessions;
  - declaring sets, including a protocol change, an empty set, a busy
    port, and eight sessions declaring at once;
  - classifying wrapped errors;
  - a connected HTTP timeout, and a foreign TCP listener (`success`, no
    peer);
  - the daemon: its command's exit code, stopping the command on SIGTERM,
    a command that can't start;
  - `connect` waiting for a late agent, and giving up.
- **The tests fail when the code is broken.** Three faults were planted,
  one at a time:
  - listeners closed when a session ends: the session test failed;
  - unwanted listeners left open: the declaration test failed;
  - refusals misclassified: the refusal and classification tests failed.
- **docker-init** ships with Docker 26.1.5 (`docker info`: `docker-init`),
  and runs as PID 1 with `--init`.

## Alternatives

- **Listeners that live in one session**, closed when it ends. That was
  this record's first version, before review. It was simpler, with no
  socket inside the node. But `up` and a separate `test` would both have
  to open the same ports, and the headless journey's `test` would find
  none.
- **The agent started detached through exec,** rather than as the
  entrypoint. Nothing would supervise it, and its output would be lost.
- **Adding and removing listeners one at a time.** Concurrent processes
  could leave a set that matches neither's `lab.yaml`, and stale
  listeners would stay.
- **The agent as a mode of the tailsuite binary.** One binary, but a Mac
  would have to carry two Linux builds of all of tailsuite (about 30 MB
  each) instead of two 7 MB agents.
- **JSON-RPC 2.0, or gRPC.** JSON-RPC's envelope adds nothing here. gRPC
  would add generated code and a dependency to the agent.
- **Counting `refused` as a deny.** It isn't one: a refusal proves the
  packets got through.

## Given up

- **Every node runs the agent as its entrypoint**, so the engines wrap
  their command in it, and an image's own entrypoint has to be named
  explicitly.
- **Listeners outlive the processes that declared them**, until the next
  declaration or teardown. A run that declares nothing finds what the last
  one left; the runner (#8) declares the set before every run.
- **Probes run inside the node's own network stack.** They test what the
  node can reach, not traffic forwarded through it. Subnet routers and
  exit nodes are M3.
- **Only TCP and HTTP so far.** SSH, HTTPS, Postgres and UDP come in later
  milestones, as #7 says.
- **No embedded agent yet** (#52). Until then, nothing outside the tests
  can start one.
