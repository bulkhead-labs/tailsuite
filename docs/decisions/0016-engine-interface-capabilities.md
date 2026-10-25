# 0016: The engine interface and the capability matrix

Status: **accepted** (2026-10-25). Settles #17. What results that
couldn't be proven do to CI is in [0015](0015-test-file-labels-profiles.md).

Refines earlier records:

- [0002](0002-two-engines.md): headscale matches users by email, so users
  are native there, not rewritten. IP sets, which 0002 lists as
  unsupported, are lowered on headscale.
- [0003](0003-docker-one-container-per-node.md): rules that name a
  Tailscale IPv6 address are refused, not only flagged.
- [0014](0014-tailnet-model-and-project-layout.md): a user's role (admin,
  owner…) is a fact the identity provider or the admin console makes true,
  never deployed, so it lives in `lab.yaml`, beside the user.

## Context

- **Two engines with opposite gaps** (0002).
- **A missing capability is REFUSED or UNKNOWN before anything runs**
  ([0001](0001-truth-over-mocks.md)).
- **Each engine gets the user's policy plus a recorded patch** (0014).
- **headscale 0.29.5 rejects a policy with any feature it lacks**, one
  error at a time
  ([research 0005](../research/0005-tailnet-model-trial.md)). With no
  policy, or one with no `grants` or `acls`, it allows all traffic.

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

- **`Lower` is pure.** It returns the RFC 6902 patch and every rewrite,
  with no I/O. So it's unit-tested and fuzzed without Docker, and shown to
  the user before the lab starts.
- **`Start` returns only when the lab is ready:**
  - the control plane is up;
  - the nodes have joined through the sandbox (0003);
  - the configuration is applied;
  - every node shows the applied policy, in its packet filter and its list
    of peers, and from M3 in its SSH policy.

  How it waits:
  - **A marker shows a node has the new policy.**
    - `Start` adds one rule to each apply: from an address no node holds,
      on a port unique to that apply.
    - The patch shown to the user labels it as tailsuite's.
    - `Lower` stays pure, because the marker isn't part of it.
  - **A policy with no `acls` section and no `grants` section gets its
    default made explicit first.**
    - Both engines allow everything when both sections are absent. With
      the marker alone, they'd allow only the marker.
    - An empty section, such as `"acls": []`, is different: both engines
      deny everything. Such a policy gets no default.
    - **An empty `grants` section with no `acls` section is REFUSED on
      both engines for now.** headscale denies everything for it.
      Tailscale's docs, read literally ("in the absence of an acls
      section"), might allow everything. The #3 spike checks how Tailscale
      reads it.
    - So the patch first adds `{"src": ["*"], "dst": ["*"], "ip": ["*"]}`,
      which is exact, and then the marker.
  - **tailsuite waits** until every node's compiled filter holds the
    marker, and its peer list has every peer the plan expects.
  - **To check before the engines rely on it:** an allow-all rule covers
    the marker, so the marker has to survive under one. #5 checks that on
    headscale, and the #3 spike on Tailscale.
  - **The SSH policy gets its own marker from M3**, when SSH ships. M3
    settles its shape, because Tailscale's SSH rules can't take an address
    as their source.
  - **The wait is bounded.** It's the plan's one allowed kind of wait, only
    for policy propagation. At the bound the run stops with exit 3, and no
    probe starts.
  - **Without it**, a probe could pass on the previous filter, and
    headscale's starts as allow-all.
  - **On headscale**, the policy is set after the nodes join. **On the
    Tailscale engine**, the wait comes after `tofu apply`.
- **`Teardown` runs on every exit path.** `tailsuite down` can also run it
  after a crash, from the lab state in `.tailsuite/`.
- **`CompiledFilter` is required of every engine.** Readiness, the
  conformance check in 0002, and the differential fuzzing in #42 all use
  it.

### Capabilities are data

Each capability an engine declares has one of three values:

- **native:** the engine does it as Tailscale does;
- **lowered:** the engine does it after a rewrite that's exact for every
  test it touches, recorded in the patch;
- **refused:** the engine can't, or can only approximate it. A test that
  needs it is REFUSED on that engine, and the result names the capability
  and the engine.

**A feature with no row is refused on every engine.** That fails safe, the
same way 0014's allow-list does.

Some conditions no engine can reproduce, so a test that depends on them is
UNKNOWN on both:

- **Per-OS behaviour outside the packet filter**
  ([0009](0009-os-mimicry.md)). Filter-level `node:os` rules are proven,
  and labelled "OS mimicked".
- **Completing an SSH check**, which needs a person.
- **Traffic from the public internet**, which a Funnel test would need.
- **Posture attributes the lab can't set:** a third-party integration's,
  or Tailscale's geolocation attributes (`ip:country`,
  `ip:publicAddress`), which come from the device's public IP.
- **The "doesn't meet it" side of a condition** when the lab can't build
  such a device (0015).

The matrix lives in code next to each engine. `tailsuite capabilities`
prints it, and the docs page is generated from the same data, so the two
can't drift.

**A capability is refused until the milestone that builds it ships.** The
table shows each one's target, and its milestone where that's after M1:

- **M1, the walking skeleton**, builds what the three-node lab needs:
  - grants and ACLs on TCP ports between tags, hosts, users and groups,
    with users lowered to tags on the Tailscale engine;
  - `*` sources and destinations, and `"ip": ["*"]`, as in a new tailnet's
    untouched default policy;
  - pinned IPv4 addresses;
  - the compiled filter.
- **M2** completes tags, grants and ports: every port and protocol form,
  and IP sets.
- **M3** adds SSH, `via`, subnet routes, exit nodes, `autogroup:internet`
  and session recording.
- **M4** completes users and groups (`autogroup:member`, `self` and
  `tagged`, and role autogroups), and adds posture and `attrConfig`.

Here's the table as of headscale 0.29.5 and Tailscale's docs (checked
2026-10-25):

| Capability | headscale | Tailscale engine |
|---|---|---|
| Grants, ACLs, ports, hosts, tags | native (M1's basics; in full from M2) | native (M1's basics; in full from M2) |
| Users and groups | native (matched by email) | lowered: one user becomes one tag (#6). Rules whose meaning depends on the source's login are refused until #21 (rows below) |
| `autogroup:member`, `autogroup:self` | native from M4 | refused; #21 (M3) decides |
| `autogroup:tagged` | native from M4 | refused while users are lowered to tags, since every device is then tagged (#21) |
| `autogroup:shared`, groups synced from an identity provider | refused | refused |
| Role autogroups (`autogroup:admin`…) | lowered from M4: as tag owners, to the lab's admin, which can't change who reaches what because tags come from `tofu/`; in rules, to the users with that role in `lab.yaml`, else refused | refused: no human users (Tailscale says users are coming) |
| Posture (`postures`, `srcPosture`, `defaultSrcPosture`) | refused; declined upstream ([headscale#2458](https://github.com/juanfont/headscale/issues/2458)) | native from M4, with `node:os` mimicked |
| IP sets | lowered from M2: expanded to addresses; refused when a set includes an autogroup or a host | native from M2 |
| `via`, `autogroup:internet`, subnet routes, exit nodes | native from M3 | native from M3 |
| SSH accept and deny | native from M3 | native from M3 |
| SSH logins tied to the source's login (`localpart:*@<domain>`) | refused; headscale's support isn't verified | refused: a tagged device has no login (#21) |
| SSH check | native from M3: the demand is proven, completing it is UNKNOWN | refused: "an SSH access rule from a tagged device cannot be in check mode" |
| SSH session recording | refused | native from M3, with a recorder node |
| Funnel (`nodeAttrs`) | refused | native as configuration, from the config checks of #32 (M2); traffic from the internet is UNKNOWN |
| `attrConfig` | refused | native from M4 |
| Services (`svc:`) | refused | refused until a milestone adds them |
| Pinned IPv4 addresses | native (allocation, #5) | native (`POST /device/{id}/ip`) |
| Pinned IPv6 addresses | refused | refused |
| Compiled filter dump | native | native |

### How a test becomes REFUSED or UNKNOWN

1. **The planner computes each test's needs, before the lab starts.**
   Needs always over-approximate:
   - the features of every rule that could match the test's source and
     destination, ignoring the rules' conditions. This comes from the typed
     copy in 0014;
   - a selector the planner can't resolve counts as matching every device;
   - the test's probe, such as SSH or a check;
   - its conditions, such as posture.
2. **A need the engine refuses makes the test REFUSED on that engine**,
   naming the capability. A need no engine or lab can reproduce makes it
   UNKNOWN, naming the condition.
3. **Needs also become automatic labels**, `needs:<capability>` (0015), so
   a profile can send those tests to an engine that can prove them.
4. **A key the typed copy doesn't understand** follows 0014.

### The engines' copies of the policy

- **The policy's own `tests` and `sshTests` are removed from every
  engine's copy**, because tailsuite runs them itself (0015).
  - headscale checks them against live devices, and has no field for
    `srcPostureAttrs`.
  - On the Tailscale engine, their users would need rewriting too.
- **Each engine's copy also loses what that engine refuses**, because each
  rejects the whole policy over a single invalid rule:
  - headscale over any feature it lacks;
  - Tailscale, once users are tags, over a check-mode SSH rule ("an SSH
    access rule from a tagged device cannot be in check mode") or an
    `autogroup:self` destination (valid "if the source contains only users
    or groups").

  Each removal is recorded in the patch:
  - every test whose source and destination a removed or altered rule
    could match is REFUSED;
  - a removal that isn't about a rule's source or destination (Funnel in
    `nodeAttrs`, `attrConfig`) refuses every test on the devices it
    targets;
  - if the user's policy had rules in `grants` or `acls`, and the removals
    leave none, the whole run is REFUSED. A section left absent would make
    both engines allow everything, and one left empty would make them deny
    everything. Neither is the user's policy. The readiness marker doesn't
    count as a rule;
  - `defaultSrcPosture` applies to every rule, so a policy that uses it is
    REFUSED on headscale entirely;
  - the rest of the suite still runs.
- **Conditions are removed, not the rules they guard.** Every test such a
  rule touches is already REFUSED, so this only matters if the needs
  computation has a bug. Then a rule without its condition allows more,
  never less, and the bug shows extra access instead of a false block.

## Why

- **A pure `Lower`** makes the riskiest code, rewriting a security policy,
  the easiest to test, and shows the user every rewrite before anything
  runs.
- **Capabilities as data** let the planner refuse before starting a lab,
  let profiles route by `needs:` labels, and keep the published matrix
  true.
- **Refusing whatever an inexact rewrite touches** is 0001 applied to
  rewrites: a result the lab couldn't prove is never a PASS or a deny.
- **More access as the bound on bugs.** It's chosen because a false
  "locked down" is the failure 0001 calls worst.
- **Waiting for the compiled filters** stops a probe from passing on the
  filter the lab started with.
- **Removing refused features** keeps pull-request runs on headscale
  useful for policies that use posture, instead of refusing everything.

## Evidence

- **headscale 0.29.5:**
  - research 0005 (2026-10-24) found it rejects role autogroups as tag
    owners, `postures`, `ipsets`, SSH `recorder`, Funnel in `nodeAttrs`,
    and `attrConfig`;
  - it accepted `via`, `autogroup:self`, `autogroup:internet`, SSH check
    mode, `sshTests` and `autoApprovers`;
  - its policy tests have no `srcPostureAttrs` field, and it rejects
    unknown fields (`hscontrol/policy/v2/test.go`, checked 2026-10-25);
  - with no policy loaded it "allows all traffic between nodes", and a
    policy file with no `acls` or `grants` defaults to allow-all
    (`docs/ref/policy.md`, checked 2026-10-25);
  - it allows all only when both sections are absent: `pol.ACLs == nil &&
    pol.Grants == nil` (`hscontrol/policy/v2/filter.go`).
- **headscale's maintainer declined posture** in 2025 ("Headscale is not
  really a ZTNA solution"; headscale#2458, closed as not planned).
- **Tailscale's docs, checked 2026-10-25:**
  - "An SSH access rule from a tagged device cannot be in check mode";
  - "in the absence of an acls section", Tailscale "applies the default
    allow all policy", and "to deny all traffic, use an empty object for
    the acls section";
  - API-only tailnets "have no human owner or users", and "future
    iterations of this feature will remove this limitation";
  - the geolocation posture attributes `ip:country` and
    `ip:publicAddress`.
- **The engines' other gaps** are in
  [research 0002](../research/0002-engines.md).

## Alternatives

- **Each engine checks capabilities while running**, failing midway. Tests
  would start, then die, and nothing could route them in advance.
- **REFUSED for the whole run whenever the policy uses anything an engine
  lacks.** It's simpler, but every posture user would lose headscale for
  pull requests.
- **Trusting an approximate rewrite, erring toward more access**, which
  an earlier draft of this record proposed. A wrong allow is a PASS when
  the test expects allow, so it isn't visible. Refusing is the only safe
  answer.
- **Removing the rule instead of its condition.** A bug in the needs
  computation would then show a false block.
- **A hand-written matrix in the docs.** It would drift from the code.

## Given up

- **headscale tests a narrower policy** when the real one uses features
  it lacks. Tests touching those features are REFUSED, so nothing false
  shows, but pull-request runs prove less for those policies.
- **`check` and `autogroup:tagged` are refused on the Tailscale engine**
  until its throwaway tailnets hold human users.
- **The policy's own tests never run inside an engine's evaluator.**
  tailsuite runs them with real traffic instead, and `tailsuite deploy`
  still runs them through `/acl/validate` (0007).
- **The needs computation relies on the typed copy's static view.** A bug
  there could send a test to the wrong engine. Over-approximation, erring
  toward more access, the nightly Tailscale run and the conformance check
  (#42) bound the damage.
- **Each new Tailscale feature is refused until it has a capability
  entry.** That's 0014's forcing function again.
