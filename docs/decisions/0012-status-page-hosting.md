# 0012: The status page is served by GitHub Pages from docs/, with no build

Status: **accepted** (2026-10-24).

## Context

The plan has to stay true and visible. A page that copies plan text drifts.
The agent's GitHub token can't push workflow files (no `workflow` scope),
and GitHub Pages can serve a folder on a branch directly.

## Decision

- **GitHub Pages serves `docs/` from `main`**, with no workflow and no
  Jekyll (`docs/.nojekyll`). Every push to `main` republishes it.
- **Previews:** while a PR changes the page, Pages may point at that
  branch, and `post-merge` points it back at `main`.
- **The page holds no plan text.** It renders `docs/plan.md` and
  `docs/decisions/README.md` beside it, and reads milestones, design
  questions and the latest commit live from the GitHub API.
- **It shows where the plan and GitHub disagree:**
  - "not started" with closed issues;
  - "done" with open issues or an open milestone;
  - a closed milestone the plan doesn't call done;
  - a milestone on only one side;
  - a current milestone that isn't the lowest one not done. This one
    needs only the plan, so it works without the API.
- **marked and DOMPurify are vendored** in `docs/assets/vendor/` at exact
  versions, with SRI hashes. The CSP's `script-src` is `'self'` only. GitHub
  data is inserted as text.
- **If the renderer fails or throws**, the page shows the raw markdown and
  keeps what it parsed from the plan. **If the API is unavailable**, each
  list says so and links to GitHub.
- **Design questions reach the page only after triage**: the issue form
  files them as `needs-triage`, and a maintainer adds `type:design`.
- **A library is pinned only once its release is two weeks old.**

## Evidence (checked 2026-10-24)

- **marked 12.0.2:** sha384 `/TQbtLCAerC3jgaim+N78RZSDYV7ryeoBCVqTuzRrFec2akfBkHS7ACQ3PQhvMVi`.
- **DOMPurify 3.4.16** (released 2026-09-23): sha384
  `a7SzOxErzJ3ZpQz0zJ32d67dSitNzPcbfybc/ykU9KJhMgZkwqfSxlhhdJRS+XGL`.
  Both were downloaded from cdnjs and match.
- **Three DOMPurify advisories were published 2026-10-22**, fixed in 3.4.17:
  GHSA-cg2g-xh64-v4q7, GHSA-mrv2-3pqr-j5jr, GHSA-c6gr-qvv4-5v3f.
  - **None applies here.** The page calls `sanitize(string)` with the
    default config (string output, no hooks, `text/html`), not
    `SAFE_FOR_TEMPLATES`, `IN_PLACE` or `PARSER_MEDIA_TYPE`.
  - **3.4.17 was released 2026-10-12**, so it can be pinned from 2026-10-26
    (#31).
- **GitHub's unauthenticated API limit is 60 requests an hour per IP.** A
  page load makes 3 calls, so about 20 loads an hour from one address, and
  fewer behind a shared NAT.
- **Checked by hand in Playwright Chromium:**
  - light, dark, and phone width with no horizontal scroll;
  - no console errors;
  - with the script source blocked, the raw markdown is shown and the plan
    is still parsed.

  Unit tests for the parsing are #30.

## Alternatives

- **Load the libraries from cdnjs.** That was the first version; the
  reviewer flagged it. Vendoring removes the runtime dependency on a third
  party, and lets the CSP be `'self'` only.
- **A Pages workflow that builds the page.** It needs the `workflow` token
  scope and adds a step that can fail. Not needed while the page renders
  in the browser.
- **Copy plan text into the page.** It drifts, which is the problem this
  page exists to prevent.

## Given up

- **No response headers on Pages**, so the CSP is a meta tag, without
  `frame-ancestors` or reporting.
- **`'self'` is the whole `bulkhead-labs.github.io` origin.** It would cover
  any Pages site the organization publishes later; only tailsuite has one
  today.
- **Vendored libraries are updated by hand** (#31 is the first).
- **Live data is limited by the per-IP rate limit.**
- **The parsing logic has no automated tests** until #30.
