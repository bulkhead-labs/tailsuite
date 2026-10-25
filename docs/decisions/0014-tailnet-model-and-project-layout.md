# 0014: The tailnet model, and which project file owns each fact

Status: **accepted** (2026-10-24). Settles #15 and #18. Amends
[0004](0004-opentofu-owns-config.md) twice:

- On headscale, M1 reads the module's values from
  `tofu/tailnet.auto.tfvars`, not from `tofu plan -json`.
- A lab applies the module from its own run directory, and on the
  Tailscale engine with the engine's copy of the policy, so the policy
  file it reads differs from production's.

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
    - A key counts as understood when it matches a known key ignoring
      case, because older policies capitalise keys (`ACLs`, `Groups`), as
      the official client's `hujson` tags show.
    - A known key at its zero value (`"tests": []`, `false`) counts as
      understood. The comparison never relies on `omitempty`.
  - **Each engine gets the user's file plus an RFC 6902 patch**, applied
    with `tailscale/hujson`, which keeps comments. The patch is the record
    of rewrites that [0002](0002-two-engines.md) requires, and is shown
    with every run.
    - Patch pointers come from the file's own keys (hujson `Find`), never
      from the typed copy, so `/Groups/…` stays `/Groups/…`.
    - A patch defines every tag it introduces, with its owners.
      headscale rejects a tag owned by an undefined tag (research 0005).
    - **On the Tailscale engine, the patched copy is what tofu applies.**
      `tailsuite.tf` always reads `../policy.hujson`, and has no input
      that could point it elsewhere. A lab run puts the engine's copy at
      that path in its own run directory (below). So production always
      gets the user's own file, and on that engine 0004's "same module"
      is the same module reading a different file.
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
| Which devices exist, who added each, OS, client version, IP; later advertised routes, exit node, SSH server, posture attributes | The device, or the control plane (IPs, owners). No provider resource sets them, so tailsuite never deploys them | `lab.yaml` |
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

- **One copy of the policy.** The module always reads `../policy.hujson`.
  A lab run puts the engine's copy at that path in its own run directory.
- **The project alone rebuilds the lab, with no access to production.**
  - On headscale it needs no credentials at all. PR runs there have no
    secrets ([0013](0013-ci-trust-model.md)), so the device facts are
    committed.
  - The Tailscale engine needs only the test organization's OAuth client,
    never production's.
  - Pulling the pinned lab images needs the network.
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

  ```hcl
  # tofu/tailsuite.tf (excerpt)
  terraform {
    required_providers {
      tailscale = {
        source  = "tailscale/tailscale"
        version = "0.29.2" # exact: the lab and production run the same provider
      }
    }
  }

  resource "tailscale_acl" "policy" {
    acl = file("${path.module}/../policy.hujson") # no input changes this path
  }
  ```

- **The module's only inputs are its values**, so nothing can point it at
  another policy.
  - `tailsuite.tf` declares no variable that names a file or a path.
  - `tailnet.auto.tfvars` sets only the variables `tailsuite.tf` declares:
    `devices` in M1, and DNS and settings as their milestones land.
- **The provider is pinned to an exact version in `tailsuite.tf`**, so the
  lab and production run the same one, and results say which. Whether
  `tofu plan` checks the policy against `/acl/validate` depends on that
  version (research 0005).
- **tailsuite reads the values itself:**
  - to build the model;
  - to give nodes their tags as they join, before tofu can look them up
    (throwaway tailnets hold tagged devices only);
  - to apply them on headscale.
- **A lab run never runs tofu in the user's `tofu/`.**
  - That folder holds production's provider settings and state backend.
    A run there could write the lab's policy to the production tailnet,
    and the lab's state over production's ([0007](0007-read-only-by-default.md)).
  - Instead, each Tailscale-engine run builds its own directory under
    `.tailsuite/`:

    ```
    .tailsuite/run-<id>/
      policy.hujson          # the engine's copy, where tailsuite.tf looks
      tofu/
        tailsuite.tf         # copied from tofu/ and hash-checked
        tailnet.auto.tfvars  # copied from tofu/
        lab.tf               # tailsuite's: the throwaway tailnet's `tailnet` and `base_url`, local state
        .terraform.lock.hcl  # the lock file for this tailsuite.tf's release
    ```

  - **tofu starts from an empty environment**, plus an allow-list:
    - `PATH`, `HOME`, and proxy and CA settings;
    - `TF_CLI_CONFIG_FILE`, pointing at tailsuite's own CLI config;
    - the throwaway tailnet's credentials, in
      `TAILSCALE_OAUTH_CLIENT_ID` and `TAILSCALE_OAUTH_CLIENT_SECRET`.

    Nothing the user's shell sets reaches it: no `TF_VAR_*`, no
    `TF_CLI_ARGS*`, and no other provider credentials.
  - **The throwaway tailnet's API credentials never touch disk.** The run
    directory is deleted at teardown, even after a failure.
  - On headscale nothing runs tofu at all.
- **Production runs `tofu` in `tofu/` as usual**, with the user's provider
  settings and backend.
- **A lab applies only tailsuite's own module, on either engine.** Until
  #20, headscale can't apply anything else, and the Tailscale engine's run
  directory holds nothing else. Anything else in `tofu/` that changes what
  production applies would make the lab differ from production without
  tailsuite seeing it.

  So the check is an allow-list. It covers every file OpenTofu would load
  from `tofu/`, by the rules of the OpenTofu version tailsuite pins:
  - `.tf`, `.tf.json`, `.tofu` and `.tofu.json` files (a `.tofu` file
    replaces the `.tf` file of the same name);
  - override files, in every extension;
  - variables files (`.tfvars`, `.tfvars.json`).

  A lab run is REFUSED, with the reason, unless those files are only:
  - `tailsuite.tf`, as shipped by this or an earlier tailsuite release,
    with no `.tofu` twin;
  - `tailnet.auto.tfvars`, setting only the variables `tailsuite.tf`
    declares;
  - other configuration files that apply nothing, none of them an override
    file.
    - They may hold `terraform` blocks (production's backend), `provider`
      blocks (its credentials), `variable`, `locals` and `output` blocks,
      `import` blocks for `tailsuite.tf`'s resources, and data sources and
      ephemeral resources that only read, such as credential lookups.
    - They may not hold a `resource` or `module` block of any provider.
    - They may not use an `external` or `http` data source, which can run
      commands or send requests.
  - other variables files that set no variable `tailsuite.tf` declares.
    A `*.auto.tfvars` file would override the values in lexical order, and
    any other would reach production through `-var-file`.

  Other files, such as `.terraform.lock.hcl`, `.terraform/`, state files
  or a README, are ignored. Values passed on the command line aren't in
  the project, and `tailsuite deploy` (M6) passes none.

### Export and the round trip

- **`tailsuite export tofu` writes `tofu/` from the model:** the shipped
  `tailsuite.tf` and a `tailnet.auto.tfvars`. `init` uses it for templates
  and `import` (M5) for a real tailnet. After that, the user owns the
  files. This isn't the rejected generated `tofu/` below: export writes
  once, and tailsuite never regenerates the files on a run.
- **Upgrading `tailsuite.tf`.**
  - The binary carries the hash of every release's `tailsuite.tf`, with
    that release's lock file. So a copy from an earlier release still
    counts as shipped, its run directory gets the matching lock file, and
    results say which version ran.
  - When the user asks, tailsuite replaces `tailsuite.tf` with the
    current release's copy and leaves the values alone. #9 names the
    command.
- **The round trip (#10):** export from a lab's model into a fresh
  project, rebuild the lab from it, and the same suite gives the same
  results.
  - On the Tailscale engine, `tofu apply` applies the export.
  - On headscale, tailsuite reads the export back and applies it. That
    proves export and read-back lose nothing.

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
- **The patch kept everything the user wrote, and showed a rule it has to
  follow.** A users-to-tags patch kept every comment and changed only the
  two places it touched. headscale then rejected the patched file: the new
  tag's owner, `tag:tailsuite`, wasn't defined. So a patch defines every
  tag it introduces, with its owners.
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
- **Running a lab in a copy of every file in `tofu/`, with override files
  for the provider and the backend.** It would let a team's own
  `tailscale_*` resources reach the Tailscale engine. But every provider
  alias, module and data source would have to be redirected correctly, a
  miss writes to production, and headscale still couldn't apply them.
- **A `policy_file` variable that points the module at the engine's copy**
  (an earlier draft of this record). Production could set it too, from a
  variables file, and the lab would then test a policy production doesn't
  apply. A fixed path, with the copy placed there in the run directory,
  leaves nothing to set.
- **A guard that lists what to refuse** (another earlier draft). Each
  review found a file type or block it missed: other variables files,
  `module` blocks, other `tailscale_*` resources, `.tofu` files. An
  allow-list fails safe.
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
- **An auto-approved route is stated twice.** The provider requires a
  device's enabled routes in `tofu/` to include the routes `autoApprovers`
  approved, or the plan drifts. So such a route appears in `policy.hujson`
  as a rule and in `tofu/` as state. Once advertised routes land, a load
  check keeps the two in step (#44, M3).
- **A team's own resources in `tofu/`, of any provider, make lab runs
  REFUSED.**
  - That holds until tailsuite's module covers them, as DNS and settings
    will when their milestones land, or until #36 (M5) brings in teams'
    own HCL.
  - Teams that script around the provider's gaps, such as device IPs or
    posture attributes, must keep those scripts outside `tofu/` to use the
    lab.
- **Data sources and ephemeral resources are trusted to only read.**
  tailsuite refuses `external` and `http` data sources, but doesn't try to
  prove that another provider's have no side effects.
- **On the Tailscale engine, tofu applies the engine's copy of the
  policy**, not the user's file. The module is the same, the file at
  `../policy.hujson` differs, and the patch says how.
- **Production takes tailsuite's provider version.** It's pinned exactly
  in `tailsuite.tf`, so moving to a new provider means upgrading
  `tailsuite.tf`.
- **A fact changes file if its owner changes**, say if the provider gains
  a device-IP resource. That move is a migration with a schema bump.
- **A customized `tailsuite.tf` can't be tested in the lab**, because it
  isn't a shipped copy.
- **A team's own HCL (M5, #36) is read through `tofu plan -json` against
  production**, which needs a credential (#24).
- **`hashicorp/hcl` (MPL-2.0) becomes a dependency.**
