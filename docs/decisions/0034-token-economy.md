# 0034: Lean agent sessions: one per issue, short prompts and replies

Status: **accepted** (2026-10-26). Builds on
[0028](0028-review-tiers.md), which sized reviews; this record covers the
rest of an agent's spend. Changes no check, review tier or round.

## Context

Measured on 2026-10-26 from the maintainers' Claude Code transcripts for
this repo, 2026-10-16 to 2026-10-26: 3,034 API calls. Cost is in relative
units at the usual price ratios (input 1, cache read 0.1, 1-hour cache
write 2, output 5).

- **Re-reading the context is most of the cost.** Every call re-reads the
  whole session from cache, so cost grows with context × turns, roughly
  the square of a session's length. Output, thinking included, was a
  small share.
- **Sessions carried several issues each.** The main session's median
  context per call was 387k. #75 used 27 calls at a median of 742k, and
  #71 57 calls at 700k: small PRs that cost like big ones because they
  ran at the end of a long session.
- **Effort mattered less than length.** By effort, `xhigh` was 40% of the
  cost and `high` 36%, mostly because those calls ran in long contexts.
  `max`, all review subagents before 0028, was 7%.
- **Reviews:** about a third of the calls were subagents. Each round
  returned its whole comment into the main session, and prompts often
  carried a summary of the change the reviewer then read again for
  itself.
- **What fills a context** is mostly what the agent itself types and
  reads back: whole-file rewrites, repeated command preambles, and long
  `git`, `gh`, `go test` and `docker` output. File reads are a small part.

## Decision

The rules are in [`CLAUDE.md`](../../CLAUDE.md#token-economy-0034). In
short:

1. **One session per issue.** The handoff goes in the PR body and, when
   work stops part-way, a comment on the issue. Past about 300k, write
   the handoff and suggest a fresh session. `post-merge` ends with it.
2. **Edit, don't rewrite; short commands, small output; read by range.**
3. **Reviewer prompts carry facts only** (PR, head SHA, tier, budget,
   worktree, branch below, earlier rounds), not the diff, the issue or a
   summary. **Replies are short**: the verdict, the comment link, one
   line per finding. The full review stays in the PR comment.
4. **The reviewer** starts from the diff's file list, reads docs by
   section, and takes lint and unit results from the head SHA's CI run,
   running a test only to show a finding. At the light tier, a diff with
   no code marks the engine, UI, OpenTofu and release items N/A in one
   line.

## Why

Cost comes from how long a context is carried. Shorter sessions and
leaner turns cut it without removing a check, and a shorter context also
reasons better. A facts-only prompt keeps the review independent too: the
reviewer can't be steered by the author's summary.

## Given up

- **Continuity.** A fresh session re-reads what the last one knew, and a
  handoff can miss a nuance. The PR and the issue outlast any session
  anyway.
- **The reviewer no longer reruns unit tests CI already ran.** A run
  still going when it reviews is listed as unchecked, not assumed green;
  `pr-ready` still blocks on a red or missing run before marking ready.
- **The numbers are estimates:** list-price ratios, not actual billing,
  and the breakdown of what fills a context comes from character counts.
