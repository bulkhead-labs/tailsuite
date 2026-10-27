# 0031: The local UI, as built

Status: **accepted** (2026-10-26). Builds [0017](0017-local-ui.md) for
#11. Records the implementation choices 0017 left open.

## Context

- 0017 settled what users see, the privacy measures and the stack:
  Preact, htm and Cytoscape.js, vendored, with no build step; Server-Sent
  Events read with `fetch`; a Playwright smoke test.
- `tailsuite up` and `tailsuite test` are separate processes
  ([0020](0020-node-agent.md)), and `test` writes the last run's JSON to
  `.tailsuite/results.json` (#12's CLI, decision 0030, in review on #77).
- The repository has no Node toolchain, and adds none (0017).

## Decision

- **`internal/ui`** serves the page from the binary on `127.0.0.1` and
  `::1`, one random port for both (`::1` is skipped where the host has
  no IPv6).
- **The map before any traffic** (`ui.NewView`) is the lab's devices and
  the paths the policy's grants and accept ACLs name, resolved to lab
  devices with the test suite's resolver, each labelled unverified. An
  entry that names no lab device (an IP set, `autogroup:internet`) isn't
  drawn, and the page lists it. Nothing here evaluates the policy beyond
  naming the devices a rule mentions.
- **Results replace the map path by path.** Every pair a test probed is
  drawn from its result: allowed and proven, blocked and proven (a deny
  that held, with its positive control, 0001), FAIL, or not proven
  (UNKNOWN or REFUSED). A policy path keeps, still unverified, the ports
  no test probed between its two devices; a test on one port proves
  nothing about the others.
- **Results arrive a run at a time.** The server watches the results
  file and sends each new run whole. It never sends a test's result
  before the run has finished, because the runner turns results UNKNOWN
  at the end when listeners moved under them (0020).
- **Only runs written after the UI started are shown.** An older file is
  another lab's.
- **Links and sessions** follow 0017: a 32-byte secret in the link's
  fragment, traded once for a 32-byte session secret sent as a header;
  secrets are kept as their SHA-256; an unused link expires after five
  minutes; a used link traded again within that time ends the session it
  made, and is refused after. A link in the address bar is traded even
  over a stored session, and when it's pasted into an open tab (a
  fragment change, which doesn't reload the page). Every response carries the CSP, `nosniff`,
  `no-referrer` and same-origin opener and resource policies.
- **Cytoscape injects a `<style>` element**, which the CSP refuses,
  unless the page has an element with its id. The page's stylesheet link
  carries that id, and `style.css` holds Cytoscape's one rule.
- **Vendored at the newest versions past the two-week rule** (checked
  2026-10-26 against the npm registry, each tarball against its
  integrity hash): Preact 11.0.1 (11.0.2 is from 2026-10-23), htm 3.1.1,
  Cytoscape.js 3.34.3 (3.34.4 is from 2026-10-15). Only Preact's hooks
  file is changed, its bare `preact` import made relative. A unit test
  checks each file's SHA-256.
- **The brand's tokens and fonts** are copied from the status page
  (`docs/assets`), Latin and Latin Extended only, so the page makes no
  external request.
- **The smoke test is Playwright for Python** in its official image,
  pinned by digest, with the `playwright` package and its dependencies
  installed from a requirements file pinned by hash: no Node in the
  repository. `go test -tags ui ./internal/ui/` runs it against a real
  server. It checks the trade, the map, a run's results arriving, a
  reload, phone width in dark mode, and that the page makes no external
  request and logs no error. #13 puts it in CI.
- **`up` serves the UI while it holds the lab** ([0030](0030-the-cli.md),
  [0033](0033-up-start-and-stop-options.md)):
  - in the foreground, it opens the browser on a 0600 redirect file in
    the runtime directory, except in CI or with `--no-browser`;
    `--no-redirect-file` opens the link itself. If the page hasn't traded
    the link within 5 seconds, `up` prints it and says why. With no
    browser to open, it prints the link at once. Enter in its terminal
    prints a fresh one-time link;
  - detached, the background process serves the UI, logs the link, and
    leaves a redirect file whose path it records in the lab state; the
    `up` that started it opens the browser on it, under the same rules;
  - the UI stops, and the redirect file goes, when the lab does.

## Why

- **Drawing from the rules, not evaluating them**, keeps the first
  picture honest: it shows what the policy mentions, labelled unverified,
  and only traffic turns a path solid (0001).
- **A whole run at a time** can't show a result the run later changes.
- **Python Playwright in a pinned image** keeps the toolchain Go-only.

## Given up

- **Results don't stream test by test**, so a long run shows nothing
  until it ends.
- **The policy-drawn map ignores ports' protocols, posture and `via`**;
  it shows which devices a rule names, nothing more.
- **The smoke test needs Docker** and the network to install its
  packages, so it isn't part of `go test ./...`.
