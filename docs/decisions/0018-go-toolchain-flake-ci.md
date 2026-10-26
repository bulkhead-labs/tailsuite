# 0018: The Go toolchain, the flake, and CI for lint and unit tests

Status: **accepted** (2026-10-25). Built in #1. Refines
[0005](0005-go-single-binary-nix.md); follows
[0008](0008-tests-and-pr-flow.md) and [0013](0013-ci-trust-model.md).
Amended by [0024](0024-merge-commits.md): with merge commits, "every
commit on `main` keeps its run" means every push to `main`.

## Context

- **0005:** Go; a flake whose dev shell has Go, OpenTofu, golangci-lint,
  goreleaser and the Docker CLI; CI uses it; plain `go build` still works.
- **The plan's first tier:** lint and unit tests on every push, with the
  docs link check.
- **CLAUDE.md rule 9:** pin everything, and pin a release only once it is
  two weeks old.
- **Found while building it (2026-10-25):**
  - The flake followed `nixos-26.05`. That channel builds its Go tools
    (golangci-lint, gopls, goreleaser) with Go 1.26.8, two security
    releases behind upstream's 1.26.10.
  - golangci-lint refuses to lint a module whose `go` line is newer than
    the Go it was built with.
  - The lock on `main` pointed at a channel release from 2026-10-22,
    three days old.

## Decision

### The Go module

- **`github.com/bulkhead-labs/tailsuite`, `go 1.27.0`.** The command is
  `cmd/tailsuite`. Code lives under `internal/` until something is meant
  to be imported by other projects.
- **Plain `go build ./...` and `go test ./...` work with Go 1.27 or
  later.** An older Go 1.21+ downloads 1.27 itself (`GOTOOLCHAIN=auto`).
- **`tailsuite version`** prints, in order of preference:
  - the version set at link time, by Nix now and goreleaser later (#2);
  - what Go recorded: the module version from `go install`, or a
    pseudo-version from the commit for a build in a git checkout;
  - `devel`.
- **An unknown command exits 3**, "couldn't run", the code
  [0015](0015-test-file-labels-profiles.md) gives `tailsuite test` for the
  same case.
- **No CLI framework yet.** #12 picks one.

### The flake

- **nixpkgs follows `nixos-unstable`, locked to a channel release at least
  two weeks old.**
  - Today: `nixos-26.11pre1089111.6510408d147d`, published 2026-10-10.
  - The two-week rule counts from the day the channel published the
    release (on releases.nixos.org), not the commit date.
  - Only channel releases, never an arbitrary commit: only those are built
    in cache.nixos.org.
  - A lock bump is a PR, and CI runs on it.
- **Go is `go_1_27`**, 1.27.2 in this lock. In this channel, golangci-lint
  and gopls are built with the same Go.
- **`packages.tailsuite`** (also the default) is built with
  `buildGo127Module`: static (cgo off), version `0.0.0-<commit>`.
  - `vendorHash` is `null` while there are no dependencies.
  - Every change to `go.sum` must update it to the hash `nix build`
    reports. CI fails until it matches.
- **`checks.tailsuite` is the package**, so `nix flake check` builds it,
  and runs the tests of the package it builds.
- **Two dev shells, one lock:**
  - `ci` has what CI runs: Go, golangci-lint, lychee, actionlint (with
    shellcheck), zizmor and nixfmt;
  - `default` adds gopls, goreleaser, OpenTofu, the Docker CLI, gh and jq.
- **The formatter is nixfmt**, nixpkgs' official one, in place of
  nixpkgs-fmt.

### CI: `.github/workflows/ci.yml`

- **Triggers, one run per commit:**
  - `pull_request`, drafts included: every push to a pull request runs it,
    on the merge with `main`. A fork's run needs the maintainer's approval
    (0013);
  - `push` to `main`;
  - `workflow_dispatch`, with no inputs, for a branch without a pull
    request yet.
- **No secrets and no environment.** `permissions: contents: read`, and
  the checkout keeps no credentials.
- **"Lint and unit" runs through the `ci` shell:**
  1. `go mod tidy -diff`;
  2. golangci-lint:
     - the standard set (errcheck, govet, ineffassign, staticcheck, unused);
     - plus bodyclose, errorlint, gosec, misspell, noctx, nolintlint,
       unconvert and usestdlibvars;
     - gofmt and goimports;
  3. `go test -race ./...`;
  4. **the docs link check**: lychee, offline. It checks links between
     files in this repo, `.github/` and `.claude/` included, and the
     headings they point at;
  5. **the workflow audit**: actionlint, which also runs shellcheck on
     every `run:` script, and zizmor offline. zizmor flags
     unpinned actions, broad permissions and template injection, the
     mechanical half of 0013's rules;
  6. `nixfmt --check flake.nix`;
  7. `nix flake check` and `nix build`, then it runs the binary.
- **"Plain go build"** runs `go build` and `go test` in
  `golang:1.27.2-bookworm`, pinned by digest, with no Nix. That keeps
  0005's "plain `go build` still works" tested.
- **Pinned:**
  - `actions/checkout` v7.0.1 and `cachix/install-nix-action` v31.11.1,
    which installs Nix 2.35.2, both by commit;
  - the runner, `ubuntu-24.04`.
- **A newer push to a pull request cancels its older run.** Every commit
  on `main` keeps its run.
- **No cache.** A run takes about a minute without one (below). Revisit
  when a run passes five minutes.
- **The checks aren't required by a ruleset.**
  - Rulesets are the maintainer's (0013).
  - 0013 leaves open whether a check posted by a branch workflow could
    satisfy a required check. Until that's answered, a required check
    would be a convenience, not a gate.
  - The maintainer's review stays the gate. `pr-ready` won't mark a PR
    ready unless the run on its head commit passed.

## Why

- **One Go everywhere.** On nixos-unstable, the compiler, golangci-lint
  and gopls are the same Go 1.27.2, and all of them come prebuilt from
  cache.nixos.org. On nixos-26.05, linting a Go 1.27 module would mean
  rebuilding golangci-lint on every run, and staying on Go 1.26 would mean
  tools two security releases behind.
- **The `ci` shell** downloads only what CI runs. It shares the lock, so
  its versions are the dev shell's.
- **An offline link check** can't fail a build because another site is
  down or rate-limits GitHub's runners.
- **zizmor and actionlint** catch an unpinned action or a broad permission
  before a reviewer has to.
- **One run per commit.** The first version ran on every branch push, and
  also on `pull_request` with an `if:` that skipped this repo's own
  branches. GitHub reports a job skipped by a condition as successful, so
  that skipped twin could satisfy a required check the real run had
  failed.
- **A container job for plain Go** proves the claim without another
  action to pin.

## Evidence (checked 2026-10-25)

- **Go releases** ([go.dev](https://go.dev/doc/devel/release)):
  - 1.27.2, 2026-10-08: security fixes to the go command, crypto/tls,
    html/template, net/http, net/textproto and os.
  - 1.27.3, 2026-10-19: a security fix to crypto/x509. It's too new for
    the two-week rule. Nothing ships before #2, which should build releases
    with a Go that has it.
- **nixos-26.05**, the 2026-10-11 release:
  - Go 1.26.8, and `go_1_27` 1.27.2;
  - golangci-lint 2.12.2, gopls and goreleaser, all built with Go 1.26.8;
  - OpenTofu 1.11.14.
- **nixos-unstable**, the 2026-10-10 release:
  - `go_1_27` 1.27.2;
  - golangci-lint 2.14.0 and gopls, built with Go 1.27.2;
  - goreleaser 2.17.1;
  - OpenTofu 1.13.1, the version [research 0005](../research/0005-tailnet-model-trial.md)
    used;
  - lychee 0.24.2, actionlint 1.7.12, shellcheck, zizmor 1.30.1,
    nixfmt 1.5.0;
  - the Docker CLI 29.8.1.
- **Every check fails on a planted fault.** Run locally in `nixos/nix`
  2.34.9, on a copy of the tree with:
  - a link to a missing file;
  - a link to a missing heading;
  - unformatted Go;
  - a failing test;
  - an unpinned action;
  - an unformatted `flake.nix`.

  `nix flake check` also failed on the failing test.
- **The first run on GitHub**
  ([run 40210360835](https://github.com/bulkhead-labs/tailsuite/actions/runs/40210360835)):
  - "Lint and unit" took 60 s: installing Nix 4 s, fetching the tools
    14 s, lint 8 s, the race tests 13 s, `nix flake check` and
    `nix build` 15 s.
  - "Plain go build" took 30 s, 17 s of it pulling the image.
- **A job skipped by a condition reports success**: "It will not prevent
  a pull request from merging, even if it is a required check"
  ([Control jobs with conditions](https://docs.github.com/en/actions/how-tos/write-workflows/choose-when-workflows-run/control-jobs-with-conditions)).
- **`install-nix-action` v31.11.1:**
  - it installs Nix 2.35.2 from releases.nixos.org;
  - it writes the job's `GITHUB_TOKEN` into `/etc/nix/nix.conf` as a
    GitHub access token, for rate limits. Read in its `install-nix.sh`.

## Alternatives

- **nixos-26.05 with Go 1.26.** Everything is cached, but the Go tools lag
  security releases, and a dependency that needs Go 1.27 would force a
  change anyway.
- **nixos-26.05 with golangci-lint rebuilt with Go 1.27.** Minutes of
  compiling per run, unless a Nix cache holds it.
- **golangci-lint through `go tool`.** Its maintainers don't support
  building it from source, and it would share the module's dependency
  graph.
- **CI without Nix** (setup-go and golangci-lint's action). Faster with
  caches, but CI and the dev shell would drift apart, against 0005.
- **Every branch push, plus forks' pull requests** (the first version).
  It gave every pull request a skipped, "successful" twin of each check.
- **Checking external links on every push.** Other sites' outages would
  fail builds. A nightly check could do it later.
- **A Nix cache** (Cachix, cache-nix-action). Not needed at a minute a
  run. Pushing to Cachix needs a secret, which branch runs can't have.
- **The Determinate Systems installer.** It installs Determinate's own
  Nix distribution by default; `install-nix-action` installs upstream Nix.

## Given up

- **An unstable channel's bumps can bring bigger changes** than a stable
  one's. Each bump is a PR, and CI runs on it.
- **Plain builds need Go 1.27**, or a Go that downloads it.
- **`vendorHash` must be kept in step with `go.sum`.**
- **A branch without a pull request runs CI only by hand.** Agents open
  pull requests as drafts at once, so it's rare.
- **External links aren't checked** on every push.
- **The job's read-only token sits in `nix.conf`**, readable by anything
  the job runs, the tests included. It can read this public repo, and
  nothing more.
- **Some tests run twice**: in `go test`, and in `nix build`'s check
  phase. Seconds, for now.
