---
name: pr-ready
description: Take a tailsuite pull request from draft to ready. Walk the definition of done, make the docs true, check the public-repo safety rules, then run the pr-review skill (the independent pr-reviewer agent, round after round) and address its findings. Use when asked to "get this PR ready", "mark ready", "pr-ready", "finish the PR", or before marking any tailsuite PR ready for review.
---

# pr-ready

A PR is ready when a stranger could merge it and the repo would still tell
the truth. Work through these steps in order and don't skip any. Report
what each step found.

## 1. Find the PR and its issue

- `gh pr view <n> --json number,title,body,isDraft,files,headRefName`
- The issues it closes (works on any `gh` version):
  `gh api graphql -f query='query{repository(owner:"bulkhead-labs",name:"tailsuite"){pullRequest(number:<n>){closingIssuesReferences(first:20){nodes{number title state}}}}}'`
- It must be a draft linked to an issue (`Closes #n`). If not, fix the body,
  or stop and ask. If `gh pr edit` fails on an older `gh` (GitHub retired
  classic Projects), use
  `gh api -X PATCH repos/bulkhead-labs/tailsuite/pulls/<n> -F body=@<file>`.
- **Stacked on another PR?** Only when the two would conflict
  ([0029](../../../docs/decisions/0029-stacked-prs-need-reapproval.md));
  otherwise rebuild it on `main` once the one below merges. Its base must
  be `main`, its branch built on the branch below, and its body must say
  "Builds on #n"
  ([process](../../../docs/process.md#stacked-pull-requests)).
  After `git fetch origin`, `git log --oneline origin/<branch below>..HEAD`
  must list only its own commits; check those in the steps below.
- Read the issue's "Done when" list. Every item is either done in this PR
  or moved to a new issue (linked in the PR).

## 2. Walk the definition of done

From `docs/plan.md#definition-of-done`. For each item, find the **evidence
in the diff**. The checkbox alone isn't evidence.

| Item | Evidence to look for |
|---|---|
| Lab, both engines | Integration tests run against headscale **and** Tailscale (or a REFUSED capability declared, with the reason) |
| Tests | New or changed `_test.go`; a journey step in the user-journey suite if users touch it |
| UI | A change under the UI source, or N/A with a reason |
| OpenTofu | A change to the module or templates; the round-trip test still covers it |
| Release | It builds into the binary; nothing needs a clone to work |
| Docs | Next step |
| Decision | Next step |

Tick the template's boxes only where the evidence exists. Write N/A plus
the reason for the rest.

## 3. Make the docs true

Read these against the diff, and edit them in this PR:

- **`docs/plan.md`**: the milestone status, what ships, the tiers, and the
  "Last checked" date. Bump it to today when you checked.
- **`docs/architecture.md`**: layers, interfaces, commands, the project
  layout.
- **`README.md`**: the status line, and the commands shown.
- **`docs/decisions/`**: a new record for any choice with trade-offs
  (context, decision, evidence with dates, what's given up), added to
  `docs/decisions/README.md`. Supersede old records, never rewrite them.
- **A settled design question**: close its `type:design` issue from the PR
  (`Closes #n`), and point it at the record.

## 4. Public-repo safety

Search the diff and the PR text:

- **Real tailnet data**: `100\.` and `fd7a:` addresses that aren't obviously
  synthetic, `tskey-`, `.ts.net` names, email addresses, policy files that
  aren't marked synthetic.
- **Secrets, and names of companies or people.**
- **Sign-offs**: every commit has `Signed-off-by` (`git log --format=%B
  origin/main..HEAD`).
- **Commits reach `main` as they are**: PRs merge with merge commits
  ([0024](../../../docs/decisions/0024-merge-commits.md)), and `main`'s
  ruleset forbids rewriting it, so each commit's text and identities are
  public for good.
  - **Messages:** nothing in one the public-repo rules forbid (that
    blocks). Each should say what it does, with no "wip" or "fix review"
    alone.
  - **Addresses:** `git log --format='%ae%n%ce' origin/main..HEAD | sort -u`
    lists only `noreply` addresses, or a contributor's own sign-off
    address. Anything else blocks.

  Reword before review, not after approval: a force-push dismisses it.
  Rewording a PR with others stacked on it means rebuilding each of them
  ([process](../../../docs/process.md#stacked-pull-requests)).

Anything else found blocks the PR. Fix it and say so.

## 5. Independent review

Run the [`pr-review`](../pr-review/SKILL.md) skill. It starts the
`pr-reviewer` agent on Opus, at the effort and budget of the PR's
review tier (light, standard or critical), as the
[`delegate`](../delegate/SKILL.md) skill says for review. The agent is read-only and
reviews against the same checklist without seeing your conclusions. It
runs another round only for a blocking finding, within the PR's tier,
and links every round in the template's Review box. Don't mark the PR ready until a round finds
nothing blocking.

## 6. Mark ready

First, the `ci` workflow (lint and unit) must have passed on the PR's head
commit:
`gh run list --branch <branch> --workflow ci --json headSha,conclusion,url`.
A red or missing run blocks marking it ready.

Then `gh pr ready <n>`. Once #13 exists, the heavy test tiers start here.
Report:

- the checklist result;
- the doc changes made;
- the reviewer's findings and what happened to each;
- the CI run link (and, once #13 exists, the heavy tiers' runs).

When the PR is the last of a group of PRs that deliver one piece
together to be ready,
build the group's review page with the `review-artifact` skill, and give
its link in the report. A PR outside any group gets no page.

Merging needs the maintainer's approving review (decision 0013), and their
say-so. Don't merge unless asked.
