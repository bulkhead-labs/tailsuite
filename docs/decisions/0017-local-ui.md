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
  as the policy itself.

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
  behind [0007](0007-read-only-by-default.md)'s gates.

### Keeping it private

- **Localhost only:** served on `127.0.0.1` and `::1`, on a random free
  port.
- **A one-time token in the link**, exchanged on first load for an
  `HttpOnly`, `SameSite=Strict` session cookie. Other local users and other
  websites can't read the map.
- **Host checking:** the server refuses any `Host` header other than the
  localhost names, which defeats DNS rebinding.
- **No external requests:** the Content-Security-Policy is
  `default-src 'self'`, and everything is embedded in the binary.

### The stack

- **No build step and no Node.**
  - Preact (MIT) and htm (Apache-2.0) are loaded as vendored ES modules
    through an import map.
  - Cytoscape.js (MIT) draws the map.
  - They're vendored at exact versions under the two-week rule, with SRI
    hashes, as the status page does ([0012](0012-status-page-hosting.md)),
    and embedded with `go:embed`.
- **If a later dependency needs bundling**, such as a code editor for #34,
  esbuild runs from Go at generate time. esbuild is written in Go, so
  there's still no Node toolchain.
- **Results stream over Server-Sent Events.** Actions, such as re-running
  a test, are plain POST requests. SSE is one-way, reconnects by itself,
  and needs only `net/http`.
- **The UI reads the same JSON results as `tailsuite test --json`**
  ([0015](0015-test-file-labels-profiles.md)), so the CLI, CI and the UI
  can't disagree.
- **A Playwright smoke test covers it** (#11).

## Why

- **The map first** makes the first minute show what the product does. A
  results list alone looks like a CI log.
- **Opening the browser** removes a step from that first minute, and the
  CI rule keeps it out of pipelines.
- **The token, the cookie and the Host check** keep a local tailnet map
  from leaking to other users on the machine, or to a web page running in
  the same browser.
- **No build step** keeps the toolchain Go-only, and the release free of a
  second ecosystem. It's the approach the status page already proved.
- **Preact and htm** give later views (the matrix, the diff, the editor) a
  component model for a few kilobytes. **Cytoscape.js** handles graphs
  from three devices up to thousands, with built-in layouts.
- **SSE** fits a one-way stream of results.

## Evidence (checked 2026-10-25, npm registry)

| Library | Latest | Licence | Pinnable today (two weeks old) |
|---|---|---|---|
| preact | 11.0.2 (2026-10-23) | MIT | 11.0.1 (2026-10-08); 11.0.0 was released 2026-09-30 |
| htm | 3.1.1 (2022-04-26) | Apache-2.0 | 3.1.1 |
| cytoscape | 3.34.4 (2026-10-15) | MIT | 3.34.3 (2026-09-07) |

#11 pins the exact versions when it's built.

## Alternatives

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

- **Preact's 11.x line is new** (2026-09-30). The pin waits for the
  two-week rule.
- **Until esbuild is wired, only libraries that ship ES modules** can be
  used.
- **SSE is one-way.** Anything interactive goes through separate
  requests.
- **Vendored libraries are updated by hand**, as on the status page.
