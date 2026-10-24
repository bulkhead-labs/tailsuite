# 0013: Agents write CI; nothing merges or ships without the maintainer

Status: **accepted** (2026-10-24). The maintainer's settings: **pending**
(#39; [plan](../plan.md#m1-order-and-prerequisites)). Amends the context
of [0012](0012-status-page-hosting.md): once #39 is applied, the agent's
token can push workflow files.

## Context

tailsuite's maintainer wants agents to help build CI/CD. GitHub only lets
a token push `.github/workflows/` with workflow rights, and a workflow can
reach whatever secrets and token scopes it is allowed. An agent that reads
untrusted content can be steered by it (prompt injection).

What has to be protected:

- **The release:**
  - the binaries and checksums people install;
  - the container image;
  - the Homebrew formula;
  - the signatures that prove all of them.
- **The Tailscale test organization's OAuth client**, which can create
  tailnets.

## Decision

**Agents may write workflows and code. The protection is that nothing
reaches `main`, the secrets or users without the maintainer.**

1. **Every pull request needs the maintainer's approving review.**
   - The `main` ruleset requires a PR with 1 approval.
   - It dismisses stale approvals when new commits are pushed, and requires
     approval of the most recent push.
   - It blocks force pushes and deletion.
   - A PR's author can't approve it, so an agent never approves its own.
   - The maintainer's own PRs bypass through the Repository admin role.
   - CODEOWNERS also names the maintainer for `.github/`, `CLAUDE.md`,
     `.claude/` and `docs/decisions/`, so those always need their review,
     even if other collaborators join.

   This only formalizes rule 6: every merge was already the maintainer's
   call.
2. **The agent's access:**
   - **Its token** is a fine-grained token for `bulkhead-labs/tailsuite`
     only: Contents, Workflows, Pull requests, Issues, Actions and Pages read
     and write; Metadata read (always included); nothing else.
   - **Its role on the repo is Maintain, not Admin**, so it can't change
     rulesets, environments, secrets or Actions settings (environments
     need admin).
3. **Secrets live only in environments.** There are no repo-level secrets,
   and no organization secrets or variables are visible to this repo.

   | Environment | Deployment refs | Approval | Holds |
   |---|---|---|---|
   | `nightly` | `main` | none (scheduled) | the test organization's OAuth client |
   | `release` | `v*` tags only | the maintainer | the Homebrew tap credential and the container-registry credential |
   | `branch-run` | no restriction | **the maintainer, every run** | the test organization's OAuth client |

   - The environments, with their protections, exist before any workflow
     names them. A workflow that names a missing environment creates it
     unprotected.
   - **PR tiers** (integration and the user journey) run on headscale and
     need no secrets. A real-Tailscale run on a branch goes through
     `branch-run`. Before approving one, the maintainer reads the branch's
     diff against `main`, workflows first. "Prevent self-review" stays off,
     so the maintainer can approve a run they dispatched themselves.
4. **Branch workflows get no secrets, and nothing that reaches users
   depends on `GITHUB_TOKEN`.**
   - The repo default for `GITHUB_TOKEN` is read-only, but anyone with write
     access can raise it in a workflow file. A branch workflow can therefore
     take any `GITHUB_TOKEN` scope except workflows; it gets no secrets.
   - **So nothing that reaches users is published with `GITHUB_TOKEN`:**
     - images are pushed with the registry credential in `release`, never
       `packages: write`;
     - the Homebrew formula is pushed with the tap credential in `release`.
   - **Actions may not create or approve pull requests.**
5. **Releases can't be changed after they're published:**
   - **Immutable releases** are on, which locks a published release's
     assets and tag.
   - goreleaser drafts the release, uploads the assets and publishes, all
     inside the approved `release` job.
   - The release job restores no caches (`cache: false` on setup-go, no
     `actions/cache`) and downloads artifacts only from its own run.
6. **Signatures are trusted by their exact identity, not by a gate.**
   - Keyless cosign issues a certificate to any job with `id-token: write`,
     on any ref, so environments don't gate signing.
   - **Verifiers pin the exact signer instead:**
     - **who:** CI, the install script and the docs;
     - **issuer:** `https://token.actions.githubusercontent.com`;
     - **identity:**
       `https://github.com/bulkhead-labs/tailsuite/.github/workflows/release.yml@refs/tags/v…`.

     They never use a repo-wide pattern.
   - The `v*` tag ruleset is what makes that identity meaningful.
7. **Only the maintainer creates release tags.**
   - A tag ruleset on `v*` blocks creating, updating and deleting them,
     with a bypass for the Repository admin role.
   - That holds while the maintainer is the only admin (organization owners
     aside).
   - Tested once #39 is applied, including tags created through the
     Releases API.
8. **Workflow rules** (CLAUDE.md rule 8):
   - every workflow declares least-privilege `permissions:`;
   - every action is pinned to a full commit SHA, which the repo's Actions
     policy also enforces;
   - no `pull_request_target` and no `workflow_run` triggers: they run in
     `main`'s context, with `main`'s environments, on anyone's PR;
   - only the maintainer approves workflow runs from forks, which need
     approval for all external contributors.
9. **Nothing that verifies tailsuite is served from GitHub Pages.** Not the
   install script, not the verification instructions. Those live in the
   repo and in the release itself.

## Why

- **Merges:** a hijacked agent can push branches and open PRs, and nothing
  more lands without the maintainer's review.
- **Branch workflows:** a branch's workflow gets runner minutes and a
  `GITHUB_TOKEN` that can touch the repo, but no secrets, no signed
  identity that verifiers accept, and no route to users.
- **Releases:** they need the maintainer's tag and approval, and can't be
  altered afterwards.

The maintainer's view (2026-10-24): denying agents workflow access entirely
is wrong; they're needed for CI/CD. Workflows from `main` can run freely,
and branch workflows need explicit permission.

## Alternatives

- **No workflow rights, as in the maintainer's home-lab repo.** The agent
  drafts and the maintainer pushes. Safe, but M1 is heavy on CI.
- **Owning only `.github/`, with no approval on other paths.** That was the
  first draft. Rejected in review: trusted workflows on `main` run code
  from every path, so unreviewed code there reaches the secrets and the
  release.
- **Repo-level secrets.** Any branch's workflow could read them.
- **Adding the `workflow` scope to the agent's classic token.** It would
  cover every repo the agent can push to.

## Given up

- **An Approve click on every PR**, the docs-only `post-merge` syncs
  included.
- **A branch workflow can still write to the repo through `GITHUB_TOKEN`:**
  push branches, create draft releases, post statuses and checks. Before
  checks become required gates (#1, #13), confirm that a check posted by a
  branch workflow can't satisfy them (uncertain today).
- **The agent's Actions: write is broad:** it can delete runs and their
  logs, disable workflows, evict caches, and change the OIDC subject
  template. That last one matters only if OIDC federation is ever added.
- **The agent can point Pages at any branch** (Maintain plus Pages: write).
  The status page is informational, and nothing that verifies tailsuite is
  served there (item 9).
- **Running real Tailscale on a branch needs a click each time.**

## Sources (checked 2026-10-24)

- [Managing GitHub Actions settings for a repository](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/enabling-features-for-your-repository/managing-github-actions-settings-for-a-repository):
  - `GITHUB_TOKEN` defaults, and that anyone with write access can raise
    them in the workflow;
  - fork approvals;
  - requiring actions pinned to a full-length commit SHA.
- [Workflow syntax: permissions](https://docs.github.com/en/actions/reference/workflows-and-actions/workflow-syntax#permissions)
- [Available rules for rulesets](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/available-rules-for-rulesets)
- [Managing environments for deployment](https://docs.github.com/en/actions/managing-workflow-runs-and-deployments/managing-deployments/managing-environments-for-deployment)
- [`pull_request_target` and environment changes (2025-11-07)](https://github.blog/changelog/2025-11-07-actions-pull_request_target-and-environment-branch-protections-changes/)
- [Immutable releases](https://docs.github.com/en/code-security/supply-chain-security/understanding-your-software-supply-chain/immutable-releases)
- [Fulcio certificate fields](https://github.com/sigstore/fulcio/blob/main/docs/oid-info.md)
- [Dependency caching](https://docs.github.com/en/actions/reference/workflows-and-actions/dependency-caching)
- [Permissions for fine-grained tokens](https://docs.github.com/en/rest/authentication/permissions-required-for-fine-grained-personal-access-tokens)
- [Pages API](https://docs.github.com/en/rest/pages/pages#update-information-about-a-github-pages-site)
