# 0013: Agents write CI; main is trusted; branches need explicit approval for secrets

Status: **accepted** (2026-10-24). The maintainer's settings: **pending** (#39;
[plan](../plan.md#m1-order-and-prerequisites)).

## Context

tailsuite's maintainer wants agents to help build CI/CD. GitHub only lets
a token push `.github/workflows/` with workflow rights, and a workflow
runs with whatever secrets it can reach. An agent that reads untrusted
content can be steered by it (prompt injection).

tailsuite's CI will hold real secrets:

- the Tailscale test organization's OAuth client (it creates tailnets);
- the Homebrew tap's credential;
- the right to sign releases: keyless cosign signs as whichever workflow
  ran.

For a tool people install, the release pipeline is the crown jewel.

## Decision

**Agents may write workflows. The protection lives in where secrets sit
and who approves.**

1. **The agent's token**: a fine-grained token for `bulkhead-labs/tailsuite`
   only, with:
   - Contents, Workflows, Pull requests, Issues, Actions, Pages: read and
     write. GitHub offers Workflows only as read and write.
   - No Administration, and no Secrets.
2. **The agent's role on the repo is Maintain, not Admin**, so it can't
   change rulesets, environments, secrets or Actions settings.
3. **Workflows from `main` are trusted. Branch workflows get nothing
   secret:**
   - The repo's default `GITHUB_TOKEN` is read-only, and Actions may not
     create or approve pull requests.
   - **There are no repo-level secrets.** Every secret lives in a GitHub
     environment:

     | Environment | Runs from | Approval | Holds |
     |---|---|---|---|
     | `nightly` | `main` only | none (scheduled) | the test organization's OAuth client |
     | `release` | `main` and `v*` tags | the maintainer | the tap credential; the signing job |
     | `branch-run` | any branch | **the maintainer, every run** | the test organization's OAuth client, for a deliberate run on a branch |

   - A workflow on a branch, whoever wrote it, runs with a read-only token
     and no secrets, unless the maintainer approves its `branch-run` job.
4. **Workflow changes need the maintainer's review.** `.github/CODEOWNERS`
   owns `.github/` (workflows, CODEOWNERS itself, the templates). A ruleset
   on `main` requires a pull request and code-owner review for owned paths;
   other paths need none. Force pushes and deletion are blocked.
5. **Only the maintainer creates release tags** (a ruleset on `v*`).
6. **Actions must be pinned to full commit SHAs**, enforced by the repo's
   Actions policy as well as by CLAUDE.md rule 9.
7. **Pull requests from forks** need approval before their workflows run.
8. **Every workflow declares least-privilege `permissions:`** and references
   secrets only through its environment.

## Why

A hijacked agent that pushes a workflow gets runner minutes, a read-only
token and no secrets. That's no more than it gets by pushing code, which
it can already do. Shipping anything to users needs the maintainer's
click: on the merge of a workflow change, on the `release` environment,
and on the tag.

The maintainer's view (2026-10-24): denying agents workflow access
entirely is wrong; they're needed for CI/CD. Workflows from `main` can run
freely, and branch workflows need explicit permission.

## Alternatives

- **No workflow rights, as in the maintainer's home-lab repo.** The agent
  drafts workflows and the maintainer pushes them. Safe, but M1 is heavy on
  CI, so every week would need a handoff.
- **Workflow rights with repo-level secrets.** Any branch's workflow could
  read them. Rejected.
- **Adding the `workflow` scope to the agent's classic token.** It would
  cover every repo the agent can push to, not just this one. Rejected.

## Given up

- **A hijacked agent can still push malicious code to a branch**, and to
  `main` through unowned paths that need no review. A release still needs
  the maintainer's approval, so that code doesn't reach users.
- **Running the real-Tailscale tier on a branch** needs a click each time.
- **Repo settings** (merge options, Pages source) are the maintainer's.
  The agent can still switch the Pages source for previews, since Maintain
  covers Pages.

## Sources

- GitHub's Actions policy can require actions pinned to full-length commit
  SHAs, per repo or per org (checked 2026-10-24, from third-party guides:
  [guideflow](https://www.guideflow.com/tutorial/how-to-require-actions-to-be-pinned-to-a-full-length-commit-sha-for-a-repository-in-github),
  [Semgrep](https://semgrep.dev/blog/2026/sha-pinning-for-github-actions-org-wide)).
