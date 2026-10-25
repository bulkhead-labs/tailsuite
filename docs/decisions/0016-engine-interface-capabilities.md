# 0016: The engine interface and the capability matrix

Status: **accepted** (2026-10-25). Settles #17. What results that
couldn't be proven do to CI is in [0015](0015-test-file-labels-profiles.md).

## Context

- **Two engines with opposite gaps** ([0002](0002-two-engines.md)).
- **A missing capability is REFUSED or UNKNOWN before anything runs**
  ([0001](0001-truth-over-mocks.md)).
- **Each engine gets the user's policy plus a recorded patch**
  ([0014](0014-tailnet-model-and-project-layout.md)).
- **headscale 0.29.5 rejects a policy with any feature it lacks**, one
  error at a time
  ([research 0005](../research/0005-tailnet-model-trial.md)).

## Decision

### The interface

```go
type Engine interface {
	Name() string
	Version() string            // for example "headscale 0.29.5"; part of every result
	Capabilities() Capabilities // data, per engine version
	Lower(model.Tailnet) (Lowering, error)
	Start(ctx context.Context, plan LabPlan, l Lowering) (Lab, error)
}

type Lab interface {
	Nodes() []Node
	CompiledFilter(ctx context.Context, n Node) (Filter, error) // tailscale debug netmap
	Teardown(ctx context.Context) error                          // idempotent
}
```

- **`Lower` is pure.**
  - It returns the RFC 6902 patch and every rewrite, with no I/O. So it's
    unit-tested and fuzzed without Docker, and shown to the user before
    the lab starts.
  - Where a rewrite can't be exact, it errs toward more access, never
    less. A wrong "allowed" is visible and gets investigated. A wrong
    "blocked" is the false "locked down" that 0001 calls the worst
    failure.
- **`Start`** brings up the control plane, joins the nodes through the
  sandbox ([0003](0003-docker-one-container-per-node.md)), and applies the
  configuration. On headscale it sets the policy after the nodes join,
  because headscale checks a policy's own tests against live devices
  (research 0005).
- **`Teardown` runs on every exit path.** `tailsuite down` can also run it
  after a crash, from the lab state in `.tailsuite/`.
- **`CompiledFilter` is required of every engine.** The conformance check
  in 0002, and the differential fuzzing in #42, compare these filters.

### Capabilities are data

Each capability an engine declares has one of three values:

- **native:** the engine does it as Tailscale does;
- **lowered:** the engine does it after a recorded rewrite;
- **refused:** the engine can't. A test that needs it is REFUSED on that
  engine, and the result names the capability and the engine.

Some conditions no engine can reproduce, so a test that depends on them is
UNKNOWN on both:

- a real macOS or Windows client (the lab mimics them, per
  [0009](0009-os-mimicry.md); results say "OS mimicked");
- completing an SSH check, which needs a person;
- traffic from the public internet, as Funnel needs, so Funnel is a
  config-only check (#32);
- posture attributes from a third-party integration, which the lab can't
  set.

The matrix lives in code next to each engine. `tailsuite capabilities`
prints it, and the docs page is generated from the same data, so the two
can't drift. Here it is as of headscale 0.29.5 and Tailscale's docs
(checked 2026-10-25):

| Capability | headscale | Tailscale engine |
|---|---|---|
| Grants, ACLs, ports, hosts, tags | native | native |
| Users and groups | native | lowered: one user becomes one tag (#6; the hard cases are #21) |
| `autogroup:member`, `autogroup:self` | native | refused in M1; lowered from M3 (#21) |
| Role autogroups (`autogroup:admin`…) | lowered: as tag owners, to the lab's admin; in rules, to the users with that role in `lab.yaml`, else refused | refused: no human users (Tailscale says users are coming) |
| Posture (`postures`, `srcPosture`, `defaultSrcPosture`) | refused; declined upstream ([headscale#2458](https://github.com/juanfont/headscale/issues/2458)) | native, OS mimicked |
| IP sets | lowered: expanded to addresses; refused when a set includes an autogroup | native |
| `via`, `autogroup:internet`, subnet routes, exit nodes | native (M3 proves them) | native |
| SSH accept and deny | native | native |
| SSH check | native; the demand is proven, completing it is UNKNOWN | the same |
| SSH session recording | refused | native, with a recorder node (M3) |
| Funnel | config-only | config-only |
| `attrConfig` | refused | native (M4) |
| Pinned IPv4 addresses | native (allocation, #5) | native (`POST /device/{id}/ip`) |
| Pinned IPv6 addresses | refused | refused |
| Compiled filter dump | native | native |

### How a test becomes REFUSED or UNKNOWN

1. **The planner computes each test's needs, before the lab starts:**
   - the features of every rule that could match the test's source and
     destination, ignoring the rules' conditions. This comes from the typed
     copy in 0014;
   - its probe, such as SSH or a check;
   - its conditions, such as posture.
2. **A need the engine refuses makes the test REFUSED on that engine**,
   naming the capability. A need no engine or lab can reproduce makes it
   UNKNOWN, naming the condition.
3. **Needs also become automatic labels**, `needs:<capability>` (0015), so
   a profile can send those tests to an engine that can prove them.
4. **A key the typed copy doesn't understand** follows 0014.

### Lowering for headscale when the policy uses refused features

headscale rejects the whole policy over a single refused feature. So the
headscale lowering removes what headscale refuses, and records each
removal in the patch:

- Every test whose source and destination a removed or altered rule could
  match is REFUSED. The rest of the suite still runs.
- **Conditions are removed, not the rules they guard.** A rule without its
  posture condition allows more, never less, so any mistake in the needs
  computation shows extra access instead of a false block.
- **`defaultSrcPosture` applies to every rule**, so a policy that uses it
  is REFUSED on headscale entirely.

## Why

- **A pure `Lower`** makes the riskiest code, rewriting a security policy,
  the easiest to test, and shows the user every rewrite before anything
  runs.
- **Capabilities as data** let the planner refuse before starting a lab,
  let profiles route by `needs:` labels, and keep the published matrix
  true.
- **Erring toward more access** matches what tailsuite promises: it may
  over-report exposure while the lab can't prove something, but it never
  reports a lockdown that isn't there.
- **Removing refused features** keeps pull-request runs on headscale
  useful for policies that use posture, instead of refusing everything.

## Evidence

- **headscale 0.29.5** rejected role autogroups as tag owners, `postures`,
  `ipsets`, SSH `recorder`, Funnel in `nodeAttrs`, and `attrConfig`. It
  accepted `via`, `autogroup:self`, `autogroup:internet`, SSH check mode,
  `sshTests` and `autoApprovers` (research 0005, 2026-10-24).
- **headscale's maintainer declined posture** in 2025 ("Headscale is not
  really a ZTNA solution"; headscale#2458, closed as not planned).
- **Tailscale's API-only tailnets** "have no human owner or users", and
  "future iterations of this feature will remove this limitation".
  Tailscale's docs, checked 2026-10-25.
- **The engines' other gaps are in
  [research 0002](../research/0002-engines.md).**

## Alternatives

- **Each engine checks capabilities while running**, failing midway. Tests
  would start, then die, and nothing could route them in advance.
- **REFUSED for the whole run whenever the policy uses anything an engine
  lacks.** It's simpler, but every posture user would lose headscale for
  pull requests.
- **Removing the rule instead of its condition.** It's more faithful to
  the rule's intent, but a mistake would show a false block.
- **A hand-written matrix in the docs.** It would drift from the code.

## Given up

- **headscale tests a narrower policy** when the real one uses features
  it lacks. Tests touching those features are REFUSED, so nothing false
  shows, but pull-request runs prove less for those policies.
- **The needs computation relies on the typed copy's static view.** A bug
  there could send a test to the wrong engine. Erring toward more access,
  the nightly Tailscale run and the conformance check (#42) bound the
  damage.
- **Each new Tailscale feature needs a capability entry.** That's 0014's
  forcing function again.
