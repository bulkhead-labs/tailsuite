# 0021: An empty `grants` section denies everything, and the marker holds on Tailscale

Status: **accepted** (2026-10-25). Settles the two checks
[0016](0016-engine-interface-capabilities.md) gave the #3 spike, and amends
0016 accordingly. Evidence:
[research 0006](../research/0006-throwaway-tailnets-spike.md), runs 5 and 7.

The spike also found that one line of 0016 can't hold on the Tailscale
engine as things stand: "`tailsuite down` can also run it after a crash,
from the lab state in `.tailsuite/`". A lost throwaway tailnet can't be
deleted with the test organization's `tailnets`-only client. That's a
question for the maintainer on #6, and its answer will be #6's decision.

## Context

0016's readiness wait adds a marker rule to every policy it applies, and
makes an absent default explicit first. It left two things to check on
Tailscale:

- **`{"grants": []}` with no `acls` section.**
  - Tailscale's docs, read literally ("in the absence of an acls
    section"), might allow everything.
  - headscale denies everything for it: 0016's reading of headscale's
    source.
  - So 0016 made that shape REFUSED on both engines "for now".
- **Whether the marker survives beside an allow-all rule.** An allow-all
  covers the marker, and a control plane that drops covered rules would
  hide it from the wait. #5 checks headscale; #3 checks Tailscale.

**No engine ever applies the shape bare.** `Start` adds the marker to every
policy it applies. So how an engine reads the bare shape can't be seen by
running labs: not in the compiled filters, not in the conformance check
(0002), not in nightly runs. If an engine read it as allow-all, the lab
would still deny everything. That would show every path blocked, while
production allows everything: the false "locked down" that 0016 calls
the worst failure. That's what 0016's REFUSED guarded against.

## Decision

- **An empty `grants` section with no `acls` section denies everything on
  both engines.** So it's handled like an empty `acls` section:
  - it gets no default;
  - it's no longer REFUSED, **as long as the engine's reading is checked**
    (next two points).

  This amends 0016's "An empty `grants` section with no `acls` section is
  REFUSED on both engines for now".
- **On the Tailscale engine, every run whose policy has that shape checks
  Tailscale's reading first** (#6). It fails closed:
  - The engine sends `/acl/validate` a copy of the policy:
    - without the policy's own `tests` and `sshTests`, which 0016 keeps
      out of every engine's evaluator;
    - with a tag of its own in `tagOwners`;
    - with one test: a deny from that tag to itself.
  - **The run goes ahead only if validate answers that the copy is
    valid.** Any other answer makes it REFUSED for that policy, naming
    the response:
    - the deny test failing, which is what an allow-all reading would do;
    - a rejection for any other reason;
    - an error, or no answer.

    So the engine never has to read the error's text.
  - It costs one call of about 0.3 s, and needs no devices.
- **Nightly, a bare-shape check reads the compiled filter** on a
  throwaway tailnet, as run 5 did (#13's nightly tier). The per-run check
  reads Tailscale's test evaluator. This one reads what Tailscale
  compiles for nodes, so a change in the compiler alone shows within a
  day.
- **On headscale, an integration test checks headscale's reading** (#5),
  with a positive control:
  - it starts from a known state, where an allowed port answers and the
    filter holds that rule;
  - it applies the bare shape, and waits, bounded, for the filter to
    change;
  - it expects the filter empty, and the port closed.

  headscale's validation needs live devices for a policy's tests
  (research 0005), so the Tailscale engine's check doesn't carry over. The
  integration tier runs on every ready PR, including any that moves the
  pinned headscale.
- **Only a policy with neither section** gets the explicit allow-all
  default before the marker, as 0016 says.
- **On the Tailscale engine, the readiness wait relies on the marker**,
  beside an allow-all grant or without one. On headscale it still waits
  on #5's check.

## Why

- **Tailscale denies everything for the shape**, measured in traffic and
  in the compiled filter (run 5). headscale does too, by 0016's reading
  of its source, which #5's test is to confirm. Refusing the shape would
  only block policies that both engines treat the same.
- **The checks make a change visible** where running labs can't.
  - On Tailscale, a changed reading makes the policy REFUSED at the next
    run, or shows in the nightly check.
  - On headscale, it fails the integration tier, and so the PR that
    moved the pin.
- **The marker stays visible beside an allow-all grant** on Tailscale: as a
  rule of its own, on every node, within 0.12 s of the policy call
  returning. If it ever disappeared, the wait would stop the run at its
  bound, with exit 3, before any probe.

## Evidence

**The shapes, in traffic and in the filter (run 5, 2026-10-25).** Each
shape was set from a known state, 8080 allowed and 8081 denied, and read
in traffic and in the receiving node's compiled filter.

| Policy | Traffic | Compiled filter |
|---|---|---|
| No `acls` and no `grants` | 8081 opened: allows everything | One rule: every tailnet address, every port |
| `"grants": []`, no `acls` | 8080 closed: denies everything | Empty |
| `"acls": []`, no `grants` | 8080 closed: denies everything | Empty |

**The validate check tells the readings apart (run 7, 2026-10-25).** The
same deny test, from `tag:tailsuite-check` to itself on port 1, was
validated with no devices:

| Policy, with the deny test | `/acl/validate` |
|---|---|
| `"grants": []`, no `acls` | Valid |
| `"acls": []` | Valid |
| No `acls` and no `grants` | "test(s) failed": `want: Drop, got: Accept` |
| An allow-all in `acls` | "test(s) failed": `want: Drop, got: Accept` |

**The marker (run 5).** Two markers went in:

- one from an address outside the tailnet's range, `192.0.2.77/32`;
- one from an address inside it, `100.99.99.99/32`;

each to every node, on a port of its own. Both appeared in both nodes'
compiled filters, as rules of their own:

- beside one explicit grant;
- beside an allow-all grant.

## Alternatives

- **Keep the shape REFUSED.** Safe, but it refuses policies both engines
  read the same way.
- **Validate the policy as written.** It would run the policy's own tests
  in Tailscale's evaluator, which 0016 rules out, and a failure of theirs
  couldn't be told from a failure of the check's without reading error
  text.
- **Remove the REFUSED with no check.** That was this record's first
  draft. The review showed that a change would go unseen, and would show
  as false denies.
- **A nightly check only**, on a throwaway tailnet. It would see a change
  up to a day late, and runs in between would trust the old reading. It
  runs beside the per-run check instead.
- **Give the shape the allow-all default**, as for a policy with neither
  section. That contradicts both engines.

## Given up

- **This is Tailscale's behaviour on 2026-10-25, through an alpha
  surface.** It isn't documented, so it can change without notice. The
  checks above catch a change; they don't prevent one.
- **An extra validate call per run** on the Tailscale engine, for policies
  with that shape only.
- **The per-run check reads Tailscale's test evaluator, not the filter it
  compiles for nodes.** Runs 5 and 7 show the two agree today; nothing
  shows they'd change together. If the compiler alone changed, runs would
  show false denies until the nightly bare-shape check caught it, within
  a day.
- **A marker beside an allow-all in `acls`**, rather than in `grants`,
  wasn't tried. A missing marker fails safe, with exit 3.
- **Where the marker's source address comes from is still open.** Both
  an address outside the tailnet's range and one inside survived on
  Tailscale. #5 tries both on headscale, and then one is chosen.
