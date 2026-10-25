# 0021: An empty `grants` section denies everything, and the marker holds on Tailscale

Status: **accepted** (2026-10-25). Settles the two checks
[0016](0016-engine-interface-capabilities.md) gave the #3 spike, and amends
0016 accordingly. Evidence:
[research 0006](../research/0006-throwaway-tailnets-spike.md), run 5.

## Context

0016's readiness wait adds a marker rule to every policy it applies, and
makes an absent default explicit first. It left two things to check on
Tailscale:

- **`{"grants": []}` with no `acls` section.**
  - headscale denies everything for it.
  - Tailscale's docs, read literally ("in the absence of an acls
    section"), might allow everything.
  - So 0016 made that shape REFUSED on both engines "for now".
- **Whether the marker survives beside an allow-all rule.** An allow-all
  covers the marker, and a control plane that drops covered rules would
  hide it from the wait. #5 checks headscale; #3 checks Tailscale.

## Decision

- **An empty `grants` section with no `acls` section denies everything on
  both engines.** So it's handled like an empty `acls` section:
  - it gets no default;
  - it's no longer REFUSED.

  This amends 0016's "An empty `grants` section with no `acls` section is
  REFUSED on both engines for now".
- **Only a policy with neither section** gets the explicit allow-all
  default before the marker, as 0016 says.
- **On the Tailscale engine, the readiness wait relies on the marker**,
  with or without an allow-all rule. On headscale it still waits on #5's
  check.

## Why

- **Both engines agree**, so the shape is no longer ambiguous, and
  refusing it would only block policies that both engines treat the same.
- **The marker stays visible beside an allow-all rule** on Tailscale: as a
  rule of its own, on every node, within 0.12 s of the policy call
  returning.
- **It fails safe if Tailscale ever changes either behaviour.**
  - The wait reads each node's compiled filter.
  - A marker that disappeared would stop the run at the wait's bound,
    with exit 3, before any probe.
  - A changed reading of `{"grants": []}` would show in the conformance
    check (0002) and in the nightly runs.

## Evidence (run 5, 2026-10-25)

Each shape was set from a known state, 8080 allowed and 8081 denied, and
read in traffic and in the receiving node's compiled filter.

| Policy | Traffic | Compiled filter |
|---|---|---|
| No `acls` and no `grants` | 8081 opened: allows everything | One rule: every tailnet address, every port |
| `"grants": []`, no `acls` | 8080 closed: denies everything | Empty |
| `"acls": []`, no `grants` | 8080 closed: denies everything | Empty |

**The marker.** Two markers went in:

- one from an address outside the tailnet's range, `192.0.2.77/32`;
- one from an address inside it, `100.99.99.99/32`;

each to every node, on a port of its own. Both appeared in both nodes'
compiled filters, as rules of their own:

- beside one explicit grant;
- beside an allow-all grant.

## Alternatives

- **Keep the shape REFUSED** until headscale's reading is measured too.
  0016 already records that headscale denies everything for it.
- **Give the shape the allow-all default**, as for a policy with neither
  section. That would contradict both engines.

## Given up

- **This is Tailscale's behaviour on 2026-10-25, through an alpha
  surface.** It isn't documented, so it can change without notice. The
  run-time checks above catch a change; they don't prevent one.
- **Where the marker's source address comes from is still open.** Both
  an address outside the tailnet's range and one inside survived on
  Tailscale. #5 checks headscale before either is chosen.
