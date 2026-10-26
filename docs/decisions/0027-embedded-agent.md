# 0027: The tailsuite binary carries the node agent

Status: **accepted** (2026-10-26). Built in #52. Follows
[0020](0020-node-agent.md), which left it to #52, and
[0005](0005-go-single-binary-nix.md) ("Embedded: the UI and the node
agent").

## Context

- Every lab node runs `tailsuite-agent` (0020): a stdlib-only Go binary,
  copied in through the sandbox.
- **A lab node is Linux on the Docker host's architecture**, which isn't
  tailsuite's on a Mac: an arm64 Mac runs an arm64 Linux VM, an Intel Mac
  an amd64 one.
- Until now only the tests built the agent, on the fly.

## Decision

- **`internal/agentbin` embeds `bin/`**, and `go generate
  ./internal/agentbin` builds the agent there for linux/amd64 and
  linux/arm64 (`-trimpath`, stripped, cgo off). git ignores the binaries;
  a README keeps the folder, so plain `go build` and `go install` still
  compile.
- **Nix builds generate it** in `preBuild` (not while vendoring modules),
  and the package's tests fail unless both are embedded
  (`TAILSUITE_REQUIRE_AGENT=1`). goreleaser (#2) will run the same `go
  generate`.
- **The agent is picked by the Docker host's architecture**, from the
  Engine's version response (`docker.Sandbox.Arch`), never tailsuite's
  own.
- **A build without it says so**, with how to get one: install a release,
  or `go generate ./... && go build ./cmd/tailsuite`. `tailsuite version`
  names the agents a build carries. `tailsuite up` (#12) refuses to start
  a lab without one, with the same message.
- **No fallback to building the agent with a local Go toolchain.** The
  agent would then depend on whatever Go is installed, unpinned and
  untested, and a missing toolchain would fail late, mid-start.

## Why

- **Both architectures in every build** means one binary per host OS,
  whatever Docker runs, at about 14 MB extra.
- **`go generate` with a committed README** keeps the source tree
  buildable by anyone, and every shipped build complete.

## Evidence (2026-10-26)

- `nix build .#tailsuite`: the binary is 15.7 MB, and `tailsuite version`
  prints `node agent: linux/amd64, linux/arm64`. Its tests require both.
- The embedded agents are 7.3 MB (amd64) and 6.8 MB (arm64), as 0020
  measured; a unit test checks each is a Linux ELF binary for its
  architecture.
- `go build ./cmd/tailsuite` without generating gives a 2.4 MB binary
  that prints `node agent: not embedded`.

## Alternatives

- **The agent as a mode of the tailsuite binary.** Rejected in 0020: a Mac
  would carry two Linux builds of all of tailsuite.
- **Fetching the agent from the release at run time.** A network call,
  and a second artifact to verify, on every first start.
- **A build tag for embedding.** A plain build would need the tag to
  compile either way; the README does it with nothing to remember.

## Given up

- **About 14 MB** in every binary.
- **A plain `go build` can't start a lab** until `go generate` has run.
- **A stale `bin/`** from an earlier `go generate` is embedded as it is;
  the agent's hello (0020) refuses one that speaks another protocol.
