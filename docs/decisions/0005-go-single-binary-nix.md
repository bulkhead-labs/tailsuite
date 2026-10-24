# 0005: Go, one signed binary, Nix for development

Status: **accepted** (2026-10-24).

## Decision

- **Go.** tailscale, headscale, tsnet, the Terraform provider,
  testcontainers-go and `tailscale/hujson` are all Go.
- **One binary, no cloning.**
  - goreleaser builds Homebrew, an install script, `go install`, release
    binaries, a container image and a GitHub Action.
  - Every release is signed with cosign (keyless), with an SBOM and
    provenance.
  - The UI and the node agent are embedded; lab images are pinned by digest.
  - OpenTofu is fetched pinned and checksummed when it isn't installed.
- **Nix from day one for the repo.** A flake with a dev shell (Go, OpenTofu,
  golangci-lint, goreleaser, the Docker CLI); CI uses it. Plain `go build`
  still works without Nix.
- **Nix for users is optional.** `tailsuite init --nix` adds a flake that
  pins tailsuite and OpenTofu to the project.
- **Templates are embedded in the binary.** A quick-start template
  repository is generated from them on each release, never edited by hand.
- **The release pipeline is part of M1**, so every milestone is installable
  and the install path is tested from the start.

## Why

Users of a security tool should never have to clone it, and should be able
to verify what they install. Nix gives contributors and CI the same
toolchain.

## Given up

- **Nix adds CI time** unless it's cached.
- **Signing and provenance add pipeline work in M1.**
- **Nix pins the toolchain but not the lab**, which still needs Docker.
