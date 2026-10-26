# 0025: The headscale engine: its lab, its copy of the policy, and readiness

Status: **accepted** (2026-10-26). Built in #5. Builds on
[0016](0016-engine-interface-capabilities.md) and
[0021](0021-empty-grants-and-the-marker-on-tailscale.md), and amends
0016's matrix twice:

- **Role autogroups are lowered on headscale from M1**, not M4.
- **Pinned IPv4 addresses are refused on headscale** until #67, not
  native.

It also settles 0021's open point: the readiness marker's source is
`192.0.2.77/32`, outside the tailnet's range.

## Context

- **0016** set the engine interface: a pure `Lower`, and a `Start` that
  returns only when every node shows the applied policy, waiting on a
  marker rule.
- **#5** asked for headscale 0.29.x with TLS from a per-run lab CA and
  DERP over TLS ([0003](0003-docker-one-container-per-node.md)); users,
  keys and policy applied in database mode without a restart; pinned node
  addresses, or the gap documented; the compiled filter; the
  capabilities; the node agent as every node's entrypoint; and the two
  checks 0021 left to headscale.
- **headscale rejects a whole policy** over any key or feature it lacks,
  one error at a time ([research 0005](../research/0005-tailnet-model-trial.md)).
- **What headscale 0.29.5 offers** (its source at the `v0.29.5` tag,
  checked 2026-10-26):
  - a policy schema that's a subset of Tailscale's: groups, hosts,
    `tagOwners`, `acls`, `grants` (with `app` and `via`), `nodeAttrs`,
    `autoApprovers` (routes and exit nodes), `ssh`, `tests`, `sshTests`
    and `randomizeClientPort`, read with unknown fields rejected;
  - autogroups `internet`, `member`, `nonroot`, `tagged`, `self` and
    `danger-all`, and no role autogroups;
  - a login in a policy matches a user's email or name;
  - pre-auth keys with tags and no user, which make a tagged node;
  - no API or command that sets a node's address. Its allocator hands
    addresses out in order (or at random) from `prefixes`, and it keeps
    nodes in memory, so editing its database needs a restart.

## Decision

### The interface, in code

`internal/engine` holds 0016's interface, with what 0016 left to the
code:

- **`LabPlan`** lists the devices the lab rebuilds: name, user or tags,
  pinned address. `PlanEveryDevice` takes the whole model; the planner
  (#8, then #22) will choose.
- **`Lowering`** is the engine's copy and the record of how it differs:
  - `Patch`: RFC 6902 operations in the file's own spelling, each with a
    `note` saying why. RFC 6902 has receivers ignore members it doesn't
    define, so the patch stays standard;
  - `Rewrites`: exact changes. The tests they touch still run;
  - `Changes`: places the copy leaves out (`removed`), or keeps but
    can't reproduce (`inexact`). Each names its capability and the
    identities it touches: its rule's `src` and `dst`, or `targets`
    whatever the rule. #8 refuses every test a change could touch;
  - `Refusals`: the whole run is REFUSED on this engine, and `Start`
    refuses too.
- **Capabilities are data**: each row has what the engine does today,
  and where it's going (`target`, `milestone`). A capability with no row
  is refused.
- **Readiness helpers** every engine uses: `WithMarker`, `NewMarkerPort`
  and `WaitReady`, below.

### The lab

- **headscale 0.29.5, pinned by digest**, one day short of the two-week
  rule, because two of its fixes apply to a lab:
  - "a rejected policy leaving its packet filter active";
  - "clients uploading logs to Tailscale Inc. while `logtail.enabled` is
    `false`" (CLAUDE.md rule 3).

  It turns two weeks old on 2026-10-27. 0.29.6 is #68.
- **One internal network.** The control plane is the node
  `tailsuite-headscale`, which no device may be named.
- **TLS from the run's own CA** (0003):
  - an ECDSA P-256 CA is made in memory, name-constrained to the control
    plane's name;
  - it signs headscale's certificate, and its key is then dropped, so
    nothing can issue another;
  - nodes trust it through `SSL_CERT_FILE`, which replaces Go's system
    roots, so they trust it and nothing else.
- **DERP is headscale's embedded server only**, over the same TLS. No
  DERP map comes from Tailscale (`derp.urls: []`).
- **Nothing leaves the lab:** no update checks, `logtail` off, no DNS
  pushed to nodes, and tailscaled runs with `--no-logs-no-support`.
- **Users** are created with the login as their email, which is what a
  policy's login matches. Their headscale names are tailsuite's
  (`tailsuite-user-1`…), so no policy names one.
- **Keys** are single-use and expire in 10 minutes: a user's device gets
  one for its user, a tagged device one carrying its tags and no user.
  A key reaches its node only on stdin (`--auth-key=file:/dev/stdin`).
- **Every tailnet node** runs real tailscaled under the node agent
  (`control.Entrypoint`, 0020), in kernel mode with `--state=mem:`.
  Nodes join in parallel. Each then passes `CheckKernelTUN` (0019), and
  its agent must answer as that node.
- **The control plane runs without the agent.** It's a service, not a
  tailnet node, and its image has no shell.
- **The policy is applied in database mode** through headscale's CLI,
  run through the sandbox's exec channel, with the policy on stdin. No
  change restarts headscale, and nothing listens for an API.
- **A policy headscale rejects is REFUSED**, naming headscale's message,
  but only when headscale is otherwise healthy. A CLI that can't reach the
  server fails the same way, and that's an error (exit 3).
- **If `Start` fails**, it tears down what it made.

### Headscale's copy of the policy

`Lower` is pure, and works on the user's file through the typed copy's
references, in the file's own spelling (0014, 0022):

| In the user's policy | In headscale's copy | Recorded as |
|---|---|---|
| A key the typed copy doesn't understand, or a duplicate key | The run is REFUSED, naming the key: headscale rejects unknown fields | Refusal |
| `defaultSrcPosture` | The run is REFUSED: it applies to every rule (0016) | Refusal |
| `disableIPv4: true` | The run is REFUSED: the lab's probes are IPv4 | Refusal |
| `tests`, `sshTests` | Removed: tailsuite runs them itself (0015) | Change, touching no test |
| `ipsets`, and every rule that names an IP set | Removed | Change: the rule's tests REFUSED |
| A rule naming `autogroup:shared` | Removed | Change |
| `postures`; a rule's `srcPosture` | Removed: the condition, not the rule (0016) | Change: the rule's tests REFUSED |
| SSH `recorder`, `enforceRecorder` | Removed | Change |
| `nodeAttrs` | Removed, each entry recorded | Change: tests from or to its targets REFUSED |
| `attrConfig`, `externalTailnets` | Removed | Change: tests on the devices they name REFUSED |
| `autoApprovers.services` | Removed | Change |
| A grant's `app` | Kept | Change, inexact |
| An address inside the tailnet's range, short of the whole range | Kept | Change, inexact: headscale can't give a device production's address |
| `derpMap`, `oneCGNATRoute`, `disableIPv4: false` | Removed: they don't change who reaches what | Rewrite |
| An ACL's legacy `users` and `ports` | Moved to `src` and `dst` | Rewrite |
| Role autogroups as tag owners | The lab's admin, `lab-admin@tailsuite.invalid` | Rewrite |
| Role autogroups in rules and route approvers | The users lab.yaml gives that role (below) | Rewrite, or Change |

- **If removals leave no rule in `acls` and `grants`** that the user had,
  and some were removed because headscale can't do them, the run is
  REFUSED: the lab would deny everything (0016). Rules removed because
  they match no lab device don't count: then the lab denies what
  production denies too.
- **Comments survive**, except inside what's removed. Lists that change
  are replaced whole, so a comment inside one is lost.

### Role autogroups, from M1 (amends 0016)

0016 put role autogroups on headscale at M4. They come now, because a
common policy can't run on headscale without them: headscale rejects
`"tag:prod": ["autogroup:admin"]`, and with it the whole policy.

- **As tag owners**, every role autogroup becomes the lab's admin. Tags
  come from `tofu/` and the engine, never from a device asking, so a
  tag's owners can't change who reaches what. That's exact.
- **In rules and route approvers**, `autogroup:<role>` becomes the users
  lab.yaml gives that role (0022). That's exact only when the lab knows
  every role, so it happens only when:
  - **every user has their roles listed.** A user with none could be an
    admin in production;
  - **for `autogroup:admin`, no user is an owner without also being an
    admin.** Tailscale's docs describe `autogroup:admin` as users "who
    [have] the role of Admin", and don't say whether that includes the
    owner (policy syntax reference, checked 2026-10-26).

  Otherwise the rule is removed, and its tests are REFUSED.
- **A role no lab user holds** matches no lab device. A rule whose
  source or destination is left empty by that matches nothing in the
  lab, and is removed, exactly.

### Pinned addresses: refused on headscale (amends 0016)

0016 called pinned IPv4 "native (allocation, #5)" on headscale. headscale
0.29.5 can't do it: no API sets an address, and an edit to its database
needs a restart. So:

- **pinned IPv4 is refused on headscale**, and a device's `ip:` in
  lab.yaml isn't honoured there;
- **an address in the tailnet's range in a rule** is recorded as inexact,
  and every test it could touch is REFUSED on headscale. The whole range,
  and addresses outside it, aren't devices' addresses, and aren't marked;
- **tests name devices**, and the runner probes each device's lab
  address, so a device's address matters only where a rule names it;
- #67 (M2) restores it, likely by one restart during `Start`.

### Readiness

- **The marker's source is `192.0.2.77/32`** (TEST-NET-1, RFC 5737). On
  headscale, a source outside the tailnet's range and one inside
  (`100.99.99.99/32`) both survived beside an allow-all rule, as rules of
  their own, as they did on Tailscale (0021). The outside one is chosen:
  no node holds or can be given it, on either engine, and no rule of the
  user's in the tailnet's range can cover it.
- **The marker's port is random** in 41000–60999, and never one a node's
  current filter already has a marker on. So a wait can't be satisfied by
  an earlier apply's filter, even one another tailsuite process made.
- **The default is made explicit only for a policy with neither section**
  (0016, 0021). `"grants": []` and `"acls": []` get the marker alone.
- **`WaitReady` waits, bounded, until every node:**
  - has the marker in its compiled filter;
  - has every peer its filters call for: a node whose filter admits
    traffic from another, or whose traffic another's filter admits.

  Peers are judged from the filters only once every filter shows this
  apply. Extra peers, such as through SSH rules, are fine. A filter
  source it can't read admits everything, so it expects more peers, never
  fewer: a wrongly expected peer stops the run at the bound (exit 3); a
  wrongly missing one could let a probe start before the path exists.
- **headscale's bound is 30 s**, polled every 250 ms. headscale took up to
  2.5 s for a policy to reach traffic ([research 0004](../research/0004-benchmark-2026-10-23.md)).

### 0021's check on headscale

An integration test reads headscale's reading of `{"grants": []}` with
no `acls`, as 0021 says: from a known state, where an allowed port
answers and the filter holds its rule, it sets the bare shape with no
marker, waits for the filter to change, and expects it empty and the
port closed.

### What a deny looks like

Measured here, and for the runner (#8): **a deny has two forms.**

- **Between peers** (the policy allows something between the two nodes),
  a port it doesn't allow is dropped: the probe times out.
- **Between nodes with no path either way**, they aren't peers.
  Tailscale and headscale send a node only the peers it has a path with,
  so the sender has no route at all: the probe is an `error`, "network is
  unreachable".

Both are the policy denying. The runner turns either into a deny only
with its positive control (0001).

## Why

- **The CLI through exec** needs no API key, no listener and no network
  path to the control plane, like the agent's control (0019).
- **A name-constrained CA whose key is gone** can't be used to vouch for
  anything else, even by someone holding a lab's files.
- **`SSL_CERT_FILE`** makes the lab CA the nodes' only root, so a node
  can't reach anything over TLS that the lab didn't issue.
- **Refusing on any unknown key** follows from headscale's own
  behaviour: it would reject the policy anyway, and a refusal from
  `Lower` names the key before anything starts.
- **Lowering role autogroups only when exact**, and refusing otherwise,
  is 0016's rule for rewrites.
- **Judging peers from the filters** works on both engines without a
  model of peer visibility of tailsuite's own.

## Evidence (2026-10-26)

- **A spike** (docker CLI, headscale 0.29.5 and tailscale v1.102.5, a
  throwaway lab, torn down): TLS from a lab CA, the embedded DERP, a
  user's node and a tagged node joining, the policy set from stdin, the
  markers beside an allow-all rule, and the bare `{"grants": []}`
  emptying the filter and closing the port.
- **Integration tests** (`go test -tags integration
  ./internal/engine/headscale/`), against Docker 26.1.5, all seven pass:
  - the startup lab (alice's laptop, a web server, a database): every
    node has its addresses and the marker; the allowed paths answer, each
    naming its peer; a port the policy doesn't allow between peers times
    out; a node with no path gets no route; the tag owners'
    `autogroup:admin` was lowered;
  - both markers survive beside an allow-all rule, as rules of their own,
    on every node;
  - `{"grants": []}`: the filter empties and the port closes, from a
    known state where it answered;
  - a node without the lab CA can't join, and tailscaled says
    `x509: certificate signed by unknown authority`;
  - a policy headscale rejects is REFUSED, and the last good policy
    stays in force;
  - research 0005's third policy, lowered, is accepted by headscale, and
    its surviving grant carries traffic;
  - a refused lowering isn't started.
- **The tests fail when the code is broken.** Nine faults were planted,
  one at a time, and each made a test fail:
  - no marker added;
  - IP sets kept in the copy;
  - role autogroups kept as tag owners;
  - nodes not given the lab CA;
  - peers not checked by the wait;
  - an unreadable filter source admitting nothing;
  - the default not made explicit for a policy with neither section;
  - a posture condition removing its whole rule;
  - the owner-versus-admin ambiguity ignored.
- **Fuzzing** `Lower` (parse, lower, re-apply the patch, re-parse, and
  check no role autogroup, IP set, test or unknown key is left) ran
  10.2 million inputs. The first runs found two inputs, a group named
  `ipset:` and a role autogroup as a group member; both are invalid in
  Tailscale too, headscale refuses them at `Start`, and they stay as
  regression cases.

## Alternatives

- **headscale's gRPC or REST API.** It needs an API key, and a listener
  reachable from the host, which an internal network doesn't give.
- **The official image's `containerboot`.** It takes the auth key in the
  environment, where `docker inspect` shows it.
- **Adding the lab CA to the system store.** Nodes would trust every
  public root too.
- **An address in the certificate rather than a name.** The address is
  known only after the node starts, and the files go in before.
- **Pinning addresses by a restart and a database edit now.** It needs a
  sandbox stop, copy and start, and a way to edit SQLite in a distroless
  image: #67.
- **A marker source inside the tailnet's range.** It survived too, but a
  user's rule on the range could cover it.
- **A counter for the marker's port.** A second process would start from
  the same number.
- **Role autogroups at M4, as 0016 had it.** Policies with role
  autogroups as tag owners couldn't run on headscale before then.
- **Lowering `autogroup:admin` to admins and owners.** Tailscale's docs
  don't say the owner is included.

## Given up

- **Pinned IPv4 on headscale**, until #67. Rules naming a device's
  address are REFUSED there.
- **Role autogroups in rules need every user's roles in lab.yaml.**
- **A policy with any key the typed copy doesn't understand is REFUSED
  on headscale**, even one headscale might accept.
- **A comment inside a list the lowering replaces is lost** in the
  engine's copy. The user's file is never changed.
- **The CA's certificate is valid for a year**, so a lab `up` leaves
  running keeps working. Its key is gone, so it can't sign anything else.
- **The control plane has no agent**, so nothing probes from it.
- **headscale 0.29.5 was pinned a day early**, for two fixes that apply.
