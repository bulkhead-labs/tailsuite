# 0001: What exists today

Checked 2026-10-23. GitHub stars and activity as of that day.

**No tool takes a real tailnet's policy, rebuilds its devices, and sends
real traffic to prove what gets through.** Everything popular checks the
rules.

| Tool | What it is | Size and activity | End to end? |
|---|---|---|---|
| The policy file's `tests` block, and [gitops-acl-action](https://github.com/tailscale/gitops-acl-action) to run it in CI | Tailscale checks allow and deny statements against the policy when it's saved | Built in; the action has about 150 stars, active | No. It evaluates the rules; no packets move |
| [Tailsnitch](https://github.com/Adversis/tailsnitch) | Audits a live tailnet through the API: about 57 checks for permissive rules, tags, keys and hygiene (MIT) | About 1.1k stars in 10 months; [reviewed by SANS ISC](https://isc.sans.edu/diary/32602) | No. Config scanning; a candidate plugin for hygiene checks |
| [tailviz](https://github.com/neodyme-labs/tailviz) | Draws the policy as a graph | 13 stars; inactive since 2024 | No |
| Tailscale's `natlab` / `vnet` | QEMU virtual networks for testing NAT traversal | Internal to Tailscale | No. Connectivity, not policy |
| [headscale `integration/`](https://github.com/juanfont/headscale/tree/main/integration) | Starts headscale plus real `tailscaled` containers, applies a policy, and asserts which `curl` and `ping` calls succeed | headscale has about 45k stars; `acl_test.go` is about 3,600 lines | **Yes**, but it's headscale's own test code, not a product. Its harness is the pattern to copy |

**Why this gap matters:**

- A new tailnet starts with an allow-all policy, and a policy file with no
  `acls` section allows all too
  ([Tailscale docs](https://tailscale.com/kb/1018/acls)).
- A denied connection drops silently, so a blocked path looks the same as
  an offline host
  ([Perun Engineering](https://perun.au/insights/tailscale-production/)).
- Tests written against the default policy can fail in unexpected ways
  ([tailscale#14117](https://github.com/tailscale/tailscale/issues/14117)).
