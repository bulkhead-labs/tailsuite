# 0033: How `up` starts and stops: detach, clean, and lab.yaml's defaults

Status: **accepted** (2026-10-26). Settles #81. Amends
[0030](0030-the-cli.md): `up` can also hold the lab in the background.

## Context

- 0030: `up` holds the lab in the foreground, and Ctrl-C tears it down.
- The maintainer asked for options on how `up` starts and stops: a
  detached or background mode, and starting by cleaning up old leftovers
  (2026-10-26). They chose:
  - `--detach` runs a background process that holds the lab, as the
    foreground `up` does, rather than starting a lab nothing holds;
  - `--clean` removes this project's leftovers only, with a separate
    `down --all` for every run;
  - defaults for both in `lab.yaml`. A `--keep` (Ctrl-C leaves the lab)
    and a teardown-timeout flag were offered and not chosen.

## Decision

- **`up --detach`** starts tailsuite again as a background process, in a
  session of its own (a new process group without a console on Windows),
  with `up --detached-holder=<token>`, a random token. That process does
  what a foreground `up` does, writing to a log of its own,
  `.tailsuite/up-<token>.log` (0600). One file per holder, because an
  earlier holder may still have its own open for a few seconds, and on
  Windows a file that's open can't be removed; earlier logs are removed
  when they can be. `up` waits until the lab state carries that token and
  says the lab is ready, prints the log, and returns: a token, not the
  process ID, which may be reused. If the process stops first, `up`
  prints its log and exits 3. Ctrl-C while waiting stops it; `up` then
  checks whether the lab is gone, and says to run `down` if not.
- **`down` ends a detached lab** the way it ends any: it tears the run
  down. The holder checks its lab every 5 seconds, sees it gone, and
  exits. Nothing signals a process by its ID, so a reused ID can't be hit.
- **`up --clean`** first tears down every earlier lab of this project:
  - the runs carrying the project's label, a hash of its folder's path
    (`com.bulkheadlabs.tailsuite.project`), which every lab `up` or
    `test` starts now carries;
  - and the run the state file names, for a lab started before labels.

  Other projects' labs are never touched. A holder whose lab goes exits.
  It runs after the policy's checks, so a bad policy leaves the lab
  that's up. The label hashes the folder's path with symlinks resolved
  (and, on Windows, in lower case), and the lab state records the same
  resolved path, so a folder reached through a symlink is one project.
- **`down --all`** tears down every tailsuite run on the Docker Engine,
  any project's. This folder's state goes only if its run did.
- **`lab.yaml`'s `up:` section** holds `detach` and `clean` defaults; a
  flag, `--detach=false` included, always wins.
- **The lab state records the holder** (`holder`, its process ID, for
  reading only) and `ready`.

## Why

- **A detached `up` behaves like a foreground one**, so the UI (#11) is
  served either way, and `down` is the one way to end a lab that isn't in
  a terminal.
- **Labels, not the state file, find leftovers**, so a lost state file or
  a crashed `up` can't hide one.
- **`--clean` stays inside the project**: another project's lab may be in
  use. `down --all` says plainly that it isn't.

## Evidence (checked 2026-10-26)

- `cmd/tailsuite/integration_test.go`, on a real headscale lab:
  `up --detach --clean` removes this project's leftover and not another
  project's, returns with the holder running and the lab ready; `test`
  uses that lab; `down` ends it and the holder exits; `up` with
  `up: { detach: true }` in `lab.yaml` detaches; a second
  `up --detach --clean` over a detached lab replaces it, and prints only
  its own output. 33 seconds on its own.

## Given up

- **A detached lab ends up to 5 seconds after `down`** as far as its
  holder knows; the lab itself is gone at once.
- **On Windows, Ctrl-C while a detached `up` is starting kills the
  holder**, which can't tear down; `down` removes what's left.
- **Labs started before this change carry no project label**; `--clean`
  finds them only through the state file.
