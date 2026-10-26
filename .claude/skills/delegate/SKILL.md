---
name: delegate
description: Hand work in tailsuite to a subagent, choosing its model and effort by the kind of work. Every subagent runs on Opus; design, review and hard problems get the highest effort. Use whenever you start a subagent or an agent from .claude/agents/ (the pr-reviewer included), or when asked to "delegate", "fan out", "use a subagent" or "run agents in parallel".
---

# delegate

Subagents do work you'd otherwise do in this conversation: a review, a
search across many files, an independent check, a piece of building. The
maintainer's rule: **every subagent runs on Opus**. What changes with the
work is the effort.

## 1. Choose the effort

Pass `model: "opus"` and the effort from this table on every Agent call.
The maintainer asked for these levels, so pass them explicitly, even when
an agent file sets its own.

| Work | Effort | Examples |
|---|---|---|
| **Design and decisions** | `max` | A design question, a decision record, a spike's conclusions, the engine interface, anything a later milestone builds on |
| **Review** | `max` | The `pr-reviewer` agent, a security check, a second opinion on a finding |
| **Hard building and debugging** | `xhigh` | Parsers, concurrency, sandbox and network code, a test that fails only sometimes, anything a fuzzer or the race detector found |
| **Ordinary building** | `high` | A feature with a clear design, tests for code that exists, a docs change with code behind it |
| **Finding things** | `medium` | Where is X defined, which files use Y, what changed between two refs |

When unsure between two rows, take the higher one. A wrong answer from a
cheaper run costs more than the run saved.

## 2. Check the machine first

Agents that run builds or Docker use the VM's memory. Before starting
more than one at a time, check `free -m`. Run them in parallel when the
available memory covers each one's containers (each has a `--memory`
limit), with room to spare; otherwise run them one after another.

## 3. Write the prompt

A subagent starts with nothing. Its prompt must carry:

- **The task and its done-when:** the issue or PR number, and what to hand
  back.
- **Where to work:** an existing worktree under
  `~/github/tailsuite-worktrees/`, or `git worktree add` a new one from
  `origin/main`. Never switch branches in `~/github/tailsuite`.
- **The rules that bind it:** [CLAUDE.md](../../../CLAUDE.md) (public repo,
  sign-offs, pinning), and for a read-only agent, that it's read-only.
- **Access:** load the GitHub token in the same command as any `gh` or
  push (`set -a; . ~/.config/tailsuite/agent.env; set +a`), and never
  print it. Credentials for the Tailscale test organization go only to
  an agent whose task needs them.
- **What not to touch:** other agents' worktrees and branches; running
  containers it didn't start.

For an agent defined in `.claude/agents/` (such as `pr-reviewer`), say
"read and follow `.claude/agents/<name>.md`" when the session can't start
that agent type directly.

## 4. Use what comes back

The subagent's report is its own claim. Before you act on a finding,
check it against the code. Before you pass a result on to the
maintainer, check its numbers and links. Say which agent found what.
