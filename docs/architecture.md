# Architecture

**Status: designed; M1 is being built.** Built so far:

- the Go module and its CI ([0018](decisions/0018-go-toolchain-flake-ci.md));
- the Docker sandbox, `internal/sandbox`
  ([0019](decisions/0019-docker-sandbox.md));
- the node agent, `cmd/tailsuite-agent` and `internal/agent`
  ([0020](decisions/0020-node-agent.md)), which the tailsuite binary
  carries for linux/amd64 and linux/arm64, `internal/agentbin`
  ([0027](decisions/0027-embedded-agent.md));
- the tailnet model and loading a project, `internal/model` and
  `internal/project` ([0022](decisions/0022-reading-a-project.md));
- the engine interface and the headscale engine, `internal/engine` and
  `internal/engine/headscale` ([0025](decisions/0025-headscale-engine.md));
- the test suite and the runner, `internal/suite` and `internal/runner`
  ([0026](decisions/0026-test-runner.md));
- the CLI, `cmd/tailsuite`: `init`, `up`, `test`, `down` and
  `capabilities` ([0030](decisions/0030-the-cli.md),
  [0033](decisions/0033-up-start-and-stop-options.md));
- the local UI, `internal/ui`
  ([0031](decisions/0031-the-local-ui-built.md)), which `tailsuite up`
  serves while it holds the lab;
- tailsuite's OpenTofu module, the allow-list over `tofu/`, the
  Tailscale engine's run directory, and editing the values,
  `internal/tofu` and `internal/project`
  ([0032](decisions/0032-the-opentofu-module.md)). `init` writes the
  module, `upgrade tofu` replaces it, and a lab whose `tofu/` applies
  anything else is refused. The run directory is applied once the
  Tailscale engine lands (#6).

The rest describes the intended design, and the open questions are linked
where they apply. Update it in the same pull request as any change to it.

## The picture

```
 sources                         the lab (Docker, local)                     outputs
 ───────                         ───────────────────────                     ───────
 policy file ──┐                ┌ engine: headscale container          ┐    headline + matrix
 Tailscale API ├─► tailnet ─► plan ┤      or a throwaway Tailscale tailnet  ├─► blast-radius diff
  (read-only)  │    model        │ one container per node class           │    JUnit, PR comment
 OpenTofu ─────┘                 │ real tailscaled, kernel TUN            │    policy file, checklist
  plan JSON                      │ node agent: listeners + probes         │    the local UI
                                 └ LANs behind routers, a fake internet   ┘
                 OpenTofu applies the tailnet config ──► the lab, and later production
```

**tailsuite owns the machines; OpenTofu owns the config**
([0004](decisions/0004-opentofu-owns-config.md)):

- **tailsuite** creates and tears down containers, networks, the control
  plane and the agents.
- **OpenTofu**, with the official Tailscale provider, applies the tailnet's
  configuration: the policy, device tags, routes, DNS and settings. It
  applies the same module to the lab and, at deploy time, to production.
  A lab never runs it in the user's `tofu/`, which holds production's
  credentials and state. On the Tailscale engine, tailsuite applies the
  module from its own run directory, with the engine's copy of the policy.
  On headscale, until #20, tailsuite reads the module's values and applies
  them itself ([0014](decisions/0014-tailnet-model-and-project-layout.md)).

## Layers

| Layer | Interface | Implementations | Notes |
|---|---|---|---|
| **Sources** | `Source → TailnetModel` | The project's files; Tailscale API (read-only); OpenTofu plan JSON; later Pulumi and headscale | [0014](decisions/0014-tailnet-model-and-project-layout.md) |
| **Tailnet model** | Plain data | **Built** (`internal/model`, `internal/project`). Our own types, in Tailscale's terms, independent of the source. The policy is the user's file as read, plus a read-only typed copy that lists every key it doesn't understand. Devices are keyed by MagicDNS name, and users by login, with their roles; DNS and settings follow. Each record says whether it was observed, declared or inferred. Only inputs are stored; effective state is computed | [0014](decisions/0014-tailnet-model-and-project-layout.md), [0022](decisions/0022-reading-a-project.md) |
| **Planner** | `TailnetModel + LabConfig → LabPlan` | Pure and deterministic. It chooses one representative node per equivalence class, the networks and the listeners. It computes each test's needs, and calls the chosen engine's `Lower`, which returns a patch on the user's file that records every rewrite | [0014](decisions/0014-tailnet-model-and-project-layout.md), [0016](decisions/0016-engine-interface-capabilities.md), [#22](https://github.com/bulkhead-labs/tailsuite/issues/22) |
| **Engines** | `Capabilities()`, `Lower(model)`, `Start(plan)`; the lab: `CompiledFilter(node)`, `Teardown()`. headscale's also has `Attach`, which reopens a lab another process started, and `StillHolds`, which checks no other process applied a policy since ([0030](decisions/0030-the-cli.md)) | **Built:** headscale 0.29.5 (`internal/engine/headscale`): its own internal network, TLS from the run's own CA, its embedded DERP server, the policy set through its CLI with no restart. Its copy of the policy leaves out what headscale can't take, and records each change; role autogroups are lowered to lab.yaml's roles; pinned addresses aren't honoured there yet (#67). Later: the Tailscale throwaway tailnet (#6), NetBird. Each declares every capability as native, lowered (by a rewrite that's exact for every test it touches) or refused. Lowering is pure, and every test an inexact rewrite would touch is REFUSED. `Start` returns once every node's filter holds the apply's readiness marker and every peer its filters call for | [0002](decisions/0002-two-engines.md), [0016](decisions/0016-engine-interface-capabilities.md), [0021](decisions/0021-empty-grants-and-the-marker-on-tailscale.md), [0025](decisions/0025-headscale-engine.md) |
| **Sandbox** | `CreateNetwork`, `StartNode`, `Exec`, `Teardown`; `sandbox.Run` tears down however a run ends. Docker's `Attach` and `Nodes` reopen a running run | **Built:** Docker, through the official client (Podman through the same API). Every resource is named and labelled with its run's ID, so teardown works after a crash. Lab networks are internal unless an engine needs the internet: no route out, though a node can still reach the host through the network's gateway (#54). Docker Engines whose internal networks leak DNS (CVE-2024-29018) are refused; Podman isn't checked. Tailnet nodes get a kernel TUN device. Userspace mode is refused before start, and `CheckKernelTUN` catches it on a running node; engines call it on every tailnet node (the headscale engine does; #6). Later: a VM driver (Lima) and real macOS or Windows nodes for verify runs | [0003](decisions/0003-docker-one-container-per-node.md), [0009](decisions/0009-os-mimicry.md), [0019](decisions/0019-docker-sandbox.md) |
| **Node agent** | Each tailsuite process opens its own sessions through the sandbox's exec channel (newline-delimited JSON), never over a network, so never the tailnet. `set_listeners` declares a node's whole set of listeners | **Built:** `tailsuite-agent`, a small stdlib-only binary and each node's entrypoint, running the node's own command under it. It owns the node's listeners, so they outlive sessions, and `up` and `test` share them. TCP and HTTP listeners name their node; TCP and HTTP probes have four outcomes that are never merged: success, refused, timeout (what a filter drop looks like), error | [0019](decisions/0019-docker-sandbox.md), [0020](decisions/0020-node-agent.md) |
| **Tests** | YAML files in `tests/`, plus the policy's own `tests` and `sshTests` | **Built** (`internal/suite`, `internal/runner`, [0026](decisions/0026-test-runner.md)), run by `tailsuite test`. A test names devices or identities (every matching device), probes the real protocol, and expects allow, deny or, for SSH, check. The planner computes each test's needs, and turns a missing capability into REFUSED or UNKNOWN before anything runs. Every deny gets a positive control. Labels and profiles choose what runs where | [0015](decisions/0015-test-file-labels-profiles.md), [0016](decisions/0016-engine-interface-capabilities.md), [0026](decisions/0026-test-runner.md) |
| **Outputs** | Reporters and writers | **Built:** text, JSON and JUnit (`internal/runner`), through `tailsuite test --json` and `--junit`. The local UI (`internal/ui`, [0031](decisions/0031-the-local-ui-built.md)): a map of the lab drawn from the policy, unverified, then each run's results over Server-Sent Events, the same JSON as `tailsuite test --json`. Later: the CLI's flags, a PR comment; a policy file, a checklist, OpenTofu | [0015](decisions/0015-test-file-labels-profiles.md), [0017](decisions/0017-local-ui.md) |

## Results

Every test ends in one of four results:

- **PASS**: the traffic did what the test expected.
- **FAIL**: it didn't.
- **UNKNOWN**: the lab can't reproduce a condition the result depends on.
  For example, a posture rule on the device's country (`ip:country`), or
  completing an SSH check, which needs a person.
- **REFUSED**: the engine doesn't support a feature the test needs, such as
  posture on headscale.

UNKNOWN and REFUSED are never shown as a pass or a deny. A test that a
profile or flag leaves out is counted as skipped, never dropped silently.

**In CI** ([0015](decisions/0015-test-file-labels-profiles.md)), a FAIL
always fails the run. UNKNOWN and REFUSED are listed separately, with where
they can be proven. They fail the run only under `unproven: fail` or
`--strict`, which nightly runs and release gates use.

## A user's project

A folder the user owns. Nobody clones tailsuite. **A fact lives in the
file for whatever makes it true in production**, so each fact has one home.
The one exception, auto-approved routes, is in
[0014](decisions/0014-tailnet-model-and-project-layout.md):

```
my-tailnet/
  policy.hujson          # the policy, the only copy: Tailscale's policy engine applies it
  tofu/
    tailsuite.tf         # tailsuite's module: logic only
    tailnet.auto.tfvars  # device tags, enabled routes: the API applies them, through OpenTofu
  lab.yaml               # never deployed: the devices as the lab rebuilds them, what listens, profiles
  tests/*.yaml           # tailsuite's tests
  flake.nix              # only with --nix: pins tailsuite and OpenTofu
  .tailsuite/            # gitignored: the lab that's up (lab.json), a detached up's log, the last results
```

The project alone rebuilds the lab, with no access to production. On
headscale it needs no credentials at all.

## Commands

Built ([0030](decisions/0030-the-cli.md)); `up`, `test` and `down` work on
the project in the current folder, or `--dir`:

| Command | Does |
|---|---|
| `tailsuite init --template <name> [--nix] [dir]` | Writes a project from a template embedded in the binary, never over an existing one. `--nix` adds a flake pinning this release and OpenTofu |
| `tailsuite up [--profile <name>] [--engine <name>] [--detach] [--clean] [--no-browser] [--no-redirect-file]` | Starts the lab, with the embedded node agent, and holds it until Ctrl-C, which tears it down. `--detach` holds it in a background process instead, until `down`; `--clean` first tears down this project's earlier labs; `lab.yaml`'s `up:` sets both defaults ([0033](decisions/0033-up-start-and-stop-options.md)). While it holds the lab, it serves the local UI and opens it in the browser (not in CI, or with `--no-browser`; `--no-redirect-file` skips the redirect file); localhost only, behind a one-time link, and Enter prints a fresh one ([0017](decisions/0017-local-ui.md), [0031](decisions/0031-the-local-ui-built.md)) |
| `tailsuite test [--profile <name>] [--engine <name>] [--only <label>] [--skip <label>] [--run <regex>] [--strict] [--json] [--junit <file>]` | Runs the suite on the lab `up` holds, after applying the current policy, or on a lab of its own. The profile picks the engine, the tests and the strictness, and flags override it. Exit codes: 0 fine, 1 a FAIL, 2 nothing proven or unproven under strict, 3 couldn't run ([0015](decisions/0015-test-file-labels-profiles.md)) |
| `tailsuite capabilities [--json]` | Prints what each engine does natively, after a rewrite, or refuses ([0016](decisions/0016-engine-interface-capabilities.md)) |
| `tailsuite down [--run <id> \| --all]` | Tears down the project's lab (ending a detached `up`), a run by its ID, or every run |

Planned:

| Command | Does |
|---|---|
| `tailsuite export tests` | Copies the tests that fit Tailscale's format into the policy's `tests` and `sshTests`, after showing the diff ([0015](decisions/0015-test-file-labels-profiles.md)) |
| `tailsuite upgrade tofu` | Replaces `tofu/tailsuite.tf` with this release's copy, leaving the values alone (#9, [0014](decisions/0014-tailnet-model-and-project-layout.md)) |
| `tailsuite export tofu` | Writes `tofu/` (tailsuite's module and the values) from the current model, once; `import` uses it, and so will `init` once the module exists (#9). The user owns the result |
| `tailsuite import tailscale` | Reads a real tailnet read-only into a project, with OpenTofu import blocks |
| `tailsuite deploy` | Lab tests → `tofu plan` against production → blast-radius diff → apply, behind the gates in [0007](decisions/0007-read-only-by-default.md) |

## Distribution

One signed Go binary, built by goreleaser
([0005](decisions/0005-go-single-binary-nix.md)):

- **Channels:** Homebrew, an install script, `go install`, release
  binaries, a container image and a GitHub Action.
- **Signed** with cosign, with an SBOM and provenance.
- **Embedded:** the UI and the node agent.
- **Pinned:** lab images by digest.
- **OpenTofu** is fetched pinned and checksummed if it isn't installed.

Templates live in the binary. A public quick-start template repository is
generated from the same source on each release, and is never edited by hand.
