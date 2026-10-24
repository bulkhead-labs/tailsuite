# 0002: Two engines from the first milestone

Status: **accepted** (2026-10-23). The default engine is decided by the
scorecards after M3 and M4 ([plan](../plan.md#milestones)).

## Context

The Tailscale client is open source, and it enforces the packet filter.
The control server turns the policy into filter rules and hands them out.

- **Tailscale's control server is closed and runs as a service.**
  Tailscale's alpha tailnet-creation API (docs validated 2026-10-20) creates
  **API-only tailnets** on every plan: up to 10 per organization, with no
  human users, tagged devices only, deletable.
- **headscale** (BSD-3, v0.29.5 on 2026-10-13) is an open-source rewrite of
  the control server that the same official clients connect to.

The two engines have opposite gaps ([research 0002](../research/0002-engines.md)):

| | headscale | Tailscale throwaway tailnet |
|---|---|---|
| Users, groups, `autogroup:self` | Native | Must be rewritten as tags |
| Posture, IP sets, Funnel | Unsupported | Supported |
| Who compiles the policy | headscale's own rewrite | Tailscale itself |
| Setup | None; offline | A test organization, an alpha API |

## Decision

- **Build both engines from M1.** Every feature is tested on both, or the
  engine that can't do it reports REFUSED.
- **One source policy, lowered per engine.** Shared features pass through
  unchanged. Identity is rewritten where an engine needs it (users to tags
  for throwaway tailnets; emails to user names for headscale). Every
  rewrite is recorded and shown.
- **Profiles choose the engine**: for example headscale on every PR, and
  Tailscale nightly and before deploy.
- **A conformance check.** Run the same policy on both, dump each node's
  compiled filter (`tailscale debug netmap`; confirmed to expose
  `PacketFilterRules`), and diff. Differences are reported, and sent
  upstream where they're headscale bugs.
- **Scorecards after M3 and M4** pick the default.

## Why

Two real implementations keep the engine interface honest. The engines
cover each other's gaps. headscale removes the setup barrier, and Tailscale
supplies the real compiler.

## Given up

- **About 1.5× the work** per feature.
- **A dependency on an alpha API from M1.**
- **A Tailscale test organization** is needed for CI.
