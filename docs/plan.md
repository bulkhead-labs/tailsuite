# Plan

**Last checked against the repo: 2026-10-26.** Current milestone: **M1, in
progress.**

This file is the plan of record. It changes in the same pull request as the
work it describes, and is checked again after every merge
([process](process.md)). If it disagrees with the code or the issues, it's
a bug.

## Goal

Anyone who runs a tailnet can see, in minutes, what it actually allows,
proven with real traffic. Then they can fix it, keep it fixed in CI, and
deploy the fix through OpenTofu, without cloning anything or trusting a
hosted service.

**How we'll know:**

| Measure | Target |
|---|---|
| Install to the first picture of a tailnet | Under 1 minute |
| Install to live, verified results | Under 5 minutes |
| Import to a zero-change `tofu plan` | Under 10 minutes |
| User-journey suite on every release | Green, from the published install channels |
| Results that are wrong rather than UNKNOWN | Zero known; each one is a `type:bug` |

## Principles

1. **Truth over mocks.** Real clients, real control planes, real traffic.
   Anything the lab can't reproduce shows as UNKNOWN or REFUSED, never as a
   pass or a deny ([0001](decisions/0001-truth-over-mocks.md)).
2. **Read-only by default; earn each permission.** Writing to a real
   tailnet is explicit, gated, and uses a credential held only for that
   write ([0007](decisions/0007-read-only-by-default.md)).
3. **Local first.** Policies and credentials stay on the user's machine or
   in their CI.
4. **Real probes, not policy evaluation**: SSH, HTTP(S), TCP, UDP, DNS,
   Postgres, ICMP.
5. **Minutes to value.** A picture first; live results stream in behind it.
6. **Swappable by design.** Sources, engines, sandboxes, probes and outputs
   sit behind interfaces ([architecture](architecture.md)).
7. **Open source, Apache-2.0.** Adoption first. A paid edition may come
   later, as separate modules; the core stays open
   ([0006](decisions/0006-apache-2-dco-open-core.md)).

## Milestones

Live progress is on the
[status page](https://bulkhead-labs.github.io/tailsuite/) and in
[GitHub milestones](https://github.com/bulkhead-labs/tailsuite/milestones).

| | Milestone | Status | What ships | How a user starts here |
|---|---|---|---|---|
| **M1** | [Walking skeleton](https://github.com/bulkhead-labs/tailsuite/milestone/1) | In progress | 3 nodes (laptop, server, db), a policy, 3 tests (allow, deny, positive control), both engines, the local UI, the OpenTofu module that configures the lab, the round-trip check, the release pipeline, the user-journey suite | `brew install` → `tailsuite init` → `tailsuite up`, about 5 minutes |
| **M2** | [Tags, grants and the diff](https://github.com/bulkhead-labs/tailsuite/milestone/2) | Not started | Tags, grants and ports; the reachability matrix; the blast-radius diff; the headline (#33); the baseline pack (#32), with any additions it needs in the test format (0015); the GitHub Action; the generated quick-start repo; the first sample configs | Add tailsuite to their repo's CI |
| **M3** | [SSH, subnets and exit nodes](https://github.com/bulkhead-labs/tailsuite/milestone/3) | Not started | Tailscale SSH (root vs nonroot, check mode, session recording); subnet routers with fake LANs; exit nodes with a fake internet; differential fuzzing across the engines (#42); **engine scorecard #1** | The scenarios people get burned by |
| **M4** | [Users and posture](https://github.com/bulkhead-labs/tailsuite/milestone/4) | Not started | Users and groups; posture (OS mimicked, real client versions) and `attrConfig`; equivalence classes; **engine scorecard #2**. The lab is feature complete | |
| **M5** | [Import](https://github.com/bulkhead-labs/tailsuite/milestone/5) | Not started | Read-only import from the Tailscale API or an OpenTofu plan (#36); onboarding with no credentials: a pasted policy or a demo (#35); a library of synthetic sample configs (a wide-open startup, a homelab, a segmented startup, contractors, subnet-heavy); import to OpenTofu with a zero-change plan as the proof | Connect read-only: their tailnet in 2 minutes, on IaC in 10 |
| **M6** | [Deploy](https://github.com/bulkhead-labs/tailsuite/milestone/6) | Not started | Editing the policy in the UI (#34); `tailsuite deploy` with the apply gates; PR mode for GitOps or Terraform-managed tailnets; deploy from the Action | Ship a change safely |
| **M7** | [Public launch](https://github.com/bulkhead-labs/tailsuite/milestone/7) | Not started | Docs site, the in-browser paste page, the launch post | Zero install |

Status values: Not started, In progress, Done. A milestone is Done when
every issue in it is closed **and** the user-journey suite passes on a
release built from it.

**Engine scorecards.** Both engines are built from M1
([0002](decisions/0002-two-engines.md)). After M3 and M4 they are scored on:

- setup time and run time;
- flaky runs per 100;
- REFUSED or UNKNOWN results on the same suite;
- divergences found by comparing their compiled filters;
- code size per engine, and what broke on upgrades.

The scorecard picks the default engine, and is recorded as a decision.

## Definition of done

Every feature, in every milestone, ticks all of these, or says why not in
its pull request:

1. **Lab:** it runs in the lab on **both engines**, or the engine that can't
   do it reports REFUSED.
2. **Tests:** unit tests, a lab integration test, and a step in the
   user-journey suite if users will touch it.
3. **UI:** it shows in the local UI.
4. **OpenTofu:** it's expressed in the module, and the round trip still
   gives the same results.
5. **Release:** it ships in the binary and works when installed from the
   release artifacts.
6. **Docs:** this plan, the architecture and the README are true after the
   merge.
7. **Decision:** a record in [`decisions/`](decisions/README.md) for any
   trade-off, added to its index.

The [pull request template](../.github/pull_request_template.md) carries
this list, and the `pr-ready` skill checks it before review
([process](process.md)).

## Test and release tiers

| Tier | When | What |
|---|---|---|
| Lint and unit | Every push to a PR or to `main` | Go tests, linters, the docs link check, the workflow audit, the Nix build; also a plain `go build` without Nix |
| Integration | Every push to a ready PR | The lab on headscale |
| **User journey** | When a PR is marked ready, on every push to a ready PR, on demand for any branch | Installs the binary **from the built artifacts**, the way a user would: `init --template startup` → `up` headless → `test` → `import` from a recorded "prod" → `tofu plan` shows zero changes → a scripted policy change → `test` shows the blast-radius diff → `deploy` to a second headscale → re-import → zero changes → teardown. Plus a Playwright smoke test of the UI. Steps that land in later milestones are stubbed until then |
| Real Tailscale | Nightly, and on demand: on a branch, through `branch-run`, with the maintainer's approval each run | The same journey with "prod" as a throwaway Tailscale tailnet and import over the real API. It re-records the API fixtures the per-PR run uses |
| Upstream canary | Nightly | Latest headscale and Tailscale client images |
| Release | On a tag | All tiers, then install from the **published** channels (Homebrew, script, container) on Linux and macOS runners, with signatures verified |

**Running:** lint and unit, on every push to a PR or to `main`
([0018](decisions/0018-go-toolchain-flake-ci.md)). **Not running yet:**
integration and the user journey, which arrive with #13. Until then, the
`pr-ready` skill and the reviewer agent are those checks.

**PRs open as drafts.** Drafts get lint and unit tests only. Marking a PR
ready starts the heavy tiers. Any branch can run them by hand
(`workflow_dispatch`) before it's ready
([0008](decisions/0008-tests-and-pr-flow.md)).

**Stability rules:**

- images pinned by digest;
- unique names per run, and teardown always runs;
- assert on results, never timings;
- bounded waits only for policy propagation;
- a flaky test goes on a quarantine list with an issue, never silently
  retried.

## M1: order and prerequisites

**Maintainer prerequisites.** Each one blocks the issues listed.

| Prerequisite | Blocks |
|---|---|
| **Done (2026-10-24):** the CI trust model's settings (#39, [0013](decisions/0013-ci-trust-model.md)) | CI in #1; #2, #13, #14; #30's CI step |
| **Done (2026-10-25):** a dedicated Tailscale test organization, and an OAuth client with only the `tailnets` scope, stored in the `nightly` and `branch-run` environments | #3, #6, #13's nightly tier |
| `bulkhead-labs/homebrew-tap` and the container package, created by the maintainer, and the credentials goreleaser publishes with, stored in the `release` environment | #2 |

**Order:**

1. **Settle the design questions that shape everything:**
   - #15, the tailnet model, together with #18 (the project layout, and
     explicit listeners in `lab.yaml` for M1): settled in
     [0014](decisions/0014-tailnet-model-and-project-layout.md);
   - #16, the test format: settled in
     [0015](decisions/0015-test-file-labels-profiles.md);
   - #17, the engine interface and capability matrix: settled in
     [0016](decisions/0016-engine-interface-capabilities.md);
   - #19, the UI stack: settled in [0017](decisions/0017-local-ui.md).
2. **Run the #3 spike**: done (2026-10-25). The Tailscale engine is a go
   ([research 0006](research/0006-throwaway-tailnets-spike.md)), and
   0016's two checks for it are settled in
   [0021](decisions/0021-empty-grants-and-the-marker-on-tailscale.md).
   Its findings for #6 are listed in research 0006. Leaked throwaway
   tailnets are deleted by a scheduled cleanup job, with a second OAuth
   client (`all` scope) held only in the `nightly` environment
   (maintainer, 2026-10-26, on #6).
3. **Build:**
   - #1, the Go module, the flake and CI for lint and unit tests: done
     ([0018](decisions/0018-go-toolchain-flake-ci.md));
   - #4, the Docker sandbox: done
     ([0019](decisions/0019-docker-sandbox.md));
   - #58, the tailnet model and loading a project: done
     ([0022](decisions/0022-reading-a-project.md));
   - #5, headscale: done ([0025](decisions/0025-headscale-engine.md)).
     Pinned addresses on headscale are #67, on M2; headscale 0.29.6 is
     #68, from 2026-11-07;
   - #7, the node agent: done
     ([0020](decisions/0020-node-agent.md));
   - #52, the node agent inside the binary: done
     ([0027](decisions/0027-embedded-agent.md));
   - #8, the test runner: done ([0026](decisions/0026-test-runner.md));
   - #12, the CLI;
   - #11, the UI;
   - #9, the OpenTofu module;
   - #10, the round trip.
4. **#6**, the Tailscale engine, after #3. M1 needs only the minimal
   rewrite (one user becomes one tag, and the rewrite is recorded); the
   hard cases are #21, on M3.
5. **#2 and #13** (releases and the journey suite), then #14.
6. **#31**, the status page's DOMPurify bump to 3.4.17: done.

Issues say "Depends on #n" where an order matters.

## Open design questions

Each is a [`type:design` issue](https://github.com/bulkhead-labs/tailsuite/issues?q=is%3Aopen+label%3Atype%3Adesign),
on the milestone where it must be settled, or on none (shown as later) when
nothing before M7 depends on it. When one is settled, its answer becomes a
decision record, and the issue closes with a link to it.

## Not building

- **A policy linter or rule evaluator on its own.** Static views exist only
  to show something while the lab starts, and are labelled unverified.
- **Probing real devices.** The lab is the only place traffic is sent.
- **A hosted service, for now.** It would hold many tailnets' maps and
  credentials. A threat model comes first (#27).
- **Writes to a real tailnet outside `deploy`.**

## Next, after M7: open source

These stay open, and get issues as their milestone is planned:

- **A graph view** of the matrix; **explain a path** ("why can A reach
  B?", from Tailscale's `/acl/preview` and the probe evidence); **dead,
  shadowed and redundant rules**.
- **Agent-written tests** from plain English, reviewed before they're saved.
- **An MCP server**, with tools: `ingest`, `run_suite`, `explain_path`,
  `propose_change`, `write_tests`, `diff`.
- **Segments and templates**: work, home, prod, lab, compiled to tags and
  grants. Warn before suggesting tags for personal devices, because tags
  remove user identity and change key expiry and SSH.
- **A filter-conformance report**: where headscale and Tailscale compile
  the same policy differently, shown to users and reported upstream.
- **Engine migration**, proven equivalent in the lab first.
- **Config hygiene checks** through Tailsnitch, as a plugin.
- **GitLab CI.**
- **Other mesh VPNs**: NetBird, ZeroTier.

## Later

Deliberately after M7. Some may become a paid edition:

- **Learn mode**: propose the tightest policy that still carries real
  traffic, from Tailscale's network flow logs (needs the
  `logs:network:read` scope).
- **Drift detection**: the live tailnet vs the repo.
- **Hosted runs**, and more parallelism than 9 throwaway tailnets at once
  (10 per organization, the original included).
- **A fleet view inside a company**: client versions, routes, exit-node use
  and posture, aggregated locally.
- **Multi-tailnet views** for MSPs.
- **SOC 2 evidence exports.**
- **An in-UI chat** that proposes changes from intent; it never applies them.
- **A hosted mode**: a way of deploying the same binary, not a rewrite, and
  only after a threat model (#27).
