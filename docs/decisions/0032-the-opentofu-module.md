# 0032: The OpenTofu module, the allow-list, and the run directory

Status: **accepted** (2026-10-26). Builds
[0014](0014-tailnet-model-and-project-layout.md)'s "How tailsuite reads
the module" for #9.

## Context

- 0014 settled the layout: `tofu/tailsuite.tf` holds the logic and always
  reads `../policy.hujson`; `tofu/tailnet.auto.tfvars` holds literal
  values; a lab never runs tofu in the user's `tofu/`, and applies only
  tailsuite's module, checked by an allow-list over every file OpenTofu
  would load.
- The Tailscale engine (#6) is what runs `tofu apply`. Until it lands,
  the run directory can be built and validated, not applied.
- The CLI (#12) is in review on #77. `init` writing `tailsuite.tf`, the
  `upgrade tofu` command, and a lab run turning the allow-list's problems
  into REFUSED are wired once it has merged.

## Decision

### The module

- **`tailsuite.tf`** (`internal/tofu/shipped/current/`):
  - pins the Tailscale provider at **0.29.2** (2026-05-26, the newest);
  - sets the policy with `tailscale_acl` from `file("${path.module}/../policy.hujson")`;
  - declares one variable, `devices`: a map from device name to
    `{ tags = [...] }`;
  - finds each tagged device by its short MagicDNS name, with the
    `tailscale_devices` data source and `name_prefix = "<name>."`, and a
    postcondition that exactly one device matched; then sets its tags
    with `tailscale_device_tags` on that device's `node_id`. A device's
    full name is `<short name>.<tailnet domain>`, so the prefix fits the
    lab and production, whose domains and IDs differ. (`tailscale_device`'s
    `name` needs the full name, and `hostname` is the machine's, which
    can differ from its MagicDNS name.)
- **Its lock file** pins the provider's hashes for linux and darwin on
  amd64 and arm64, and windows on amd64, made with `tofu providers lock`.
- **Every release's module ships in the binary**, by its SHA-256, with its
  lock file: `shipped/current/` is this build's, and the release pipeline
  (#2) adds `shipped/<version>/`. A `tailsuite.tf` that matches none,
  an edited one included, isn't shipped.

### The allow-list

`tofu.CheckProject` reads `tofu/` by OpenTofu **1.13.1**'s file rules
(2026-10-01; 1.13.2 is under two weeks old), the version the dev shell
has. Each problem is a reason a lab run is REFUSED:

- `tailsuite.tf` missing, or not one a release shipped;
- a file OpenTofu would load beside or instead of it: `tailsuite.tofu`,
  `tailsuite.tf.json`, `tailsuite.tofu.json`;
- an override file (`override.*`, `*_override.*`), in any extension;
- in any other configuration file (`.tf`, `.tofu`, and their `.json`
  forms), any block but `terraform`, `provider`, `variable`, `locals`,
  `output`, `import`, `data` and `ephemeral`: so `resource`, `module`,
  `moved`, `removed`, `check` and unknown blocks are refused. A `.tf`
  with a `.tofu` twin is checked too: OpenTofu skips it, but Terraform
  (0004) reads no `.tofu` file and applies it;
- an `external`, `http` or `local_command` data source or ephemeral
  resource, which run commands or send requests;
- OpenTofu's `external` key provider or method in a `terraform` block's
  state encryption, which run a command;
- an `import` block into anything but `tailscale_acl.policy`. Importing
  a device's tags would make production replace that resource, and clear
  the old device's tags, which the lab never does;
- `tailnet.auto.tfvars` setting a variable `tailsuite.tf` doesn't
  declare, or any other variables file setting one it does;
- a file that doesn't parse.

Hidden configuration files and editor leftovers are skipped, as OpenTofu
skips them. Hidden variables files are checked: OpenTofu loads
`*.auto.tfvars` by suffix alone, hidden or not. The lock file,
`.terraform/`, state and a README are never read.

A unit test checks that `tofu.Variables` is exactly the variables the
shipped `tailsuite.tf` declares.

### The run directory

- **`tofu.PrepareRun`** runs the check, then builds
  `.tailsuite/run-<id>/` (0700, files 0600) with the engine's copy of the
  policy, the project's `tailsuite.tf` and values, tailsuite's `lab.tf`
  (the throwaway tailnet's name and API URL, local state) and the
  shipped release's lock file. Nothing else from `tofu/` is copied, so
  production's backend and provider settings never reach it. `Remove`
  deletes it.
- **tofu's whole environment** is an allow-list of tailsuite's own
  (`PATH`, `HOME`, `TMPDIR`, proxies, `SSL_CERT_FILE`, `SSL_CERT_DIR`),
  plus `TF_CLI_CONFIG_FILE` pointing at the run's empty `tofu.rc` (so no
  user CLI config is read), `TF_IN_AUTOMATION`, `TF_INPUT=0`, and the
  throwaway tailnet's OAuth client. No `TF_VAR_*`, `TF_CLI_ARGS*` or other
  credentials pass.
- **`Run.Apply`** runs `tofu init -lockfile=readonly`, so the shipped lock
  file is the only one that counts, then `tofu apply -auto-approve`, both
  with `-no-color`. `lab.tf`'s strings are written with HCL's own
  escaping.

### Editing the values

- **`project.SetDevices`** rewrites `devices` in a values file with
  `hclwrite`, keeping the rest of the file and its comments. The map is
  written whole, in name order. It edits only a file `ReadTFVars` reads.
- `hclwrite` brings one new module, `github.com/google/go-cmp` v0.7.0
  (2025-01-14).

## Why

- **Finding devices by name** is what lets one module serve a lab and
  production (#9).
- **An allow-list by OpenTofu's own rules** catches every way a project
  could apply more than tailsuite sees: overrides, `.tofu` twins, extra
  resources, a value set twice.
- **A run directory built from the shipped module and lock file** can't
  pick up production's backend, provider settings or credentials.

## Evidence (checked 2026-10-26)

- The provider's schema and source (0.29.2): `tailscale_device`'s `name`
  is the full name, matched exactly by the API; `tailscale_devices`'
  `name_prefix` is a prefix match on that full name
  (`data_source_devices.go`); each device exports `node_id`;
  `tailscale_device_tags` takes `device_id` and `tags`; `tailscale_acl`
  takes `acl`.
- OpenTofu loads a hidden `.prod.auto.tfvars` (the reviewer's run, on
  1.13.2).
- `tofu init -lockfile=readonly` and `tofu validate` in a run directory
  `PrepareRun` built, with its scrubbed environment, under OpenTofu
  1.13.1 (`go test -tags tofu ./internal/tofu/`): pass.

## Given up

- **Comments inside the `devices` map** aren't kept on an edit; removing
  the map removes the comment lines directly above it.
- **`check` blocks are refused**, though one holding only assertions
  applies nothing: telling them apart is more than the M1 lab needs.
- **The run directory is applied only once the Tailscale engine lands**
  (#6). Until then it's validated, not applied.
