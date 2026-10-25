# 0004: OpenTofu owns the tailnet config; tailsuite owns the machines

Status: **accepted** (2026-10-24). How headscale is driven is open (#20).
Amended by [0014](0014-tailnet-model-and-project-layout.md): on headscale,
M1 reads the values from `tofu/tailnet.auto.tfvars`, not from
`tofu plan -json`, because a plan needs a Tailscale API.

## Decision

- **tailsuite** creates and tears down the lab's machines: containers,
  networks, the control plane, the agents.
- **OpenTofu, with the official Tailscale provider, applies the tailnet's
  configuration** to the lab and, at deploy time, to production, **from the
  same module**: the policy, device tags, routes, DNS and settings.
- **Terraform and OpenTofu first.** The plan JSON (`tofu show -json`) is an
  input. Pulumi comes later.
- **Export to OpenTofu is in M1**, because it's how the lab itself is
  configured.
- **Devices are referenced by name**, not ID; IDs differ between the lab and
  production.
- **The round trip is the acceptance test.** Export, `tofu apply` to a fresh
  lab, and the same tests give the same results.

**Per engine:**

- **Tailscale throwaway tailnet:** native. tailsuite creates the tailnet
  (the alpha creation API isn't in the provider), then OpenTofu configures
  it with that tailnet's credentials.
- **headscale:** M1 renders the values from `tofu plan -json` and applies
  them itself. The provider has a `base_url` setting (env
  `TAILSCALE_BASE_URL`, checked in the source on 2026-10-24), so a shim
  speaking the needed part of Tailscale's API over headscale could let the
  same provider target it. Decided after scorecard #1 (#20).

## Why

The code that configured the lab is the code that configures production,
so the two are in parity by construction. It also turns "export to IaC"
from a feature into the normal path.

The official provider (checked 2026-10-23) covers the policy, device tags,
subnet routes, keys, DNS, tailnet settings, webhooks, log streaming,
posture integrations, OAuth clients and Services. What devices *advertise*
(`tailscale up` flags) isn't in it; that stays device-side
([research 0003](../research/0003-tailnet-config-and-iac.md)).

## Given up

- **OpenTofu becomes a runtime dependency**, fetched pinned if missing.
- **Plan and apply add seconds** to each lab run.
- **On headscale, parity is by rendering the values** until the shim
  question is settled.
