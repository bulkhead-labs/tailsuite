---
name: review-artifact
description: Build a review page for the maintainer covering a group of pull requests that deliver one piece of the product, once every PR in it is ready. It says what changed, how it maps to the issue's scope, what it delivers, what it sets up, and what it changes in the plan. Use when asked for a "review artifact", a "review page", "something to review in the morning", a "PR summary page", or when every PR in a group is ready.
---

# review-artifact

The maintainer reviews PRs asynchronously, often on a phone, often hours
later. The PR body is written for the merge. This page is written for the
person deciding whether to merge: it answers "what did we get, is it what
we asked for, and what does it change" without reading the diff first.

**One page per group, never per PR** (the maintainer, 2026-10-26: "1
per grouping when its ready so I can easily see the changes as a whole").
A group is the PRs that deliver one piece users would notice together,
such as "the first usable version" or "the Tailscale engine". The page
is built only when every PR in it is ready; a draft or a lone PR in
progress gets none. Lead with the whole (what a user can now do, before
and after), and put the PRs and their review trails underneath. A small
PR outside any group (a docs fix, a pin) needs no page: the report names
it.

Every report to the maintainer opens with what needs them: each page's
link, the PRs it covers, the head SHAs it was checked at, and whether
they're ready. If the maintainer asks for a page for a single PR, that
request wins.

The page is a claude.ai artifact: private to the maintainer, outside the
repo. **Don't link it from a PR, an issue or any file in this repo.**
Others can't open it, and the repo is public. Give the link in chat.

## 1. Gather the facts

For each PR, at its current head. Run `git fetch origin` first and use
`origin/` refs throughout: a local branch, a stacked base especially, can
be stale and give the wrong diff. A PR from a fork has no `origin/<head>`;
fetch it as `git fetch origin +pull/<n>/head:refs/remotes/origin/pr-<n>` and
use `origin/pr-<n>`.

- `gh pr view <n> --json number,title,body,isDraft,baseRefName,headRefName,headRefOid,additions,deletions,changedFiles`
- The issues it closes (the graphql query in `pr-ready` step 1), and
  each issue's **"Done when"** list: the scope the work is judged against.
- `git diff --stat origin/<base>...origin/<head>` and
  `git log --format='%h %s' origin/<base>..origin/<head>`. The totals must
  match `gh pr view`'s `additions`, `deletions` and `changedFiles`; if they
  don't, a ref is stale. For a stacked PR, use `origin/<branch below>` in
  place of `origin/<base>` until the one below merges; its totals then
  differ from `gh pr view`'s, which count the lower PR's commits too.
- Changes to `docs/plan.md`, `docs/architecture.md`, `README.md` and
  `docs/decisions/`: `git diff origin/<base>...origin/<head> -- docs README.md`
  (from `origin/<branch below>` for a stacked PR, as above).
- Each `pr-reviewer` comment, its verdict, its findings, and what was done
  about each:
  `gh pr view <n> --json comments --jq '.comments[]|select(.body|startswith("## pr-reviewer"))|.url'`.
- CI on the head: `gh run list --branch <branch> -L 1`.
- Issues the work filed, or whose bodies it changed.
- **The milestone it belongs to**, for the map at the top: the closed
  issues' milestone, or, when they have none (process work), the plan's
  current milestone.
  `gh api 'repos/bulkhead-labs/tailsuite/issues?milestone=<number>&state=all&per_page=100' --jq '.[]|select(.pull_request|not)|[.number,.state,.title]|@tsv'`,
  `docs/plan.md`'s order for that milestone, and the maintainer
  prerequisites that block any of it.
- The base of each PR, and the order they must merge in.

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
2. **Where the milestone stands.** Before any PR detail, so the
   maintainer sees the macro first. One compact view, from the facts
   above:
   - the milestone's goal in one line, and a count: issues done, in
     review (these PRs), not started;
   - every issue in the plan's order, one row or chip each, marked done,
     in review (and which PR), next, or blocked (and on what, naming any
     maintainer prerequisite);
   - one sentence: what these PRs move, and what's next on the critical
     path to the milestone being done (its definition in the plan:
     every issue closed and the user-journey suite passing on a
     release).

   Keep it to what fits on a phone screen; the detail is below.
3. **Verdict box.** Ready or not, the review rounds, CI. What to do: merge
   order, and anything waiting on the maintainer. Below it, a table of
   every open PR the merge order touches (each one's base, when it merges,
   its state), even for a one-PR page, since stacked PRs merge together.
4. **Scope.** The issue's "Done when" items, one row each: the item, what
   delivers it (file, test or doc), and a chip: `done`, `partly` or
   `moved to #n`. Then anything delivered **beyond** the scope, and why.
   This is the section the maintainer checks the work against.
5. **What changed.** By area, in plain words: what each part does and the
   files that hold it, with line counts. Code blocks only where the shape
   matters (a type, a file format, a command).
6. **Choices made.** The implementation choices with trade-offs (each in
   a decision record), what was given up, and the alternative. Link the
   record on the branch.
7. **What it sets up.** Each downstream issue it unblocks, and exactly
   what that issue will use from this work. For a group, how the PRs fit
   together.
8. **Plan changes.** Everything that changes the plan of record:
   - new issues, and where they sit in the milestone order;
   - work moved between milestones, narrowed or deferred;
   - decisions added, amended or superseded;
   - prerequisites, dates or pins that moved;
   - user-visible behaviour the plan didn't state (a stricter file
     format, a new REFUSED case).

   Say plainly which of these need the maintainer's agreement.

   **Show each change, don't only name it.** For every change to what a
   user writes, sees or gets (a file format, a command, an output, a
   REFUSED case) and every change the maintainer must agree to, show
   the two versions side by side:
   - **Before and after,** as real examples: the file as it was and as it
     is now, or the output of a run each way. Use the PR's own synthetic
     test data where it has some.
   - **The difference, in a sentence or two:** what the user can do now
     that they couldn't, or the other way round, and what a run does
     differently.
   - **When it's a choice:** each option the same way (option A's file
     and what a run does with it, then option B's), with the
     recommendation marked.

   A two-column layout (`.two`) holds the pair, and stacks on a phone.
9. **Review trail.** Each reviewer round: verdict, the findings, and how
   each was fixed or answered, linked to the PR comment. Then the tests:
   what ran, where, with numbers.
10. **Risks and what to watch.** What could still be wrong, and what would
   show it.
11. **Reading guide.** If the maintainer reads the diff: which files, in
    which order, and what to check in each. Link each file on the branch.
12. **Also waiting on you** (only if there's something): open questions
    from the same body of work that aren't about these PRs, each with a
    recommendation, in one box at the end.
13. **Questions** (only if there are any). Ask the maintainer only
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
- **Date it.** The eyebrow carries the date and each PR's head SHA the
  page was checked at.
- **Title**: two to four words naming the work, ending in "Review", such
  as "Tailnet Model Build Review". Keep it across updates; it's how a later
  session finds the page (the Artifact tool's `list`).
- **What's settled goes in the repo.** The page and chat are private. An
  answer, or anything else the maintainer settles there, is written into
  the repo before it's acted on: in a PR or issue comment, or an issue's
  "Done when" list. A design question takes
  [0010](../../../docs/decisions/0010-public-tracking-in-issues.md)'s
  route: a `type:design` issue, settled into a decision record.
  The page summarises the repo; it's never the only record.
- **Subagents:** gathering facts for a big group may go to a subagent,
  as the [`delegate`](../delegate/SKILL.md) skill says. The page itself
  is written and checked here.
- **Check before you publish.** Every number (lines, inputs, rounds), SHA
  and issue number on the page against the facts from step 1. A wrong
  claim on a review page costs more than a missing one.

## 4. Keep it current

- **When any PR in the group changes** (fixes, another review round, a
  merge of `main`), republish the same page, with the new SHAs in the
  eyebrow.
- **After the group's last PR merges**, as part of
  [`post-merge`](../post-merge/SKILL.md): add an outcome box at the top
  (one line per PR: "Merged <date> as <SHA>"; what changed in review;
  follow-ups filed), and repoint branch links to the merge commit
  (`blob/<merge SHA>/<path>`), so the page keeps showing what merged.
- **When the maintainer leaves comments or answers on the page**, record
  them in the repo (see Rules), act on them, then update the page to say
  what was done.
