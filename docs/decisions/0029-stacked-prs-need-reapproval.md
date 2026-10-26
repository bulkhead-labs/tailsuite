# 0029: A stacked pull request needs a fresh approval once the one below merges

Status: **accepted** (2026-10-26). Amends
[0024](0024-merge-commits.md)'s "Stacked PRs". Settles the rest of #64.

## Context

- 0024 said a stacked PR "merges cleanly once the one below has, with no
  push, so its approval holds".
- **That was false on this ruleset.** Merging #65 dismissed the approval
  on #69, stacked on it: "The merge-base changed after approval." The
  maintainer then turned off `dismiss_stale_reviews_on_push` in the
  `main` ruleset, and kept `require_last_push_approval`.
- **Merging the next stack** (#69, #70, #71, #73, on 2026-10-26) showed
  what still happens:
  - approvals are no longer dismissed, but read as stale: "Review is
    stale because it was submitted before the merge base changed";
  - GitHub didn't re-sync the stacked PRs' base (`baseRefOid` stayed at
    the old `main`), so approvals given after the merge read as stale
    too;
  - the maintainer merged #69–#73 from the web.

## Decision

- **Don't stack unless two PRs would conflict.** Build the next piece on
  `main` once the one it needs has merged, or, when it doesn't need it,
  in parallel on `main`.
- **When stacking is worth it**, 0024's mechanics stand: every base is
  `main`, each branch is built on the one below, the body says "Builds on
  #n", they merge bottom up, and `git log origin/main..origin/<branch>`
  must list only the PR's own commits before it merges.
- **But each stacked PR needs the maintainer's approval again** after the
  one below merges. The approval it had is stale. Ask for it in the
  post-merge report.

## Why

- **Stacking still saves rebases.** Merge commits keep the lower PR's
  commits with their hashes, so the branch above merges without
  conflicts.
- **It no longer saves approvals**, and each re-approval is a round trip
  to the maintainer. Unstacked PRs cost one approval each, once.

## Evidence (checked 2026-10-26)

- **The `main` ruleset** (`GET /repos/bulkhead-labs/tailsuite/rulesets`):
  `dismiss_stale_reviews_on_push` off, `require_last_push_approval` on,
  one code owner's approval required.
- **#64's comments** record the dismissal on #69 and the stale approvals
  on #69–#73.

## Given up

- **A stack merges no faster than one PR at a time**: each needs its own
  approval after the one below.
- **Parallel work waits** when the next piece needs the one before it,
  instead of being reviewed on top of it.
