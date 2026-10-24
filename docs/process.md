# How we work

Development happens in the open. Anyone can follow along, and weigh in on
the design questions.

## Where work lives

- **Every piece of work is a GitHub issue**, with one `type:` label
  (feature, bug, design, chore, spike), one or more `area:` labels, and a
  milestone when it belongs to one.
- **Milestones** M1–M7 are the plan's milestones ([plan](plan.md)).
- **Open design questions** are `type:design` issues. A settled question
  becomes a decision record in [`decisions/`](decisions/README.md), and the
  issue closes with a link to it.
- **Research and measurements**, dated, go in [`research/`](research/).

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
renders these files and reads milestone progress live from GitHub, so
it can't drift on its own.

## A pull request, start to finish

CI arrives with #1 and #13. Until then, the test runs in steps 1, 2 and 4
don't exist yet, and the `pr-ready` checklist and the reviewer agent are
the checks.

1. **Open it as a draft**, linked to its issue (`Closes #n`), using the
   [template](../.github/pull_request_template.md). Drafts run lint and unit
   tests only.
2. **Need the heavy tests early?** Run the journey workflow by hand on the
   branch (`workflow_dispatch`).
3. **Before marking it ready, run the `pr-ready` skill**
   (`.claude/skills/pr-ready`). It walks the
   [definition of done](plan.md#definition-of-done) and updates the docs,
   then starts the **`pr-reviewer` agent**. The agent is a separate,
   read-only reviewer: it checks the PR against the checklist and the docs
   against the code, and posts its findings on the PR.
4. **Fix the findings, then mark it ready.** The integration and
   user-journey tiers run, and must pass.
5. **Squash-merge.** The PR title becomes the commit title. The branch is
   deleted.
6. **After the merge, run the `post-merge` skill**
   (`.claude/skills/post-merge`). It checks that the status page deployed,
   that the milestone and issue state matches `plan.md`, and that nothing
   in the docs went stale. Any fix goes in a docs-only PR.

Humans can do every step by hand. The skills exist so agents and people
follow the same checklist.

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
- **No secrets.** CI credentials live in GitHub secrets for the test
  organization only.
- **No names of companies or people** whose networks were looked at.

## Sign-off

Every commit is signed off under the
[Developer Certificate of Origin](https://developercertificate.org/)
(`git commit -s`). See [CONTRIBUTING.md](../CONTRIBUTING.md).
