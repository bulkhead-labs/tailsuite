# 0026: The test runner: reading traffic into results

Status: **accepted** (2026-10-26). Built in #8. Implements
[0015](0015-test-file-labels-profiles.md) (the test file, labels,
profiles, results and exit codes) and the needs computation of
[0016](0016-engine-interface-capabilities.md), on the node agent
([0020](0020-node-agent.md)) and the headscale engine
([0025](0025-headscale-engine.md)).

## Context

0015 settled what users write and what a run reports, and 0016 how a test
becomes REFUSED or UNKNOWN before anything runs. They left to the code
how an identity becomes devices, how a probe's outcome becomes a result,
and what a positive control is in practice. 0001 asks that every deny
have "proof, in the same run, that the target is alive and reachable from
an allowed source or from itself".

## Decision

### Where it lives

- **`internal/suite`**: reading `tests/*.yaml`, mapping the policy's own
  tests, resolving identities, computing needs, labels, profiles,
  selection and warnings. All of it is pure.
- **`internal/runner`**: running a planned suite on a lab, and the
  human, JSON and JUnit outputs. The command line is #12's.

### Reading the suite

- **Test files are strict**, as `lab.yaml` is (0022): an unknown key, a
  second document, a missing name, a port out of range, an `expect` that
  doesn't fit, a probe the agent doesn't have, a `when` other than `os`,
  or a label with `:` (tailsuite's own) is an error naming the file and
  line.
- **The policy's own tests** map as 0015 says. A `tests` entry whose port
  isn't one number can't be probed, and is UNKNOWN.

### Identities become devices

| A test names | It means |
|---|---|
| A device in lab.yaml | That device. If a host has the same name, its address must be the device's pinned one |
| A login | That user's devices that aren't tagged |
| `group:…` | Its members' untagged devices |
| `tag:…` | The devices with that tag |
| `autogroup:member` / `tagged` | Untagged / tagged devices |
| `autogroup:self` (as `to`) | Each source's own user's devices |
| A role autogroup | The devices of users lab.yaml gives that role |
| `*` | Every device |
| A host, an address or a prefix | The devices whose pinned address is in it |

- **A device is never paired with itself.** Its traffic to its own
  address never meets a filter.
- **A test from `tests/` that names no lab device, or aims at a port
  where nothing listens on a device it names**, fails to load (exit 3);
  the error names the `listen:` entry to add. **A policy test** is
  UNKNOWN instead.

### Needs, before anything runs (0016)

For each pair, every rule in `acls` and `grants` (and `ssh`, for an SSH
test) that could match its source and its destination counts:

- **over-approximated:** ports and conditions are ignored; a selector that
  can't be resolved, an IP set, an unknown group or a role a user's
  listed roles don't rule out matches; any address in the tailnet's range
  could be any device's, since the engine assigns them;
- **each matching rule's features become needs:** users and groups, each
  autogroup's row, IP sets, `via`, routes to addresses outside the
  tailnet's range, posture conditions, app grants;
- **the test's own:** SSH, a check, a login tied to the source's, a
  `when` (posture, until M4).

A need the engine refuses makes the pair REFUSED, naming the capability.
**Every change in the engine's lowering that could touch the pair**
refuses it too, whatever the capability's row says (0025's `Changes`).
The needs become `needs:` labels.

### A run

- **Listeners first.** One session per node declares its `listen:`
  entries, keeps the generation, and lasts the run. Every listener must
  open, or the run stops (exit 3). After the probes, a generation that
  moved, or a lost session, makes every pair to that node UNKNOWN (0020).
- **Probes** use the pair's protocol (`tcp`, or `http` with `/`), to the
  destination's tailnet IPv4, 3 s each, up to 8 in flight.
- **What a probe saw:**

  | Agent outcome | Read as |
  |---|---|
  | `success`, answered as the destination | reached |
  | `success`, answered as another node, or anonymously | UNKNOWN: not the target |
  | `timeout`, not connected | blocked: a drop between peers |
  | `error`, "network is unreachable" | blocked: no path, so not peers (0025) |
  | `timeout`, connected | UNKNOWN |
  | `refused` | UNKNOWN: nothing listened, though lab.yaml says something does |
  | any other `error` | UNKNOWN |

- **A block the target's filter contradicts isn't a deny.** If the
  target's compiled filter certainly admits the source on that port, to
  the target's address, a blocked half is UNKNOWN: a lost packet or a peer
  not yet set up, not the policy.
- **A blocked half needs its positive control** (0001), once per target
  and port:
  1. from a lab node the target's compiled filter certainly admits on
     that port (no benefit of the doubt here), other than the test's
     source;
  2. else from the target itself, to its own tailnet address, which
     never meets its filter.

  The control must reach the target, answered as the target. If it
  can't, the half is UNKNOWN: a dead service can't be ruled out.
- **A pair's result** (0015): an allow passes when a half was reached,
  fails when every half was blocked; a deny passes when every half was
  blocked, with its controls, and fails when a half was reached; anything
  between is UNKNOWN. In M1 a UDP half is always UNKNOWN (#56), so a
  policy `deny` with no `proto` is UNKNOWN, and its `accept` passes on
  TCP.
- **A test's result** combines its pairs: any FAIL, then any REFUSED,
  then any UNKNOWN, else PASS.

### Profiles, selection and warnings

- **Profiles resolve as 0015 says**: `--profile`, else `default`, else
  headscale with every test and `unproven: report`. `unproven: fail`
  means strict. A profile that doesn't exist is an error.
- **A flag replaces the profile's setting** for one run (`--engine`,
  `--only`, `--skip`), and `--run` and `--strict` add to it. `--skip` wins
  over `--only`.
- **Warnings:** a test no profile runs; and, when every profile uses the
  engine the needs were computed for, a test it refuses on every pair.

### Outputs

- **JSON** is the `Run`: every test, its pairs and halves, each probe's
  outcome and control, the summary and the exit code. The UI will read
  the same (0017).
- **Text** lists the results, then the unproven ones apart, under "Not
  proven in this lab".
- **JUnit**: FAIL is a `<failure>`; UNKNOWN and REFUSED are skipped, or an
  `<error>` under strict; a test the selection left out is skipped.
- **Exit codes** (0015): 1 for a FAIL; 2 when nothing was proven, or
  anything is unproven under strict; 0 otherwise. 3 is a suite that can't
  load, or a lab the runner can't drive.

## Why

- **No route is a deny.** A control plane sends a node only the peers it
  has a path with, so "unreachable" between lab nodes is the policy at
  work, as a drop is. Reading it as an error would leave every deny
  between unrelated devices UNKNOWN.
- **`refused` is never a deny, nor a FAIL.** A Tailscale filter drops;
  something answered, so the policy didn't decide it.
- **A control from an admitted source** also shows the path works; the
  target itself is the fallback 0001 allows, and shows the service is up.
- **Over-approximating needs** refuses more than it must, never less, so
  a lowering the planner can't see past can't produce a false result.

## Evidence (2026-10-26)

- **Integration tests** (`go test -tags integration ./internal/runner/`)
  on headscale 0.29.5, with a synthetic startup project, all pass:
  - an allow over HTTP, a deny with no route (controlled from the
    server, which the database's filter admits), a deny between peers by
    drop (controlled from the target itself), an allow that's wrongly
    expected (FAIL, exit 1), a pair REFUSED because headscale's copy
    leaves out a rule that could touch it, and the policy's own tests
    (a PASS, an UNKNOWN for the missing UDP half, an UNKNOWN for a user
    with no device);
  - a dead service (every connection to its port reset, its listener
    still declared): the deny is UNKNOWN, as is an allow to it;
  - listeners changed during a run: UNKNOWN for that node only.
- **Six faults planted** one at a time were each caught: `refused` read
  as blocked; no positive control; REFUSED combined before FAIL; no route
  not read as a deny; the lowering's changes ignored; generations not
  checked.

## Alternatives

- **The positive control always from the target itself.** Simpler, but
  it proves only the service, not a path to it.
- **`refused` as a FAIL for an allow.** The policy didn't block it; a
  service did.
- **Ports in the needs computation.** Fewer REFUSED results, but 0016
  matches on source and destination, and a rule's ports aren't where its
  features live.
- **Sequential probes.** Simpler output order, slower suites; results
  never depend on order.

## Given up

- **More REFUSED results than strictly needed**, from over-approximation:
  an IP set is taken to hold any device.
- **A control from the target itself** proves the service is up, not that
  any path to it works.
- **UDP halves are UNKNOWN** until #56.
- **Warnings about engines** cover only the engine the run used, until a
  second engine exists (#6). For the same reason, unproven results don't
  yet name a profile and engine that could prove them, as 0015 asks; #6
  adds that.
- **An SSH test's port defaults to 22.**
