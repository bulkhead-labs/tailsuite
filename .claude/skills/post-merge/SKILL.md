---
name: post-merge
description: After a tailsuite pull request merges, check that main, the issues, the milestones, the docs and the status page all agree, and fix any drift in a docs-only PR. Use when asked to "post-merge", "sync the docs", "check the plan is current", after any merge to main, or at the start of a session to confirm the plan is true.
---

# post-merge

The plan, the architecture, the README, the decisions, the milestones and
the status page must agree with `main` and with GitHub after every merge.
This skill finds where they don't, and fixes it.

## 0. Pages first

If Pages was pointed at the PR branch for a preview, the merge just
deleted that branch. Point it back before anything else:

- `gh api repos/bulkhead-labs/tailsuite/pages --jq .source`
- `gh api -X PUT repos/bulkhead-labs/tailsuite/pages -f build_type=legacy -f 'source[branch]=main' -f 'source[path]=/docs'`

## 1. What merged

- `git fetch origin main && git log --oneline -5 origin/main`
- `gh pr view <n> --json title,body,mergedAt`, and the issues it closed:
  `gh api graphql -f query='query{repository(owner:"bulkhead-labs",name:"tailsuite"){pullRequest(number:<n>){closingIssuesReferences(first:20){nodes{number title state}}}}}'`
- The issues it closed must be closed. If one was only partly done, reopen
  it, or file the rest as a new issue on the same milestone.

## 2. Milestones vs the plan

- `gh api repos/bulkhead-labs/tailsuite/milestones?state=all --jq '.[] | [.title, .open_issues, .closed_issues, .state] | @tsv'`
- **Each milestone's Status in `docs/plan.md`:**
  - "Not started" if no issue in it is closed and no work is merged;
  - "In progress" otherwise;
  - "Done" only when every issue is closed **and** the user-journey suite
    passed on a release built from it.

  When a milestone is Done, close the GitHub milestone too.
- **"Current milestone"** at the top of `plan.md` is the lowest one that
  isn't Done.

## 3. The docs vs main

Read each against the code on `main`:

- **`README.md`**: the status line; are the commands shown real yet, or
  marked planned?
- **`docs/architecture.md`**: do the layers, interfaces, commands and the
  project layout match the code?
- **`docs/decisions/README.md`**: every record listed; statuses right.
- **Open design questions**: every settled one has a record, and its issue
  is closed.
- **Dates**: bump `plan.md`'s "Last checked" to today.

## 4. The status page

- Pages points at `main` (step 0 did that); check the build:
- `gh api repos/bulkhead-labs/tailsuite/pages/builds/latest --jq '[.status, .commit] | @tsv'`:
  it must be `built` for the merge commit (Pages serves `docs/` from `main`).
- Fetch https://bulkhead-labs.github.io/tailsuite/. Check that it loads and
  shows the milestones and the plan.

## 5. Fix the drift

- **Nothing drifted:** say so, with what was checked.
- **Otherwise:**
  - open a **docs-only** draft PR named `docs: sync after #<n>` with every
    fix;
  - run `pr-ready` on it. It needs the maintainer's approval like any PR;
  - report each drift found and its fix.

Never change behaviour in this PR.

## 6. The review page

If the PR had a review page (the `review-artifact` skill), update it to
the outcome, as that skill's "Keep it current" says: an outcome box at the
top, and its links repointed from the branch to `main`.

## 7. Report

A short list:

- what merged;
- issue and milestone state;
- what was checked;
- the drift found and fixed, with the docs PR link;
- the review page, if there was one, and that it shows the outcome;
- the status page's state.
