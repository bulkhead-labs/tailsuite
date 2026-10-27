# CLAUDE.md

tailsuite: an open-source tool that rebuilds a tailnet in a local lab, on
real Tailscale clients, proves what it allows with real traffic, and
deploys changes through OpenTofu.

Read [`README.md`](README.md), [`docs/plan.md`](docs/plan.md),
[`docs/architecture.md`](docs/architecture.md) and
[`docs/process.md`](docs/process.md) before doing anything. The decisions
are in [`docs/decisions/`](docs/decisions/README.md).

## Rules

1. **This repo is public.** Never commit, or put in an issue, PR or comment:
   - real tailnet data (policies, device names, IPs, keys, user names, the
     maintainers' own included);
   - secrets;
   - names of companies or people whose networks were looked at;
   - anything about Bulkhead Labs' clients or business plans.

   Sample configs are synthetic.
2. **Truth over mocks** ([0001](docs/decisions/0001-truth-over-mocks.md)).
   Never make a result look like PASS or a deny when the lab can't prove it;
   use UNKNOWN or REFUSED. Every deny test gets a positive control.
3. **Never send traffic outside the lab, and never write to a real tailnet**
   except in `tailsuite deploy` behind its gates
   ([0007](docs/decisions/0007-read-only-by-default.md)). Tests touch only
   lab engines and the dedicated Tailscale test organization.
4. **All work is a GitHub issue** in this repo
   ([0010](docs/decisions/0010-public-tracking-in-issues.md)), with a
   `type:` and `area:` label, and on a milestone when it belongs to one.
5. **The docs stay true.** The PR that changes behaviour updates
   `docs/plan.md`, `docs/architecture.md`, the README and the decision
   records. A choice with trade-offs gets a new decision record, dated, with
   its evidence; versions, prices and vendor claims carry the date they were
   checked.
6. **Every PR:**
   - opens as a draft;
   - runs the `pr-ready` skill before it's marked ready (that skill runs
     `pr-review`, which starts the `pr-reviewer` agent);
   - is merged with a merge commit, only after the maintainer's approving
     review, and when they ask
     ([0024](docs/decisions/0024-merge-commits.md)). Every commit on the
     branch reaches `main`, so each one is signed off and written to be
     read;
   - is stacked on another PR only when the two would conflict
     ([0029](docs/decisions/0029-stacked-prs-need-reapproval.md)). A
     stacked PR has its base on `main` and its branch built on the one
     below, and needs the maintainer's approval again once that one
     merges. Before it merges, after a fetch,
     `git log origin/main..origin/<branch>` must list only its own
     commits: that proves the one below merged, and that it brings
     nothing its review didn't see.

   **After every merge, run the `post-merge` skill.**

   **Every subagent follows the `delegate` skill:** Opus always, with
   the effort set by the kind of work, highest for design. Reviews are
   sized to the PR: `high`, or `xhigh` for critical paths, never `max`
   ([0028](docs/decisions/0028-review-tiers.md)).
7. **Sign off every commit** (`git commit -s`, DCO), merges on a branch
   included (`git merge --signoff`). Only the merge commit GitHub makes
   when a PR merges is exempt: it adds no content.
8. **CI follows [0013](docs/decisions/0013-ci-trust-model.md).**
   - Every PR needs the maintainer's approving review.
   - Secrets live only in environments, never at the repo level:
     - `nightly` from `main`;
     - `release` from `v*` tags;
     - `branch-run`, which the maintainer approves each time.
   - **Workflows:**
     - every workflow declares least-privilege `permissions:`;
     - never use `pull_request_target` or `workflow_run`;
     - jobs with an environment run only on `schedule`, `workflow_dispatch`
       (no input that picks code) or a tag push, and no event text goes
       into `run:`;
     - `release.yml` has no `workflow_call` or `workflow_dispatch`;
     - only the release job publishes anything users get.
   - **The release job** restores no caches.
   - **Signatures are verified against the exact identity:**
     `release.yml@refs/tags/vX.Y.Z`, with repository, ref and trigger
     pinned; never "latest".
   - Never create a tag or a release by hand, and never approve another
     PR.
   - Never approve a fork's workflow run.
   - Never write secrets or variables, and never change rulesets,
     environments or Actions settings. They are the maintainer's.
   - Jobs with an environment never read `vars`.
9. **Pin everything**: Go modules, images by digest, GitHub Actions by commit
   SHA, vendored scripts by exact version with SRI. Pin a release only once
   it is two weeks old, unless a fix that applies to us can't wait (say so
   in the PR).

## Working here

- **Go.** `nix develop` for the toolchain; plain `go build` must also work.
- **Lab tests use Docker with unique names**, and always tear down.
- **The status page** is `docs/index.html` with `docs/assets/`, served by
  GitHub Pages from `docs/` on `main`. It renders the docs beside it and
  reads milestones live from GitHub. Don't copy plan text into it.

## Token economy ([0034](docs/decisions/0034-token-economy.md))

Every call re-reads the whole session, so cost grows with how long a
context is carried, not with how hard the model thinks. Keep it lean, and
never drop a check, a review round or a read the work needs to save
tokens.

- **One session per issue.** A session takes one issue to a ready PR
  (and its post-merge), then ends. The handoff lives where the work
  does: the PR body (state, review trail, what's left) and a comment on
  the issue when work stops part-way, both written to the public-repo
  rules. Then tell the maintainer to start a fresh session for the next
  issue. Past about 300k of context, write the handoff and say so.
- **Don't resume a big session after a long break.** Its cache has gone
  cold; a fresh session reading the handoff costs less.
- **Edit, don't rewrite.** Write a whole file only when it's new or
  restructured.
- **Short commands, small output.**
  - Absolute paths from the worktree, not `cd` chains; load the token in
    the same command as `gh`, as one line.
  - Quiet flags: `git -q`, `gh … --json` with only the fields needed,
    `--jq` to trim, `| tail` on `go test`, `nix` and `docker`. Rerun
    loudly only when a step fails.
  - Read files by range, or `grep -n` for the heading first, when only
    part is needed.
- **Subagents for bulky, separable work**, and only when it would add
  more than about 50k here, since each one starts at about 60k. Ask for
  a short reply; the details go in the PR or the issue.
