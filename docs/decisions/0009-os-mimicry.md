# 0009: macOS and Windows are mimicked; real only for verify runs

Status: **accepted** (2026-10-24).

## Context

Containers share the host's Linux kernel, so a container can't be a Mac.
Docker-OSX runs a full macOS VM inside a container: it needs KVM and many
GB, it's fragile, and macOS on non-Apple hardware breaks Apple's licence.

## Decision

- **The packet filter is the same Go code on every OS.** For reachability,
  the OS matters only to posture rules (`node:os`) and per-OS features.
- **The lab builds a client that reports macOS or Windows.** The client's
  OS comes straight from the Go build target, with no override setting
  (`version.OS()` in `tailscale/tailscale`, checked 2026-10-24). So this is
  a small patch to BSD-3 code.
- **Results say "OS mimicked".**
- **Client versions are real**: rules on `node:tsVersion` run real images on
  both sides of the boundary.
- **Verify runs use real machines**: GitHub macOS and Windows runners, or
  tart on Apple Silicon (its licence isn't OSI open source).
- **headscale doesn't support posture**, so these tests are REFUSED there.

## Given up

**Per-OS client differences outside the filter** aren't covered by mimicry,
for example which platforms can run the Tailscale SSH server.
