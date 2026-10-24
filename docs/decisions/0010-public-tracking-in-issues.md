# 0010: All work is tracked in public GitHub issues

Status: **accepted** (2026-10-24).

## Decision

- **Every piece of tailsuite's work is an issue in this repo**, labelled by
  type and area, on a milestone.
- **Open design questions are issues**, settled into decision records.
- **The plan, architecture and decisions live in `docs/`** and are kept true
  after every merge ([process](../process.md)).
- **The status page** renders `docs/plan.md` and the decision index, and
  reads milestone progress and design questions live from GitHub
  ([0012](0012-status-page-hosting.md)).

## Why

The project is public, and its development process is part of what it
shows: users and contributors can see what is planned, what is open and
why.

## Given up

**The maintainer's private work tracker doesn't hold this project's work.**
Anything that isn't for the public, such as which companies to show it to,
stays out of this repo.
