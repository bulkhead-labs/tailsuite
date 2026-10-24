---
name: pr-ready
description: Take a tailsuite pull request from draft to ready. Walk the definition of done, make the docs true, check the public-repo safety rules, then start the independent pr-reviewer agent and address its findings. Use when asked to "get this PR ready", "mark ready", "pr-ready", "finish the PR", or before marking any tailsuite PR ready for review.
---

# pr-ready

A PR is ready when a stranger could merge it and the repo would still tell
the truth. Work through these steps in order and don't skip any. Report
what each step found.

## 1. Find the PR and its issue

- `gh pr view --json number,title,body,isDraft,files,headRefName,closingIssuesReferences`
- It must be a draft linked to an issue (`Closes #n`). If not, fix the body,
  or stop and ask.
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

Anything found blocks the PR. Fix it and say so.

## 5. Independent review

Start the `pr-reviewer` agent with the PR number. It is read-only and
reviews against the same checklist without seeing your conclusions. Wait
for it.

- Fix every finding it marks **blocking**. For anything else, fix it or
  answer it in the PR.
- Re-run the reviewer if the fixes were substantial.
- Link its comment in the template's Review box.

## 6. Mark ready

`gh pr ready <n>`. The heavy test tiers start. Report:

- the checklist result;
- the doc changes made;
- the reviewer's findings and what happened to each;
- the CI run link.

Merging is the maintainer's call. Don't merge unless asked.
