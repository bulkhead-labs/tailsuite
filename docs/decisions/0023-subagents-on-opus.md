# 0023: Subagents run on Opus, with the effort set by the work

Status: **accepted** (2026-10-26). Built in #63. Follows
[0008](0008-tests-and-pr-flow.md).

## Context

Agents working on tailsuite start subagents: the `pr-reviewer` on every
PR, and others for searches, checks and pieces of building. Claude Code
lets each one choose a model and an effort level. Left unset, a subagent
runs on its caller's model, at the caller's session effort if one is set,
otherwise at the model's default. For Opus 5.5 that default is `medium`.
The `CLAUDE_CODE_EFFORT_LEVEL` environment variable, when set, overrides
both.

The maintainer asked for Opus on every subagent, with higher effort for
design and hard work (2026-10-26). The highest goes to design and
review, and hard building gets one step below (#63).

## Decision

- **Every subagent runs on Opus.** The model isn't chosen per task.
- **The effort is set by the work**, in the
  [`delegate`](../../.claude/skills/delegate/SKILL.md) skill's table:

  | Work | Effort |
  |---|---|
  | Design and decisions, and review | `max` |
  | Hard building and debugging | `xhigh` |
  | Ordinary building | `high` |
  | Finding things | `medium` |

- **The `pr-reviewer` agent** sets `model: opus` and `effort: max` in its
  own file, and the [`pr-review`](../../.claude/skills/pr-review/SKILL.md)
  skill passes them too.

## Why

- **Design and review are where a miss costs most.** Every review round
  on #60 and #62 found something real, including a blocking bug in #60
  and a security gap in #62. A finding missed there turns into a wrong
  decision or a merged bug.
- **One model keeps results comparable.** The reviewer's verdicts mean
  the same thing from one PR to the next.

## Evidence (2026-10-26)

- **Claude Code 2.1.301's model table** gives Opus 5.5's effort cost index
  as low 0.48, medium 0.66 (the default), high 1, xhigh 1.66 and max 3.44.
  The same build accepts `low`, `medium`, `high`, `xhigh` and `max` for
  the Agent tool's `effort`, and those or an integer for an agent file's
  `effort:`. It takes `opus` as a model alias. Checked in the binary installed on the
  maintainer's VM.

## Alternatives

- **Each caller's default model and effort.** Cheapest, but a review
  would run at whatever the caller's session was set to, `medium` by
  default.
- **A smaller model for searches.** Cheaper for the "finding things" row.
  The maintainer chose one model for everything.

## Given up

- **Cost**, against the default `medium` (0.66):
  - reviews and design at `max` cost about 5 times as much (3.44);
  - hard building at `xhigh`, about 2.5 times (1.66);
  - ordinary building at `high`, about 1.5 times (1);
  - searches stay at `medium`.
- **An environment override.** If `CLAUDE_CODE_EFFORT_LEVEL` is set, it
  wins over this table. It's unset on the maintainer's VM.
- **Speed.** Higher effort takes longer. A review round took 2 to 13
  minutes on #60 and #62.
- **Forks ignore it.** The Agent tool's `fork` type always inherits the
  caller's model and effort, so the `delegate` skill doesn't use it for
  work it sets an effort for.
