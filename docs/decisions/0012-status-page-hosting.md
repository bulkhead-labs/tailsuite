# 0012: The status page is served by GitHub Pages from docs/, with no build

Status: **accepted** (2026-10-24).

## Context

The plan has to stay true and visible. A page that copies plan text drifts.
The agent's GitHub token can't push workflow files (no `workflow` scope),
and GitHub Pages can serve a folder on a branch directly.

## Decision

- **GitHub Pages serves `docs/` from `main`**, with no workflow and no
  Jekyll (`docs/.nojekyll`). Every push to `main` republishes it.
- **The page holds no plan text.** It renders `docs/plan.md` and
  `docs/decisions/README.md` beside it, and reads milestones, design
  questions and the latest commit live from the GitHub API.
- **It shows where the plan and GitHub disagree:**
  - "not started" with closed issues;
  - "done" with open issues or an open milestone;
  - a closed milestone the plan doesn't call done;
  - a milestone on only one side;
  - a current milestone that isn't the lowest one not done.
- **Markdown is rendered by marked and sanitized by DOMPurify** from cdnjs,
  at exact versions with SRI. The CSP allows only those two script URLs.
  GitHub data is inserted as text.
- **If the CDN is blocked**, the page shows the raw markdown. If the API is
  unavailable (60 unauthenticated calls an hour per visitor), it says so
  and links to GitHub.
- **Design questions reach the page only after triage**: the issue form
  files them as `needs-triage`, and a maintainer adds `type:design`.

## Given up

- **No response headers on Pages**, so the CSP is a meta tag, without
  `frame-ancestors` or reporting.
- **A runtime dependency on cdnjs.**
- **Live data is limited by GitHub's unauthenticated rate limit.**
- **The page's parsing logic has no automated tests** until CI exists (#1).
  It was checked by hand in Chromium.
