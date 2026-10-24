# CLAUDE.md

tailsuite: an open-source tool that rebuilds a tailnet in a local lab, on
real Tailscale clients, proves what it allows with real traffic, and
deploys changes through OpenTofu.

Read [`README.md`](README.md), [`docs/plan.md`](docs/plan.md),
[`docs/architecture.md`](docs/architecture.md) and
[`docs/process.md`](docs/process.md) before doing anything. The decisions
are in [`docs/decisions/`](docs/decisions/README.md).

## Rules

1. **This repo is public.** Never commit, or put in an issue, PR or comment:
   - real tailnet data (policies, device names, IPs, keys, user names, the
     maintainers' own included);
   - secrets;
   - names of companies or people whose networks were looked at;
   - anything about Bulkhead Labs' clients or business plans.

   Sample configs are synthetic.
2. **Truth over mocks** ([0001](docs/decisions/0001-truth-over-mocks.md)).
   Never make a result look like PASS or a deny when the lab can't prove it;
   use UNKNOWN or REFUSED. Every deny test gets a positive control.
3. **Never send traffic outside the lab, and never write to a real tailnet**
   except in `tailsuite deploy` behind its gates
   ([0007](docs/decisions/0007-read-only-by-default.md)). Tests touch only
   lab engines and the dedicated Tailscale test organization.
4. **All work is a GitHub issue** in this repo
   ([0010](docs/decisions/0010-public-tracking-in-issues.md)), with a
   `type:` and `area:` label, and on a milestone when it belongs to one.
5. **The docs stay true.** The PR that changes behaviour updates
   `docs/plan.md`, `docs/architecture.md`, the README and the decision
   records. A choice with trade-offs gets a new decision record, dated, with
   its evidence; versions, prices and vendor claims carry the date they were
   checked.
6. **Every PR:**
   - opens as a draft;
   - runs the `pr-ready` skill before it's marked ready (that skill starts
     the `pr-reviewer` agent);
   - is squash-merged only when the maintainer asks.

   **After every merge, run the `post-merge` skill.**
7. **Sign off every commit** (`git commit -s`, DCO).
8. **Pin everything**: Go modules, images by digest, GitHub Actions by commit
   SHA, CDN scripts by exact version.

## Working here

- **Go.** `nix develop` for the toolchain; plain `go build` must also work.
- **Lab tests use Docker with unique names**, and always tear down.
- **The status page** is `docs/index.html` with `docs/assets/`, served by
  GitHub Pages from `docs/` on `main`. It renders the docs beside it and
  reads milestones live from GitHub. Don't copy plan text into it.
