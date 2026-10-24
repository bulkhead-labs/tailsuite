# 0006: Apache-2.0, DCO sign-off, open core later

Status: **accepted** (2026-10-24).

## Decision

- **Apache-2.0** for everything in this repo.
- **Developer Certificate of Origin**: every commit is signed off. No CLA.
- **A trademark policy** for the name ([TRADEMARK.md](../../TRADEMARK.md)).
- **A paid edition may come later**, in separate modules (for example hosted
  runs, drift detection, learn mode, fleet views). What is open stays open.

## Why

Adoption is the goal. Company legal teams approve Apache-2.0 for use in CI
without a meeting. It carries a patent grant, and it fits the ecosystem:
Tailscale and headscale are BSD-3, OpenTofu is MPL-2.0.

Rejected:

- **AGPL:** many companies ban it outright, which hurts a tool that has to
  run in their CI.
- **BSL and FSL:** source-available, not open source. Terraform's move to
  the BSL is why OpenTofu exists, and our users chose OpenTofu.

A product's moat is the name, hosted convenience, enterprise features and
the expertise behind them, not the licence.

## Given up

- **Anyone may host or fork it**, commercially included.
- **Without a CLA, contributors' code can't be relicensed.** That's fine for
  open core, where paid features are separate code.
