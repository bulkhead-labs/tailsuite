# 0001: Truth over mocks

Status: **accepted** (2026-10-23).

## Context

Every existing Tailscale policy tool checks the rules: Tailscale's own
`tests` block, the admin console's preview, linters and auditors. None of
them sends traffic. A rule evaluator can't catch a policy feature it
misreads, a route nobody approved, or a service that isn't listening.

## Decision

- **Real clients, real control planes, real traffic.** A test opens a real
  connection between real `tailscaled` nodes. Policy evaluation only fills
  the screen while the lab starts, labelled unverified.
- **Four results: PASS, FAIL, UNKNOWN, REFUSED.** UNKNOWN means the lab
  can't reproduce a condition the result depends on. REFUSED means the
  engine lacks a feature the test needs. Neither is ever shown as a pass or
  a deny.
- **Every deny has a positive control**: proof, in the same run, that the
  target is alive and reachable from an allowed source or from itself.
- **Every condition is proven both ways.** A rule gated on posture or a
  version gets a replica that meets it and one that doesn't.

## Why

A false "locked down" is the worst failure this tool can have.

Example: a grant to `tag:prod` needs `node:os == 'macos'`. Every lab node
is Linux, so every connection is dropped, and "alice's laptop can't reach
prod" goes green, while Alice's real Mac does reach prod.

The 2026-10-23 benchmark ([research 0004](../research/0004-benchmark-2026-10-23.md))
produced two false denies of its own: a stopped listener, and a client in
userspace mode.

## Given up

- **Speed and simplicity.** A lab is slower and heavier than evaluating
  rules.
- **Some results stay UNKNOWN** until the lab can reproduce them.
