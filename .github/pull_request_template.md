<!-- Open as a draft. Mark ready only when every box is ticked or explained. See docs/process.md. -->

## What and why

Closes #

<!-- One paragraph: what changes for a user, and why. -->

## Definition of done

Tick each, or write N/A with the reason. See [the plan](https://github.com/bulkhead-labs/tailsuite/blob/main/docs/plan.md#definition-of-done).

- [ ] **Lab**: runs on **both engines**, or the engine that can't do it reports REFUSED
- [ ] **Tests**: unit tests; a lab integration test; a user-journey step if users touch it
- [ ] **UI**: it shows in the local UI
- [ ] **OpenTofu**: expressed in the module; the round trip still gives the same results
- [ ] **Release**: ships in the binary; works when installed from the release artifacts
- [ ] **Docs**: `docs/plan.md`, `docs/architecture.md` and the README are true after this merge
- [ ] **Decision**: a record in `docs/decisions/` for any trade-off, added to its index

## Safety (this repo is public)

- [ ] No real tailnet data: no policies, device names, IPs, keys or user names from any real tailnet
- [ ] No secrets; no names of companies or people whose networks were looked at
- [ ] Every commit is signed off (`git commit -s`)

## How it was tested

<!-- Commands run and what they showed. Paste the user-journey result when it applies. -->

## Review

- [ ] `pr-ready` checklist run, and the `pr-reviewer` agent's findings addressed: one line per review round, with its link, its verdict, and what was done about its findings, with the commits
