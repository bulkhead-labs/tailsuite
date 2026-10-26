# 0028: Reviews are sized to the PR

Status: **accepted** (2026-10-26). Amends
[0023](0023-subagents-on-opus.md): reviews run at `high`, or `xhigh` for
critical paths, never `max`.

## Context

- 0023 put every review at `max`, and the `pr-review` skill ran a fresh
  round after any substantial fix.
- **On 2026-10-26 that cost about 1.3 million subagent tokens for five
  PRs.** #65, a 56-line docs change about merging, took three `max`
  rounds and 618k tokens, and grew to 257 lines as each round found edge
  cases. #69's first round, on 3,400 lines of engine code, took 359k; its
  later two, at `high` and scoped to the fix commits, took 101k and 69k,
  and still found a real bug.
- The maintainer asked for reviews sized to each PR's importance, never
  at `max`, and for no reading of lock files or generated code.

## Decision

- **Three tiers**, chosen before the first round by what a miss would
  cost: light (one round at `high`), standard (two at `high`), critical
  (three at `xhigh`). The `pr-review` skill has the table.
- **Each round gets a token budget**, in its prompt, and stops at it,
  listing what it didn't check.
- **Only a blocking finding earns another round**, scoped to the fix
  commits. Should-fix findings are fixed and checked by the author.
- **Findings that widen the PR become issues.**
- **The reviewer doesn't read** lock files, `go.sum`, `vendor/`,
  generated code, fuzz corpora, binaries or minified assets; it checks
  what changed in them by hash or size.

## Why

- **The cost of a miss differs by orders of magnitude.** A wrong word in
  a process doc is fixed in the next PR. A false PASS in the lowering or
  the runner, or a leaked secret in a workflow, is the failure 0001 and
  0013 exist to prevent.
- **Scoped later rounds still catch what fixes break**, as #69's second
  round did, at a fraction of the cost.

## Given up

- **Light PRs get one look.** Their remaining edge cases surface later,
  in use, or in post-merge.
- **A budget can stop a review short.** What it didn't check is listed,
  so the maintainer knows.
