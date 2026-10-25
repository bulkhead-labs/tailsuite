# 0003: Docker, one container per node, kernel TUN, TLS in the lab

Status: **accepted** (2026-10-23). Evidence:
[research 0004](../research/0004-benchmark-2026-10-23.md). Refined by
[0016](0016-engine-interface-capabilities.md): rules that name a Tailscale
IPv6 address are refused, not only flagged. Amended by
[0019](0019-docker-sandbox.md): agents are controlled through the
sandbox's exec channel, not over a separate network.

## Decision

- **Docker is the sandbox.** Podman works through the same API. A VM driver
  (Lima) comes later, for full isolation from the host's network. Real macOS
  and Windows nodes are only for verify runs ([0009](0009-os-mimicry.md)).
- **One container per lab node**, each with its own `tailscaled` and network
  namespace. The user still runs one command.
- **Kernel TUN mode** (`NET_ADMIN` and `/dev/net/tun`). Userspace networking
  is refused.
- **headscale in the lab runs with TLS** from a per-run lab CA, and so does
  its DERP server. Its policy is set in database mode from the start, so the
  control server never restarts mid-run.
- **Addresses are real, never translated.** Tailscale IPs are pinned to the
  real tailnet's (`POST /device/{id}/ip` on Tailscale; allocation on
  headscale, #5). LAN ranges behind subnet routers are recreated as they
  are, inside the lab's networks. A translation layer would test the
  mapping, not the tailnet. Tailscale IPv6 addresses can't be pinned, so
  rules that name them are flagged.
- **Agents are controlled over a separate network**, never over the tailnet.
  Otherwise a deny rule would cut off the test's own control traffic.

## Why

**Measured on 2026-10-23**: 10 nodes online in 3.3 s, 20–31 MiB each, and
a policy change seen in real traffic within 2.5 s.

**Kubernetes (k3d, kind, minikube)** runs the same containers plus a
control plane, gives one flat pod network where we need a LAN per subnet
router, and needs privileged pods for TUN. It may come later, only to test
Tailscale's Kubernetes operator or to run large labs on a remote cluster.

**The benchmark caught two traps:**

- The official image defaults to userspace networking, where outbound
  connections from a node silently fail.
- After a control-server restart, clients force TLS on port 443 and log
  out.

## Given up

- **On Linux, lab LAN ranges can collide with the host's routes** until the
  VM driver exists.
- **macOS and Windows aren't real** in the default lab.
