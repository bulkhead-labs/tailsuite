# Architecture

**Status: designed, not built.** This describes the intended design; the
open questions are linked where they apply. Update it in the same pull
request as any change to it.

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
  On the Tailscale engine it applies the engine's copy of the policy. On
  headscale, until #20, tailsuite reads the module's values and applies
  them itself ([0014](decisions/0014-tailnet-model-and-project-layout.md)).

## Layers

| Layer | Interface | Implementations | Notes |
|---|---|---|---|
| **Sources** | `Source → TailnetModel` | The project's files; Tailscale API (read-only); OpenTofu plan JSON; later Pulumi and headscale | [0014](decisions/0014-tailnet-model-and-project-layout.md) |
| **Tailnet model** | Plain data | Our own types, in Tailscale's terms, independent of the source. The policy is the user's file as read, plus a read-only typed copy that lists every key it doesn't understand. Devices are keyed by MagicDNS name; users, DNS and settings follow. Each record says whether it was observed, declared or inferred. Only inputs are stored; effective state is computed | [0014](decisions/0014-tailnet-model-and-project-layout.md) |
| **Planner** | `TailnetModel + LabConfig → LabPlan` | Pure and deterministic. It chooses one representative node per equivalence class, the networks and the listeners. It lowers the policy for the chosen engine as a patch on the user's file, which records every rewrite | [0014](decisions/0014-tailnet-model-and-project-layout.md), [#22](https://github.com/bulkhead-labs/tailsuite/issues/22) |
| **Engines** | `Capabilities()`, `Create(plan)`, `Teardown()` | headscale; Tailscale throwaway tailnet; later NetBird | [0002](decisions/0002-two-engines.md), [#17](https://github.com/bulkhead-labs/tailsuite/issues/17) |
| **Sandbox** | Networks, nodes, exec, teardown | Docker (default); Podman through the same API; later a VM driver (Lima) and real macOS or Windows nodes for verify runs | [0003](decisions/0003-docker-one-container-per-node.md), [0009](decisions/0009-os-mimicry.md) |
| **Node agent** | Control over an out-of-band network, never over the tailnet | One small Go binary in every node, running its listeners and probes | [#7](https://github.com/bulkhead-labs/tailsuite/issues/7) |
| **Tests** | Each test declares the capabilities it needs | The runner turns a missing capability into REFUSED or UNKNOWN before anything runs; every deny gets a positive control | [#8](https://github.com/bulkhead-labs/tailsuite/issues/8), [#16](https://github.com/bulkhead-labs/tailsuite/issues/16) |
| **Outputs** | Reporters and writers | CLI, JSON, JUnit, PR comment, the UI; a policy file, a checklist, OpenTofu | |

## Results

Every test ends in one of four results:

- **PASS**: the traffic did what the test expected.
- **FAIL**: it didn't.
- **UNKNOWN**: the lab can't reproduce a condition the result depends on.
  For example, a posture rule on a client OS the lab only mimics, on an
  engine that can't mimic it.
- **REFUSED**: the engine doesn't support a feature the test needs, such as
  posture on headscale.

UNKNOWN and REFUSED are never shown as a pass or a deny.

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
  lab.yaml               # never deployed: the devices as the lab rebuilds them, what listens, engines
  tests/*.yaml           # tailsuite's tests
  flake.nix              # only with --nix: pins tailsuite and OpenTofu
  .tailsuite/            # gitignored: the model, the lab plan, results
```

The project alone rebuilds the lab, with no access to production. On
headscale it needs no credentials at all.

## Commands (planned)

| Command | Does |
|---|---|
| `tailsuite init --template <name> [--nix]` | Writes a project from a template embedded in the binary |
| `tailsuite up` | Starts the lab and the local UI |
| `tailsuite test` | Runs the suite; exit codes suit CI |
| `tailsuite down` | Tears the lab down |
| `tailsuite export tofu` | Writes `tofu/` (tailsuite's module and the values) from the current model, once; `init` and `import` use it, and the user owns the result |
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
