# 0024: Pull requests merge with merge commits, not squash

Status: **accepted** (2026-10-26). Settles #64. Changes the merge step in
[0008](0008-tests-and-pr-flow.md)'s flow, as `docs/process.md` describes
it, and CLAUDE.md rules 6 and 7. Amends
[0006](0006-apache-2-dco-open-core.md) (the merge commit GitHub makes when
a PR merges isn't signed off) and [0018](0018-go-toolchain-flake-ci.md)
(on `main`, CI runs once per push, on the merge commit).

## Context

- **Every PR was squash-merged** until now: one commit on `main` per PR,
  titled with the PR, with the branch's messages, sign-offs included, as
  its body.
- **The maintainer's approval doesn't survive a change.** The `main`
  ruleset dismisses an approval on every push
  (`dismiss_stale_reviews_on_push`), and requires the last push to be
  approved by someone other than its pusher (`require_last_push_approval`).
  A base change dismisses it too, GitHub's automatic retarget after a
  merge included.
- **M1's first PRs were a stack** (#55, #60, #51, #62), each built on the
  one below. A squash gives the lower PR's commits new hashes on `main`, so
  every branch above it no longer contains them. Wherever two PRs touched
  neighbouring lines (the decision index, the plan's M1 list, the
  architecture's status list), the next one conflicted, and had to be
  rebased and pushed, which dismissed its approval.
- **Merging that stack took four approval rounds and five rebases**
  (2026-10-26, #64).
- **The maintainer chose merge commits** (2026-10-26, #64): "I'm good with
  merge commits ... IDC about the history."

## Decision

- **PRs merge with a merge commit.** Squash and rebase merges are off in
  the repo's settings, which are the maintainer's
  ([0013](0013-ci-trust-model.md)).
- **Every commit on a branch reaches `main` as it is.** So each one:
  - is signed off (DCO, [0006](0006-apache-2-dco-open-core.md));
  - says what it does, and is fit for a public repo;
  - is reworded before review, never after approval, since a force-push
    dismisses it.
- **The merge commit's title is the PR's title and number**, and its body
  the PR's description. Whoever merges passes both; through the API,
  `commit_title` and `commit_message`. The repo's defaults for web merges
  are the maintainer's to set (today GitHub's "Merge pull request #n
  from …").
- **Only the merge commit GitHub makes when a PR merges is exempt from sign-off.** It
  adds no content. A merge on a branch is signed off like any other commit
  (`git merge --signoff`), since one that resolves conflicts does add
  content.
- **Stacked PRs:**
  - every base is `main`; never another PR's branch;
  - each branch is built on the branch below it;
  - the body says "Builds on #n", and reviewers read only the commits
    above that branch;
  - they merge bottom up, and each merges cleanly once the one below has,
    with no push, so its approval holds;
  - nothing in GitHub enforces the order, or stops a PR above bringing
    old commits of a lower branch that was rewritten after it was built.
    So before a stacked PR merges, `git log origin/main..origin/<branch>`
    must list only its own commits;
  - a lower PR is fixed with a new commit, which leaves the ranges above
    it as they were. Rewriting a lower branch means rebuilding every
    branch above it.
- **To undo a PR, revert its merge commit:** `git revert -m 1 <merge>`.
- **Commit identities are checked before review:** `pr-ready` lists every
  commit's author and committer address, and only `noreply` addresses or
  a contributor's own sign-off address pass. `post-merge` checks the
  merge commit's author.

## Why

- **An approval holds across a stack.** A merge commit keeps the lower
  PR's commits with their hashes, so the branch above still contains them
  and merges with no rebase. A fix to a lower PR, made as a new commit,
  forces nothing above it; only a rewrite does.
- **Nothing is lost.** Each commit keeps its own message and sign-off,
  instead of all of them folded into one body.
- **One method, set in the repo**, so nobody picks per PR.

## Evidence (checked 2026-10-26)

- **The repo's settings** (`GET /repos/bulkhead-labs/tailsuite`): merge
  commits allowed, squash and rebase not, branches deleted on merge. The
  default merge commit title is GitHub's "Merge pull request #n from …"
  (`MERGE_MESSAGE`), with the PR title as its body (`PR_TITLE`).
- **The `main` ruleset** has no linear-history rule: deletion,
  non-fast-forward and pull request rules only. Its pull request rule
  allows all three methods, needs one code owner's approval, dismisses
  stale approvals on push, and needs the last push approved.
- **The stack's history**, in #64: four approval rounds and five rebases
  to merge four PRs.

## Alternatives

- **Squash, as before.** One commit per PR and a linear history, but every
  stack costs a rebase and a re-approval per PR above the first.
- **Squash, and wait for each PR to merge before building the next.** No
  conflicts, but each piece of M1 would wait a review cycle for the one
  below.
- **Rebase merges.** A linear history with every commit, but GitHub
  rewrites each commit's hash, so a stack conflicts just as with squash.
- **Base each stacked PR on the branch below it.** Its diff on GitHub
  shows only its own commits, but GitHub retargets it to `main` when that
  branch merges and is deleted, and the base change dismisses its
  approval.

## Given up

- **A linear history.** `main` has merge commits, and `git log` shows the
  branches' commits beside them. `git log --first-parent main` gives one
  line per PR.
- **Reverting a PR** takes `-m 1`, and reverting a revert, to re-land a
  PR, needs care.
- **Every commit message and identity is public and permanent.**
  Work-in-progress messages have to be reworded before review. Under
  squash, a branch commit's committer never reached `main` (GitHub
  committed every squash); now every author and committer does, and so
  does whoever merges, as the merge commit's author. A merge from the web
  UI uses the maintainer's web-commit address, which stays private only
  while "Keep my email addresses private" is on.
- **`git bisect` can land on a commit in the middle of a PR**, which may
  not build. `git bisect --first-parent` steps by PR.
- **On `main`, only the merge commit gets a CI run.** The branch's own
  commits ran on the PR only where each was a push's head. So [0018](0018-go-toolchain-flake-ci.md)'s
  "every commit on `main` keeps its run" now means every push to `main`.
- **Until the stack below merges, a stacked PR's GitHub diff** shows the
  lower PRs' commits too. Reviewers read the range above the branch below.
