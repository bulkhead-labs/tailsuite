# 0008: Test tiers, draft PRs, and the user-journey suite

Status: **accepted** (2026-10-24). The tiers are in the
[plan](../plan.md#test-and-release-tiers).

## Decision

- **The user-journey suite is part of M1.** It installs the binary from the
  built artifacts, the way a user would, and walks the whole journey: init,
  up, test, import, a zero-change plan, a change, the diff, deploy,
  re-import.
- **PRs open as drafts.** Drafts run lint and unit tests. Marking a PR ready
  starts integration and the journey; every push to a ready PR re-runs them.
  Any branch can run them on demand.
- **Real Tailscale runs nightly and on demand**, never as a per-PR gate (an
  alpha API, the network, the 10-tailnet cap). In per-PR runs, "prod" is a
  second headscale, and import reads recorded API responses. The nightly run
  re-records them so they stay honest.
- **Releases install from the published channels** on Linux and macOS
  runners, with signatures verified.
- **Every PR goes through the `pr-ready` checklist and an independent
  reviewer agent; every merge through the `post-merge` check**
  ([process](../process.md)).

## Why

What we're selling is the first five minutes, so that's what we test.
Keeping things that change on their own out of the per-PR gate keeps it
stable.

## Given up

**An upstream break can take up to a day to show**, through the nightly or
the canary.
