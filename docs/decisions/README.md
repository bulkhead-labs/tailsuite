# Decisions

One record per choice with trade-offs: the context, the decision, the
evidence, and what is given up. A record is changed only by a new record
that supersedes it. New records take the next number; add them to this
table in the same PR.

| # | Decision | Status |
|---|---|---|
| [0001](0001-truth-over-mocks.md) | Truth over mocks: real traffic, four results, positive controls | Accepted 2026-10-23 |
| [0002](0002-two-engines.md) | Two engines from M1: headscale and Tailscale throwaway tailnets | Accepted 2026-10-23 |
| [0003](0003-docker-one-container-per-node.md) | Docker, one container per node, kernel TUN, TLS in the lab | Accepted 2026-10-23 |
| [0004](0004-opentofu-owns-config.md) | OpenTofu owns the tailnet config; tailsuite owns the machines | Accepted 2026-10-24 |
| [0005](0005-go-single-binary-nix.md) | Go, one signed binary, Nix for development | Accepted 2026-10-24 |
| [0006](0006-apache-2-dco-open-core.md) | Apache-2.0, DCO sign-off, open core later | Accepted 2026-10-24 |
| [0007](0007-read-only-by-default.md) | Read-only by default; earn each permission; local first | Accepted 2026-10-24 |
| [0008](0008-tests-and-pr-flow.md) | Test tiers, draft PRs, the user-journey suite | Accepted 2026-10-24 |
| [0009](0009-os-mimicry.md) | macOS and Windows mimicked; real only for verify runs | Accepted 2026-10-24 |
| [0010](0010-public-tracking-in-issues.md) | All work tracked in public GitHub issues | Accepted 2026-10-24 |
| [0011](0011-name-tailsuite.md) | The name is tailsuite | Accepted 2026-10-24 |
