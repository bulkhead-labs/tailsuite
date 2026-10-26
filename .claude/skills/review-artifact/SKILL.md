---
name: review-artifact
description: Build a review page for the maintainer covering one pull request or a group of them. It says what changed, how it maps to the issue's scope, what it delivers, what it sets up, and what it changes in the plan. Use when asked for a "review artifact", a "review page", "something to review in the morning", a "PR summary page", or when one or more PRs are ready and the maintainer will review them later.
---

# review-artifact

The maintainer reviews PRs asynchronously, often on a phone, often hours
later. The PR body is written for the merge. This page is written for the
person deciding whether to merge: it answers "what did we get, is it what
we asked for, and what does it change" without reading the diff first.

One page covers one PR or a group (a stack, or PRs that deliver one piece
together). Group PRs when the maintainer would merge them in one sitting
or one decision; otherwise make one page per PR.

The page is a claude.ai artifact: private to the maintainer, outside the
repo. **Don't link it from a PR, an issue or any file in this repo.**
Others can't open it, and the repo is public. Give the link in chat.

## 1. Gather the facts

For each PR, at its current head. Run `git fetch origin` first and use
`origin/` refs throughout: a local branch, a stacked base especially, can
be stale and give the wrong diff.

- `gh pr view <n> --json number,title,body,isDraft,baseRefName,headRefName,headRefOid,additions,deletions,changedFiles`
- The issues it closes (the graphql query in `pr-ready` step 1), and
  each issue's **"Done when"** list: the scope the work is judged against.
- `git diff --stat origin/<base>...origin/<head>` and
  `git log --format='%h %s' origin/<base>..origin/<head>`. The totals must
  match `gh pr view`'s `additions`, `deletions` and `changedFiles`; if they
  don't, a ref is stale.
- Changes to `docs/plan.md`, `docs/architecture.md`, `README.md` and
  `docs/decisions/`: `git diff origin/<base>...origin/<head> -- docs README.md`.
- Each `pr-reviewer` comment, its verdict, its findings, and what was done
  about each:
  `gh pr view <n> --json comments --jq '.comments[]|select(.body|startswith("## pr-reviewer"))|.url'`.
- CI on the head: `gh run list --branch <branch> -L 1`.
- Issues the work filed, or whose bodies it changed.
- For a group: the base of each PR, and the order they must merge in.

Every fact on the page must come from one of these. If a claim can't be
traced to a file, a commit, a comment or an issue, leave it out.

## 2. Write the page

Start from [`template.html`](template.html) in this folder. It has the
layout and theme every tailsuite review page uses, so pages look alike and
read alike. Copy everything above `<div class="wrap">` unchanged except the
`<title>`, and write the body from the template's sections.

The page file lives **outside the repo**, in the session's scratchpad, and
is never committed. Within a session, republishing the same path updates
the same page. A later session (a fix round, `post-merge`) finds the page
by its title with the Artifact tool's `list`, `read`s it, edits that copy,
and publishes with the page's `url`, so the link stays the same.

Fill the sections in this order. Leave none out; when one has
nothing to say, say that in one line ("No plan changes.").

1. **Header.** An eyebrow line: `tailsuite · review · PR #n (+ #m) ·
   checked <date> at <short SHA>`. The `<h1>` names what was built, in the
   maintainer's terms. One lede paragraph says what it delivers and the
   verdict.
2. **Verdict box.** Ready or not, the review rounds, CI. What to do: merge
   order, and anything waiting on the maintainer. Below it, a table of
   every open PR the merge order touches (each one's base, when it merges,
   its state), even for a one-PR page, since stacked PRs merge together.
3. **Scope.** The issue's "Done when" items, one row each: the item, what
   delivers it (file, test or doc), and a chip: `done`, `partly` or
   `moved to #n`. Then anything delivered **beyond** the scope, and why.
   This is the section the maintainer checks the work against.
4. **What changed.** By area, in plain words: what each part does and the
   files that hold it, with line counts. Code blocks only where the shape
   matters (a type, a file format, a command).
5. **Choices made.** The implementation choices with trade-offs (each in
   a decision record), what was given up, and the alternative. Link the
   record on the branch.
6. **What it sets up.** Each downstream issue it unblocks, and exactly
   what that issue will use from this work. For a group, how the PRs fit
   together.
7. **Plan changes.** Everything that changes the plan of record:
   - new issues, and where they sit in the milestone order;
   - work moved between milestones, narrowed or deferred;
   - decisions added, amended or superseded;
   - prerequisites, dates or pins that moved;
   - user-visible behaviour the plan didn't state (a stricter file
     format, a new REFUSED case).

   Say plainly which of these need the maintainer's agreement.
8. **Review trail.** Each reviewer round: verdict, the findings, and how
   each was fixed or answered, linked to the PR comment. Then the tests:
   what ran, where, with numbers.
9. **Risks and what to watch.** What could still be wrong, and what would
   show it.
10. **Reading guide.** If the maintainer reads the diff: which files, in
    which order, and what to check in each. Link each file on the branch.
11. **Also waiting on you** (only if there's something): open questions
    from the same body of work that aren't about these PRs, each with a
    recommendation, in one box at the end.
12. **Questions** (only if there are any). Ask the maintainer only
    product questions: what users get, how they use it, the overall
    design. Settle implementation yourself, and record the choice. Give
    each question a recommendation and what it changes. If the maintainer
    should answer on the page, load the `artifact-capabilities` skill and
    give the page a store for answers; otherwise answers come in chat.

## 3. Rules

- **Public-repo rules still apply.** The page describes a public repo:
  synthetic data only, no secrets, no names of people or companies whose
  networks were looked at.
- **Write for the maintainer.** A platform and security engineer: skip the
  basics, give numbers, say what was given up. Plain sentences; no
  marketing.
- **Link everything checkable.** PRs, issues, review comments, decision
  records, and files at the PR's branch
  (`https://github.com/bulkhead-labs/tailsuite/blob/<branch>/<path>`).
- **Date it.** The eyebrow carries the date and the head SHA the page was
  checked at.
- **Title**: two to four words naming the work, ending in "Review", such
  as "Tailnet Model Build Review". Keep it across updates; it's how a later
  session finds the page (the Artifact tool's `list`).
- **What's settled goes in the repo.** The page and chat are private. An
  answer, or anything else the maintainer settles there, is written into
  the repo before it's acted on: in a PR or issue comment, an issue's
  "Done when" list, or a decision record
  ([0010](../../../docs/decisions/0010-public-tracking-in-issues.md)).
  The page summarises the repo; it's never the only record.
- **Check before you publish.** Every number (lines, inputs, rounds), SHA
  and issue number on the page against the facts from step 1. A wrong
  claim on a review page costs more than a missing one.

## 4. Keep it current

- **When the PR changes** (fixes, another review round), republish the same
  page, with the new SHA in the eyebrow.
- **After the merge**, as part of [`post-merge`](../post-merge/SKILL.md): add an outcome box at the
  top ("Merged <date> as <SHA>", what changed in review, follow-ups
  filed), and repoint branch links to the merge commit
  (`blob/<merge SHA>/<path>`), so the page keeps showing what merged.
- **When the maintainer leaves comments or answers on the page**, record
  them in the repo (see Rules), act on them, then update the page to say
  what was done.
