# 0017: The local UI

Status: **accepted** (2026-10-25). Settles #19. The maintainer settled
what users see on 2026-10-25. The stack and the safety measures are this
record's.

## Context

- **The plan's principle:** "a picture first; live results stream in
  behind it". A lab takes seconds to start, and real traffic a moment
  more.
- **#11's requirements:**
  - `tailsuite up` serves the UI from the binary;
  - it shows the nodes, the paths and results as they stream in;
  - it works offline, in light and dark, and at phone width.
- **The UI shows a map of someone's tailnet**, so it has to be as private
  as the policy itself. Later it will also take a pasted OAuth client
  ([0007](0007-read-only-by-default.md)) and edits (#34).

## Decision

### What users see

- **The first screen is a map of the lab.**
  - The devices and paths are drawn from the policy at once, labelled
    unverified.
  - Each path turns solid as real traffic proves it, allowed or blocked as
    expected.
  - The test list sits beside the map, filling in PASS, FAIL, UNKNOWN or
    REFUSED.
  - M2 adds the headline (#33) and the full map of who can reach what.
- **`tailsuite up` opens the browser.**
  - It doesn't when the `CI` environment variable is set, or with
    `--no-browser`.
  - It always prints the link.
- **The UI is read-only in M1.** Editing the policy arrives with #34 (M6),
  behind 0007's gates.

### Keeping it private

Browsers don't separate cookies by port, and treat every port on
`127.0.0.1` as the same site. So no cookie can keep other local listeners
out, and the UI doesn't use one.

- **Localhost only:** served on `127.0.0.1` and `::1`, on a random free
  port.
- **A secret per session, sent on every request:**
  - The link carries a random secret in its `#fragment`, which browsers
    never send to a server or put in logs.
  - The page keeps it in `sessionStorage`, which is per port, so a reload
    keeps working.
  - The page sends it as a header on every request, and the server
    refuses any request without it.
  - Results stream through `fetch`, which can send that header.
    `EventSource` can't.
- **Origin checks:** every request must carry the UI's own `Origin`, or
  `Sec-Fetch-Site: same-origin`, so a page served from another local port
  can't drive it.
- **Host checks:** the server refuses any `Host` header other than the
  localhost names, which defeats DNS rebinding.
- **A link expires** if it isn't opened within a few minutes.
  `tailsuite up` prints a fresh one whenever asked. With `--no-browser`,
  or over SSH, a link sitting in a terminal doesn't stay valid.
- **A strict Content-Security-Policy**, sent as a header:
  - `default-src 'self'`, with no inline scripts;
  - `connect-src 'self'`;
  - `frame-ancestors 'none'`, `base-uri 'none'` and `form-action 'self'`.

### The stack

- **No build step and no Node:**
  - Preact (MIT) and htm (Apache-2.0) as vendored ES modules;
  - Cytoscape.js (MIT) for the map.
- **Vendoring rewrites bare imports to relative paths.** Preact's hooks
  and htm import `preact` by name. With relative paths, no import map is
  needed, and an import map would be an inline script the CSP forbids.
- **Pinned and embedded:**
  - vendored at exact versions under the two-week rule;
  - each file's SHA-256 recorded at vendoring and checked by a unit test;
  - embedded with `go:embed`, so a release's signature covers them.

  That's the status page's approach ([0012](0012-status-page-hosting.md)),
  checked at build time rather than through SRI in the browser.
- **If a later dependency needs bundling**, such as a code editor for #34,
  esbuild runs from Go at generate time. esbuild is written in Go, so
  there's still no Node toolchain.
- **Results stream as Server-Sent Events**, read with `fetch`. Actions,
  such as re-running a test, are plain POST requests.
- **The UI reads the same JSON results as `tailsuite test --json`**
  ([0015](0015-test-file-labels-profiles.md)), so the CLI, CI and the UI
  can't disagree.
- **A Playwright smoke test covers it** (#11).

## Why

- **The map first** makes the first minute show what the product does. A
  results list alone looks like a CI log.
- **Opening the browser** removes a step from that first minute, and the
  CI rule keeps it out of pipelines.
- **A header secret plus the Origin and Host checks** keep the map from
  other local users and from web pages, even ones served from another
  localhost port. A cookie couldn't do that.
- **No build step** keeps the toolchain Go-only, and the release free of a
  second ecosystem. It's the approach the status page already proved.
- **Preact and htm** give later views (the matrix, the diff, the editor) a
  component model for a few kilobytes. **Cytoscape.js** handles graphs
  from three devices up to thousands, with built-in layouts.
- **Server-Sent Events** fit a one-way stream of results.

## Evidence (checked 2026-10-25, npm registry)

| Library | Latest | Licence | Pinnable today (two weeks old) |
|---|---|---|---|
| preact | 11.0.2 (2026-10-23) | MIT | 11.0.1 (2026-10-08); 11.0.0 was released 2026-09-30 |
| htm | 3.1.1 (2022-04-26) | Apache-2.0 | 3.1.1 |
| cytoscape | 3.34.4 (2026-10-15) | MIT | 3.34.3 (2026-09-07) |

#11 pins the exact versions when it's built.

## Alternatives

- **A session cookie.** It isn't separated by port, so any other localhost
  listener would receive it.
- **An inline import map**, allowed by hash in the CSP. It works, but
  rewriting the imports removes the inline script altogether.
- **React or Svelte with a Node build.** The larger ecosystem, but Node
  joins the toolchain, CI and the release.
- **Plain JavaScript.** No dependency, but each later view would rebuild a
  component model by hand.
- **Server-rendered HTML with htmx.** Simple, but a live graph fits it
  poorly.
- **React Flow**, which needs React, **or D3**, which is lower-level and
  more code for the same map.
- **WebSockets.** Two-way, but nothing needs pushing from the browser
  beyond plain requests.
- **Printing the link only.** One more step in the first minute.

## Given up

- **Preact's 11.x line is new** (11.0.0 on 2026-09-30). 11.0.1 already
  passes the two-week rule.
- **A new browser tab needs a fresh link.** `sessionStorage` isn't shared
  between tabs.
- **Until esbuild is wired, only libraries that ship ES modules** can be
  used, and vendoring has to rewrite their imports.
- **Vendored libraries are updated by hand**, as on the status page.
