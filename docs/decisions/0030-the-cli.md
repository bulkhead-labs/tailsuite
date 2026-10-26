# 0030: The CLI, and how its commands share a lab

Status: **accepted** (2026-10-26). Settles #12. Names the commands
[0014](0014-tailnet-model-and-project-layout.md) and
[0015](0015-test-file-labels-profiles.md) left to it.

## Context

- **`up` and `test` are separate processes, and the lab outlives both**
  ([0019](0019-docker-sandbox.md), [0020](0020-node-agent.md)). The user
  journey runs `up` headless, then `test`; a user may keep `up` open for
  the UI and run `test` in another terminal.
- **The engine and the runner live in one process.** `runner.Execute`
  needs an `engine.Lab` and the sandbox, and until now only `Start` made
  either.
- **Teardown already needs no state** ([0019](0019-docker-sandbox.md)):
  everything carries its run's ID, and `TeardownRun` removes a run from
  the ID alone.
- **The UI is #11.** #12 listed `up`'s browser, redirect-file and link
  behaviour; they move to #11 with the UI they open.

## Decision

### The commands

- **`tailsuite init --template startup [--nix] [dir]`** writes the
  template embedded in the binary (`internal/templates`): `policy.hujson`,
  `lab.yaml` with its profiles, `tests/`, `tofu/tailnet.auto.tfvars` and a
  `.gitignore` for `.tailsuite/`. `tofu/tailsuite.tf` arrives with #9.
  - It writes nothing if any of its files is already there, except
    `.gitignore`: an existing one, as in a new repository, gets the lines
    it lacks.
  - `--nix` adds a `flake.nix` whose dev shell has this release of
    tailsuite, by its tag, and OpenTofu from the nixpkgs tailsuite's own
    flake locks; `nix develop` writes the lock file that pins both. A
    build that isn't a release (`vX.Y.Z`) has no tag to pin, so `--nix`
    refuses it. The release pipeline (#2) must stamp the version as
    `vX.Y.Z`; tailsuite's own flake stamps `0.0.0-<rev>`, so a Nix build
    is refused too.
- **`tailsuite up [--profile] [--engine]`** starts the lab on the
  profile's engine with the embedded node agent for the Docker host's
  architecture (`agentbin.For(sandbox.Arch())`,
  [0027](0027-embedded-agent.md)), and returns, leaving it running.
  - A build without the agent is refused before Docker is touched, with
    `agentbin.ErrNotEmbedded`'s message.
  - If the lab fails to start and its teardown fails too, the state file
    stays, so `down` can remove what's left.
  - A project whose lab is already up is refused.
- **`tailsuite test`** takes `--profile`, `--engine`, `--only`, `--skip`,
  `--run` and `--strict` (0015), and `--json` and `--junit <file>` for the
  outputs #8 built. It exits with 0015's codes.
- **`tailsuite down [--run <id>]`** tears the project's lab down, or any
  run by its ID. With no lab up, it lists the runs the Docker Engine still
  has, so one left by a crash can be found.
- **`tailsuite capabilities [--json]`** prints each built engine's rows of
  0016's matrix.
- **Named, not built:**
  - **`tailsuite export tests`** (#78) copies the tests that fit Tailscale's
    format into the policy's `tests` and `sshTests`, showing the diff
    first (0015). It writes tailsuite's tests into a file production
    reads, as `export tofu` writes `tofu/`.
  - **`tailsuite upgrade tofu`** replaces `tofu/tailsuite.tf` with this
    release's copy and leaves the values alone (0014; #9 builds it).

### How `up`, `test` and `down` share a lab

- **`up` writes `.tailsuite/lab.json`** (mode 0600) before the lab
  starts, so a crash while it starts still leaves the run ID for `down`.
  It records what the lab was built from:
  - the run ID and the project's absolute path;
  - the engine;
  - the build: tailsuite's version, the images, and the SHA-256 of the
    node agent;
  - the plan: every device's name, user, tags and pinned address.

  The lab itself is found by its labels.
- **`test` reopens that lab only if it's still what this project and
  this build would start.** Otherwise it's exit 3, with what to run
  (`down`, then `up`):
  - a state from another folder (a copied project);
  - another engine;
  - a changed device. A node keeps the user and tags it joined with, and
    re-applying the policy doesn't change them, so a lab built from the
    old plan would prove the old identities;
  - another build: the images and the agent would differ from what the
    results name.

  `docker.Attach` then finds the run's nodes and networks, and the
  engine's `Attach` checks that the control plane is healthy and reads
  each node's tailnet addresses.
- **It applies the current policy first**, with a fresh readiness
  marker, and waits for every node to hold it (0016). The policy may have
  changed since `up`, and a result read off the old one would be false.
- **After the probes, every node must still hold that marker**
  (`StillHolds`). If another process applied a policy meanwhile, as a
  second `test` would, every PASS and FAIL becomes UNKNOWN: they weren't
  read under this run's policy (0001).
- **With no lab up, `test` starts one of its own** and tears it down
  however the run ends. A state file whose run is gone is removed, and
  said.
- **Every problem with the project, the flags or the selection is exit 3,
  before any lab is touched.** So is a lab that won't start, attach or
  become ready; an interrupt (Ctrl-C), whose cut-short probes prove
  nothing; and results that couldn't be written to stdout or `--junit`,
  since nobody saw them. A failed `--junit` is written first, so the
  printed results carry the same exit, 3.
- **On its own lab, a teardown that fails after the run** keeps the
  results, which the lab proved, and says how to remove what's left
  (`down --run`).
- **The last run's JSON goes to `.tailsuite/results.json`**, for the UI
  (#11). Failing to save it is a warning; it loses no result.
- **Only headscale is built.** `--engine tailscale`, or a profile that
  picks it, is exit 3 until #6.

## Why

- **One process owns a lab only while it runs.** A state file with just
  the run ID keeps `up`'s lab reachable from `test` and `down`, and the
  labels stay the truth about what exists.
- **Re-applying on every `test` is cheap** (seconds on headscale) and
  rules out testing a policy the user already changed.
- **`test` on its own works in CI** with no `up` before it.

## Evidence (checked 2026-10-26)

- `cmd/tailsuite/integration_test.go` on a real headscale lab, 40
  seconds on the maintainer's VM:
  - `test` with no lab up, and on `up`'s lab (without starting its own),
    give the same summary;
  - `--strict` exits 2 on the template's UNKNOWN and REFUSED results;
  - a device's tags changed after `up` is exit 3;
  - a second process's apply is seen by `StillHolds`;
  - removing a grant after `up` makes its test FAIL (exit 1);
  - `down` removes the run and the state, and a state left by a gone lab
    is removed, with `test` starting its own.

## Given up

- **Two `test` runs on one lab at once** spoil each other: the one whose
  policy was replaced reports UNKNOWN. Run them one at a time.
- **A lab is reopened only by the project folder that started it**, and
  only by the same build. After an upgrade, or a change to a device,
  `down` and `up` again.
- **`--nix` needs a release build**, so it can't be tried from source.
