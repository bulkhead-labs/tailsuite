# 0002: headscale and Tailscale as engines

Checked 2026-10-23 against headscale v0.29.5 (released 2026-10-13) and
Tailscale's public docs and API.

## How the pieces fit

- **The client (`tailscaled`, BSD-3) enforces the packet filter.** The same
  code runs whichever control server it talks to.
- **The control server compiles the policy** into per-node filter rules and
  hands them out.
  - **Tailscale's** is closed and runs as a service.
  - **headscale's** is an open-source rewrite.
- **The fidelity risk is therefore only the compile step**, plus features
  one side lacks.

## Feature support

| Rule or feature | Tailscale | headscale |
|---|---|---|
| Grants, ACLs, ports, via, autoApprovers, nodeAttrs, `tests`, `sshTests` | Yes | Yes |
| `autogroup:member`, `tagged`, `internet`, `nonroot`, `danger-all`, `self` | Yes | Yes |
| Users and groups | Yes; **not in API-only tailnets**, which hold tagged devices only | Yes |
| Role autogroups (`admin`, `owner`, `it-admin`, `network-admin`, `billing-admin`, `auditor`), `autogroup:shared` | Yes | No |
| Groups synced from an identity provider | Yes | No ("OIDC groups cannot be used in ACLs") |
| Device posture (`postures`, `srcPosture`) | Yes | No |
| IP sets | Yes | No |
| Funnel | Yes | No ([#1040](https://github.com/juanfont/headscale/issues/1040)) |
| Network flow logs | Yes | No ([#1687](https://github.com/juanfont/headscale/issues/1687)) |
| Tailnet Lock | Yes | No ([#1307](https://github.com/juanfont/headscale/issues/1307)) |
| SSH `check` mode | Yes | Since 0.29, with early bugs ([#3305](https://github.com/juanfont/headscale/issues/3305)) |

Sources: [headscale features](https://github.com/juanfont/headscale/blob/main/docs/about/features.md),
[headscale policy limits](https://github.com/juanfont/headscale/blob/main/docs/ref/policy.md).

Edge-case divergence is real even where both claim support. One example is
[headscale#3160](https://github.com/juanfont/headscale/issues/3160): an SSH
rule on tagged nodes that went wrong when two users shared a name.

## Tailscale's throwaway tailnets

The [tailnet creation API](https://tailscale.com/docs/features/tailnet-creation-api)
(alpha; docs validated 2026-10-20):

- **Available** on all plans.
- **Limit:** up to 10 tailnets per organization, the original included.
- **What they are:** no human users, tagged devices only, not shown in the
  admin console, configured through the public API, deletable.
- **Access:** an OAuth client with the `tailnets` scope creates them.
- **Not yet supported** by the Go client, the Terraform provider or the
  Pulumi provider.

Other API facts (from Tailscale's OpenAPI spec, 2026-10-23):

- **`POST /device/{id}/ip`** sets a device's Tailscale IPv4 address
  (`devices:core`).
- **Custom posture attributes** (`custom:` namespace) can be set per device
  or in batches (`devices:posture_attributes`).
- **The policy `ETag` with `If-Match`** prevents overwriting a concurrent
  edit. `If-Match: ts-default` matches only the untouched default policy.
- **`/acl/validate`** checks a policy and runs its tests; **`/acl/preview`**
  shows the rules that match a user or IP and port.

## Why people choose one or the other

- **headscale:** no third party holds the control plane (Tailscale sees
  metadata, never traffic), free with no limits, air-gapped or
  data-residency needs, or self-hosters who want to own it.
- **Tailscale:** nothing to run; posture, Funnel, flow logs, Tailnet Lock,
  synced groups and app connectors; SSO integrations, support, and a
  security team.
- **Safety:** whoever runs the control server is the root of trust, because
  it can add devices and change the policy.
  - **Tailscale with Tailnet Lock** removes most of that trust, so it's the
    safer choice in practice for most teams.
  - **headscale** is safer only for a team that mustn't trust a third party
    and can run it well.
