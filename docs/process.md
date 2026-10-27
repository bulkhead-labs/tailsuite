# How we work

Development happens in the open. Anyone can follow along, and weigh in on
the design questions.

## Where work lives

- **Every piece of work is a GitHub issue**, with one `type:` label
  (feature, bug, design, chore, spike), one or more `area:` labels, and a
  milestone when it belongs to one.
- **Milestones** M1–M7 are the plan's milestones ([plan](plan.md)).
- **Open design questions** are `type:design` issues. The design form files
  them as `needs-triage`; a maintainer adds `type:design`, which puts them
  on the status page. A settled question
  becomes a decision record in [`decisions/`](decisions/README.md), and the
  issue closes with a link to it.
- **Research and measurements**, dated, go in [`research/`](research/).
- **One agent session per issue**: it ends with the PR ready, or merged
  and post-merge done, and hands off through the PR body and the issue
  ([0034](decisions/0034-token-economy.md)).

## The docs stay true

[`plan.md`](plan.md), [`architecture.md`](architecture.md), the
[README](../README.md) and the decision records describe what is true now,
not what was intended. Three things keep them that way:

1. **The PR that changes the behaviour changes the docs.**
2. **The `pr-ready` checklist** checks the docs before review.
3. **The `post-merge` check** compares the docs with `main`, the issues and
   the milestones after every merge, and fixes what drifted in a docs-only
   PR.

The [status page](https://bulkhead-labs.github.io/tailsuite/)
(`docs/index.html`, served by GitHub Pages from `docs/` on `main`)
renders `plan.md` and the decision index, and reads milestone progress
and design questions live from GitHub, so it can't drift on its own.

## A pull request, start to finish

Lint and unit tests run on every push to a pull request
([0018](decisions/0018-go-toolchain-flake-ci.md)). The heavy tiers in steps
2 and 4 arrive with #13. Until then, the `pr-ready` checklist and the
reviewer agent are those checks.

1. **Open it as a draft**, linked to its issue (`Closes #n`), using the
   [template](../.github/pull_request_template.md). Drafts run lint and unit
   tests only.
2. **Need the heavy tests early?** Run the journey workflow by hand on the
   branch (`workflow_dispatch`).
3. **Before marking it ready, run the `pr-ready` skill**
   (`.claude/skills/pr-ready`). It walks the
   [definition of done](plan.md#definition-of-done) and updates the docs,
   then runs the **`pr-review` skill**, which starts the **`pr-reviewer`
   agent** round after round until it finds nothing blocking. The agent is
   a separate, read-only reviewer: it checks the PR against the checklist
   and the docs against the code, and posts its findings on the PR. Like
   every subagent, it runs as the **`delegate` skill** says: on Opus, with
   the highest effort for design, and for review the effort of the PR's
   tier: `high`, or `xhigh` for critical paths
   ([0028](decisions/0028-review-tiers.md)).
4. **Fix the findings, then mark it ready.** The integration and
   user-journey tiers run, and must pass.
   When the last PR of a group is ready, the **`review-artifact` skill**
   builds one review page for a group of PRs that deliver one piece
   together, once all are ready: the scope against the issues, what it
   sets up, and what it changes in the plan. The page is
   private to the maintainer, so it's never linked from the repo, and
   anything settled on it is written into the repo before it's acted on.
5. **The maintainer approves, then it's merged with a merge commit**
   ([0024](decisions/0024-merge-commits.md)). The branch's commits reach
   `main` as they are, each with its `Signed-off-by`, so each one is
   written to be read. Whoever merges gives the merge commit the PR's
   title and number as its title, and the PR's description as its body
   (through the API, `commit_title` and `commit_message`). The
   branch is deleted. To undo a PR, revert its merge commit
   (`git revert -m 1 <merge>`).
6. **After the merge, run the `post-merge` skill**
   (`.claude/skills/post-merge`). It checks that the status page deployed,
   that the milestone and issue state matches `plan.md`, and that nothing
   in the docs went stale. Any fix goes in a docs-only PR.

### Stacked pull requests

**Avoid stacking** unless two PRs would conflict
([0029](decisions/0029-stacked-prs-need-reapproval.md)). A stack saves
rebases, not approvals: each stacked PR needs the maintainer's approval
again once the one below merges. Otherwise, wait for the one below to
merge and build on `main`. When one PR must build on another that hasn't
merged yet:

- **Every PR's base is `main`.** Never base a PR on another PR's branch.
  GitHub retargets such a PR when the one below merges, and a base change
  makes the maintainer's approval stale.
- **Each branch is built on the branch below it**, so its commits sit on
  top of that branch's.
- **The PR body says "Builds on #n"**, and its own commits are
  `origin/<branch below>..origin/<branch>`. Until the one below merges,
  GitHub's diff shows both PRs' commits; reviewers read only that range.
- **They merge bottom up, and what each brings is checked, not
  assumed.** GitHub would merge an approved PR whose base is `main` while
  the one below is open, and bring the lower PR's commits in under an
  approval that read only its own. It would also bring old commits of a
  lower branch that was rewritten after this one was built. So, after a
  fetch, `git log --oneline origin/main..origin/<branch>` must list only
  the PR's own commits before it merges. Then, unless a fix below
  conflicts with it (next point), it merges cleanly with no push.
- **Each one needs a fresh approval after the one below merges.** The
  `main` ruleset requires the last push to be approved, and GitHub reads
  an approval given before the merge base changed as stale, even with
  no push. GitHub doesn't re-sync the stacked PR's base either, so an
  approval given right after the merge can still read as stale; the
  maintainer may need to merge it from the web.
- **Fix a lower PR with a new commit.** The PRs above keep the same
  range, and merge with no push once it has, unless the fix conflicts
  with one of them. git counts edits to neighbouring lines as a conflict,
  as in the decision index, the plan's M1 list or the architecture's
  status list; post-merge's `mergeable` check finds it. That PR then needs
  a push and a fresh approval: fix it with a signed-off merge of the
  branch below (`git merge --signoff`), which keeps the ranges above it,
  never a rebase. A rewrite of a lower branch (a reword, an amend, a
  rebase) means rebuilding every branch above it, and a push to each,
  which makes their approvals stale; rewrite only before the maintainer
  reviews the stack.

Humans can do every step by hand. The skills exist so agents and people
follow the same checklist.

## CI and secrets

Agents may write workflows and code. Nothing merges, reaches a secret or
ships without the maintainer:

- **Every PR needs the maintainer's approving review.** CODEOWNERS names
  them for every path, so no one else's approval counts, and new pushes
  dismiss it.
- **Secrets live only in GitHub environments:**
  - `nightly` runs from `main`;
  - `release` runs from `v*` tags and needs the maintainer's approval;
  - `branch-run` serves any branch, and the maintainer approves every run,
    after reading the run's own commit (`headSha`) against `main`,
    workflows first.
- **PR tiers run on headscale**, with no secrets.
- **A branch workflow can raise its `GITHUB_TOKEN`.** A release is trusted
  because of its tag, immutability and signature, never because of a
  token's scope.
- **Every tag is the maintainer's.** Releases are immutable once
  published, and signatures are verified against the exact release-workflow
  identity.

See [decision 0013](decisions/0013-ci-trust-model.md).

## Decisions

A choice with trade-offs gets a record in
[`decisions/`](decisions/README.md): the context, the decision, the
evidence, and what is given up. Prices, versions and vendor claims carry
the date they were checked. A decision is changed by a new record that
supersedes it, never by silently editing the old one.

## What never goes in this repo

This repo is public.

- **No real tailnet data**: no policies, device names, IPs, keys or user
  names from any real tailnet, the maintainers' own included. Sample
  configs are synthetic.
- **No secrets.** CI credentials live only in GitHub environments
  ([0013](decisions/0013-ci-trust-model.md)).
- **No names of companies or people** whose networks were looked at.

## Sign-off

Every commit is signed off under the
[Developer Certificate of Origin](https://developercertificate.org/)
(`git commit -s`). See [CONTRIBUTING.md](../CONTRIBUTING.md). A merge
commit on a branch is signed off like any other (`git merge --signoff`).
Only the merge commit GitHub makes when a PR merges is exempt: it adds no
content.
