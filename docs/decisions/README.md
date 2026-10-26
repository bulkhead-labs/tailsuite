# Decisions

One record per choice with trade-offs: the context, the decision, the
evidence, and what is given up. A record is changed only by a new record
that supersedes it. New records take the next number; add them to this
table in the same PR.

| # | Decision | Status |
|---|---|---|
| [0001](0001-truth-over-mocks.md) | Truth over mocks: real traffic, four results, positive controls | Accepted 2026-10-23 |
| [0002](0002-two-engines.md) | Two engines from M1: headscale and Tailscale throwaway tailnets | Accepted 2026-10-23; refined by 0016 |
| [0003](0003-docker-one-container-per-node.md) | Docker, one container per node, kernel TUN, TLS in the lab | Accepted 2026-10-23; refined by 0016; amended by 0019 |
| [0004](0004-opentofu-owns-config.md) | OpenTofu owns the tailnet config; tailsuite owns the machines | Accepted 2026-10-24; amended by 0014 |
| [0005](0005-go-single-binary-nix.md) | Go, one signed binary, Nix for development | Accepted 2026-10-24 |
| [0006](0006-apache-2-dco-open-core.md) | Apache-2.0, DCO sign-off, open core later | Accepted 2026-10-24; amended by 0024 |
| [0007](0007-read-only-by-default.md) | Read-only by default; earn each permission; local first | Accepted 2026-10-24 |
| [0008](0008-tests-and-pr-flow.md) | Test tiers, draft PRs, the user-journey suite | Accepted 2026-10-24 |
| [0009](0009-os-mimicry.md) | macOS and Windows mimicked; real only for verify runs | Accepted 2026-10-24 |
| [0010](0010-public-tracking-in-issues.md) | All work tracked in public GitHub issues | Accepted 2026-10-24 |
| [0011](0011-name-tailsuite.md) | The name is tailsuite | Accepted 2026-10-24 |
| [0012](0012-status-page-hosting.md) | The status page: GitHub Pages from `docs/`, no build | Accepted 2026-10-24; context amended by 0013 |
| [0013](0013-ci-trust-model.md) | Agents write CI; nothing merges or ships without the maintainer | Accepted 2026-10-24; settings in place (#39) |
| [0014](0014-tailnet-model-and-project-layout.md) | The tailnet model, and which project file owns each fact | Accepted 2026-10-24; `lab.yaml` sketch refined by 0015 and 0016 |
| [0015](0015-test-file-labels-profiles.md) | The test file, labels and profiles | Accepted 2026-10-25 |
| [0016](0016-engine-interface-capabilities.md) | The engine interface and the capability matrix | Accepted 2026-10-25; amended by 0021 and 0025 |
| [0017](0017-local-ui.md) | The local UI: the map first, localhost only, no build step | Accepted 2026-10-25 |
| [0018](0018-go-toolchain-flake-ci.md) | The Go toolchain, the flake, and CI for lint and unit tests | Accepted 2026-10-25; amended by 0024 |
| [0019](0019-docker-sandbox.md) | The Docker sandbox, and agents controlled through it | Accepted 2026-10-25 |
| [0020](0020-node-agent.md) | The node agent: it lives with the node, and sessions come and go | Accepted 2026-10-25 |
| [0021](0021-empty-grants-and-the-marker-on-tailscale.md) | An empty `grants` section denies everything, and the marker holds on Tailscale | Accepted 2026-10-25 |
| [0022](0022-reading-a-project.md) | Reading a project: the policy's edge cases, and strict project files | Accepted 2026-10-25 |
| [0023](0023-subagents-on-opus.md) | Subagents run on Opus, with the effort set by the work | Accepted 2026-10-26 |
| [0024](0024-merge-commits.md) | Pull requests merge with merge commits, not squash | Accepted 2026-10-26 |
| [0025](0025-headscale-engine.md) | The headscale engine: its lab, its copy of the policy, and readiness | Accepted 2026-10-26 |
