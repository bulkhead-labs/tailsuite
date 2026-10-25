# 0014: The tailnet model, and which project file owns each fact

Status: **accepted** (2026-10-24). Settles #15 and #18. Amends
[0004](0004-opentofu-owns-config.md): on headscale, M1 reads the module's
values from `tofu/tailnet.auto.tfvars`, not from `tofu plan -json`.
Evidence: [research 0005](../research/0005-tailnet-model-trial.md).

## Context

Every source, engine and output plugs into one model of a tailnet (#15).
A user's project must never hold two sources for the same fact (#18).
Three findings shaped both (checked 2026-10-24):

- **A headscale lab can't run `tofu plan`.**
  - The module finds devices with `data "tailscale_device"`, which is read
    at plan time from a Tailscale API.
  - The provider's `main` also checks `tailscale_acl` against
    `/acl/validate` during plan (commit 5f48e22, 2026-06-12; not in a
    release yet).
  - headscale has no Tailscale API until #20.
- **Policy keys get lost.** headscale rejects keys it doesn't know. The
  official Go client's `ACL` type drops them, and has no `sshTests`.
- **A tfvars file holds literal values only.** OpenTofu 1.13.1 rejects
  functions and variables there, so tailsuite can read one without running
  tofu.

## Decision

### The model (#15)

- **Our own Go types, in Tailscale's terms.**
  - Names follow Tailscale's API and policy docs.
  - Every source converts into the model, and every engine is configured
    from it.
  - The model is independent of where the data came from. It is not a
    vendor-neutral abstraction: headscale speaks the same policy language.
  - The policy carries `dialect: tailscale`, so a second mesh VPN would
    add a dialect, not reshape devices and users.
- **What it holds:** the policy, devices, users, DNS, settings, and the
  tailnet's name.
- **What it leaves out:**
  - lab-only config (listeners, engines, profiles), which the planner
    reads beside the model;
  - tailsuite's own tests (#16);
  - credentials (#24), and node or machine keys;
  - device IDs, which differ between the lab and production and stay in
    engine state;
  - results.
- **The policy:**
  - **The user's file, as read, is the truth.** Deploy ships it unchanged.
  - **A typed copy is read-only.**
    - It's parsed from a copy with the comments stripped.
    - It covers every section of Tailscale's policy docs from M1.
    - It knows every place a user, group or tag can appear.
    - It keeps byte offsets, so the UI can point at the exact rule.
    - Parsing a section isn't supporting it: support is #17's capability
      matrix.
  - **Keys the typed copy doesn't understand are always listed.** They're
    found at any depth, by comparing the file with what the copy
    understood.
  - **Each engine gets the user's file plus an RFC 6902 patch**, applied
    with `tailscale/hujson`, which keeps comments. The patch is the record
    of rewrites that [0002](0002-two-engines.md) requires, and is shown
    with every run.
  - **A key the typed copy doesn't understand:**
    - **The patch rewrites nothing:** the key passes through unchanged and
      the run carries a note. If the engine rejects the key, the run is
      REFUSED.
    - **The patch rewrites identities**, as when users become tags: the
      run is REFUSED on that engine, naming the key. The key might name a
      user the rewrite can't reach, and the lab would then enforce a
      different policy from production.
- **Devices are keyed by their MagicDNS name**, as 0004 already references
  them.
- **Every record says where it came from:**
  - observed: read from the API or a plan;
  - declared: written in the project or a template;
  - inferred: filled in by tailsuite, as when a pasted policy names
    devices it has to guess (#35).

  Each record also lists the fields that were inferred. A result whose
  source or destination is an inferred device says so, and the headline
  counts such results separately.
- **Only inputs are stored.** Identity (tags if any, otherwise the user),
  effective routes and group membership are computed, never stored beside
  their inputs.
- **The model is never a project file.** It's written to
  `.tailsuite/model.json` (gitignored, keys sorted, schema-versioned) for
  the UI, golden tests and fuzzing.

### The project (#18)

**A fact lives in the file for whatever makes it true in production.**

| Fact | Made true in production by | File |
|---|---|---|
| Grants, acls, groups, tagOwners, hosts, ipsets, ssh, nodeAttrs, autoApprovers, postures, and the policy's own `tests` and `sshTests` | Tailscale's policy engine | `policy.hujson` |
| Device tags and enabled routes; later key expiry, authorization, DNS, tailnet settings | The API, through OpenTofu | `tofu/` |
| Which devices exist, who added each, OS, client version, IP; later advertised routes, exit node, SSH server, posture attributes | The device itself; tailsuite never deploys these | `lab.yaml` |
| What listens on each lab node (explicit in M1), engines, profiles | Nothing: lab only | `lab.yaml` |
| tailsuite's own tests | Nothing | `tests/` |
| The model, the lab plan, results, lab state | Computed | `.tailsuite/`, gitignored |

```
my-tailnet/
  policy.hujson          # the policy, the only copy
  tofu/
    tailsuite.tf         # tailsuite's module: logic only
    tailnet.auto.tfvars  # device tags, enabled routes
  lab.yaml               # never deployed
  tests/*.yaml
  .tailsuite/            # gitignored
```

```yaml
# lab.yaml (synthetic)
version: 1
devices:
  laptop: { user: alice@example.com, os: macos }
  server:
    os: linux
    listen: [{ http: 80 }]
  db:
    os: linux
    listen: [{ tcp: 5432 }]
engines:
  default: headscale
  profiles: { pr: headscale, nightly: tailscale }
```

- **One copy of the policy.** The module reads it with
  `file("${path.module}/../policy.hujson")`.
- **The project alone rebuilds the lab, with no credentials and no
  network.**
  - PR runs on headscale have no secrets
    ([0013](0013-ci-trust-model.md)), so the device facts are committed.
  - Import (M5) writes a snapshot into `lab.yaml`, and keeps hand-written
    `listen:` entries.
- **Production never reads `lab.yaml`**, and a deploy never writes to it.
- **Device tags live only in `tofu/`**, even where production sets them
  with tagged auth keys or `--advertise-tags`. Import brings them under
  OpenTofu and says so (#36).
- **The policy's own `tests` and `sshTests` stay in `policy.hujson`.**
  Tailscale runs them on every save. tailsuite also runs them with real
  traffic, and #16 maps them to its own tests.
- **The key is `listen:`, not `services:`.** Tailscale Services already has
  a provider resource and an `autoApprovers.services` key.
- **Checked whenever a project loads:**
  - every device named in `tofu/` exists in `lab.yaml`;
  - every device has tags or a user;
  - names are unique.

### How tailsuite reads the module (amends 0004)

- **The values live in `tofu/tailnet.auto.tfvars`:** HCL, literal values
  only. tailsuite reads them with `hashicorp/hcl` and edits them with
  `hclwrite`, which keeps comments.
- **The logic lives in `tofu/tailsuite.tf`**, tailsuite's own module.

  ```hcl
  # tofu/tailnet.auto.tfvars (synthetic)
  devices = {
    server = { tags = ["tag:prod"] }
    db     = { tags = ["tag:prod", "tag:db"] }
  }
  ```

- **tailsuite reads the values itself:**
  - to build the model;
  - to give nodes their tags as they join, before tofu can look them up
    (throwaway tailnets hold tagged devices only);
  - to apply them on headscale.
- **The Tailscale engine and production run `tofu` as usual.**
- **headscale can apply only tailsuite's own module until #20.** Its runs
  are REFUSED, with the reason, when:
  - `tailsuite.tf` differs from the version tailsuite shipped;
  - or another `.tf` file declares an ACL, device-tag, route or DNS
    resource.

## Why

- **One vocabulary covers both engines.** headscale implements Tailscale's
  policy language. A neutral model would need its own design for grants,
  `via`, posture, autogroups and `nodeAttrs`, all for one real
  implementation, and the conformance check in 0002 compares Tailscale
  behaviour anyway.
- **Our own types are a forcing function.** A new Tailscale feature shows
  up as a listed key and a REFUSED run, never as a silent drop. Each one
  becomes an issue with tests.
- **Our own types can be fuzzed.**
  - Go's built-in fuzzing runs parse → patch → re-parse on every PR.
  - Differential fuzzing generates tailnets, runs them on both engines,
    and compares compiled filters and results (#42, on M3, before
    scorecard #1).
- **Deploy never changes a byte the user didn't change.** Re-encoding a
  typed copy would lose comments, key order and unknown keys.
- **The ownership rule gives every new key one home** without a new
  decision.
- **Reading the tfvars keeps M1 offline and PR runs secret-free**, without
  building #20's shim early.

## Evidence

[Research 0005](../research/0005-tailnet-model-trial.md) (2026-10-24) ran
three synthetic policies through a throwaway parser and headscale 0.29.5:

- **All three parsed.** Keys left out of the trial's types on purpose were
  listed, at the top level (`/attrConfig`) and nested
  (`/ssh/0/recorder`).
- **Every user reference was found.** The one inside a section the types
  skipped was flagged as one a rewrite would miss.
- **The patch was clean.** A users-to-tags patch kept every comment and
  changed only the two places it touched.
- **headscale rejected six features, one at a time:**
  - role autogroups as tag owners;
  - postures;
  - IP sets;
  - SSH session recording;
  - Funnel;
  - `attrConfig`.

  It also checks a policy's own tests against live devices.

## Alternatives

- **A vendor-neutral model** (identities, segments, rules), as the
  architecture said before this record. NetBird or ZeroTier would reuse
  the planner, at the cost above.
- **Tailscale's API types as the model** (`tailscale-client-go-v2`). The
  least code, but keys it doesn't know vanish, and there's nowhere to
  record where a fact came from. It stays the right format for the API
  source to read in M5.
- **headscale's policy types.** That means parsing with the implementation
  the conformance check is testing, so a headscale bug would look correct.
- **The typed copy as the truth, encoded again for engines and deploy.**
  It loses comments, key order and unknown keys.
- **Always pass unknown keys through, or always refuse them.** Passing
  through can give a false PASS or a false deny when a rewrite misses a
  user. Refusing always blocks Tailscale-engine runs that would have been
  true.
- **One record per device in a `tailnet.yaml`, read by the module with
  `yamldecode`.** One place per device, but tofu's inputs would live
  outside `tofu/`, beside facts that are never deployed. That's unusual
  for Terraform teams, and hard to import into their codebases.
- **A generated `tofu/`.** It contradicts 0004, and users editing
  generated files drift.
- **`tofu plan -json` for headscale** (0004 as written), **or building
  #20's shim in M1.** The first needs an API headscale doesn't have. The
  second is a lot of M1 scope.
- **headscale's community provider** (awlsring, v0.5.1, 2026-03-20). Its
  resources differ from the official provider's, so the lab would run a
  second module, a translation of production rather than the same code.

## Given up

- **"Vendor-neutral" leaves the architecture.** A second mesh VPN gets its
  own dialect and its own translation.
- **We keep types for the whole policy language**, and follow every
  Tailscale change.
- **A tagged device spans two files** (`lab.yaml` and the tfvars), joined
  by name.
- **A fact changes file if its owner changes**, say if the provider gains
  a device-IP resource. That move is a migration with a schema bump.
- **On headscale, a customized module can't be tested until #20.**
- **A team's own HCL (M5, #36) is read through `tofu plan -json` against
  production**, which needs a credential (#24).
- **`hashicorp/hcl` (MPL-2.0) becomes a dependency.**
