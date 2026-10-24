# 0003: What "the tailnet config" is, and the IaC options

Checked 2026-10-23.

## The config is more than the policy file

| Layer | Lives in | Official Terraform/OpenTofu provider |
|---|---|---|
| **Policy file**: grants and ACLs, groups, tagOwners, hosts, IP sets, SSH, nodeAttrs, autoApprovers, postures, tests | One HuJSON document | `tailscale_acl` |
| **Device state**: tags, enabled subnet routes, key expiry, authorization, name, IP | Per device, through the API | `tailscale_device_tags`, `tailscale_device_subnet_routes`, `tailscale_device_key`, `tailscale_device_authorization` |
| **What a device advertises**: routes, exit node, `--ssh`, shields-up, accept-routes, Serve and Funnel | On the device (`tailscale up` / `tailscale set`) | No; partly visible through the API |
| **DNS**: nameservers, split DNS, search paths, MagicDNS | Tailnet settings | `tailscale_dns_configuration` (or the split resources) |
| **Tailnet settings**: device and user approval, posture collection, auto-updates, flow logs | Tailnet settings | `tailscale_tailnet_settings` |
| **Users, roles, SSO** | Admin console, the identity provider | Partly |
| **Keys, OAuth clients, webhooks, log streams, posture integrations, Services** | API | `tailscale_tailnet_key`, `tailscale_oauth_client`, `tailscale_webhook`, `tailscale_logstream_configuration`, `tailscale_posture_integration`, `tailscale_service` |

The provider's resource list was read from
[tailscale/terraform-provider-tailscale](https://github.com/tailscale/terraform-provider-tailscale)
(about 377 stars; active). Its `base_url` setting (env
`TAILSCALE_BASE_URL`) points it at another API, which was checked in the
source on 2026-10-24.

## IaC options

| Layer | Options |
|---|---|
| Policy file only | [gitops-acl-action](https://github.com/tailscale/gitops-acl-action) (official), [tailscale-acl-combiner](https://github.com/tailscale-dev/tailscale-acl-combiner), the plain file plus the API |
| Tailnet state | **Terraform / OpenTofu** (the official provider); **Pulumi** ([pulumi-tailscale](https://github.com/pulumi/pulumi-tailscale), bridged from the Terraform provider, about 24 stars) |
| Device side | Ansible ([artis3n/ansible-role-tailscale](https://github.com/artis3n/ansible-role-tailscale), about 482 stars), the NixOS module, cloud-init, MDM profiles |
| Kubernetes | Tailscale's Kubernetes operator (in-cluster resources, not the policy) |
| headscale | The policy as a file, or `headscale policy set`; a community Terraform provider ([awlsring/terraform-provider-headscale](https://github.com/awlsring/terraform-provider-headscale), about 47 stars, quiet since March 2026) |

No maintained Crossplane provider was found.

**What this means for tailsuite:** Terraform/OpenTofu first
([decision 0004](../decisions/0004-opentofu-owns-config.md)). Device-side
config stays a checklist until a later milestone.
