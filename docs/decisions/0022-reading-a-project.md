# 0022: Reading a project: the policy's edge cases, and strict project files

Status: **accepted** (2026-10-25). Built in #58. Fills in the details
[0014](0014-tailnet-model-and-project-layout.md) left to the code.

## Context

0014 settled the model and the project layout:

- the policy is the user's file plus a read-only typed copy;
- keys the typed copy doesn't understand are listed;
- patch pointers use the file's own spelling;
- `lab.yaml` and `tofu/tailnet.auto.tfvars` each own their facts;
- a load checks that devices line up.

Building it raised cases 0014 doesn't name.

## Decision

### The policy

- **The typed copy covers a superset of two sources:** Tailscale's policy
  file reference and the official Go client's `ACL` type (v2.12.0), both
  checked 2026-10-25. The reference adds `sshTests`, the object posture
  syntax, and SSH's `acceptEnv` and `srcPosture`; the client adds a few
  network options.
- **Postures in either syntax:** the object form (`assertions`,
  `onFailure`) and the legacy list. Each is encoded again in the syntax
  it was read in.
- **Payloads tailsuite doesn't interpret are carried as written**, and
  count as understood: a grant's application capabilities, and
  `derpMap`.
- **A key written twice:** Go's decoder keeps the last occurrence, so the
  typed copy reflects the last. Each earlier occurrence is listed as a
  duplicate key, "overridden by" the last.
  - Two keys are the same key when they land on one key of the typed
    copy. For a field, that's ignoring case (`acls`, `ACLs`). Map keys
    are case-sensitive, so `group:Eng` and `group:eng` are two groups.
- **Places under a name written twice exactly aren't addressable.** An
  RFC 6901 pointer can't tell two members of the same name apart, so no
  pointer is recorded there, and no reference is reported. The duplicate
  is in the unknown keys, so 0014 refuses any rewrite of identities
  anyway.
- **References know every place a name can appear**, with its pointer in
  the file's spelling, its kind and its role:
  - kinds: user, group, tag, autogroup, host, IP set, address, wildcard;
  - places: rules' sources and destinations (`host:port` entries without
    their port), `via`, SSH recorders, group members, tag owners, auto
    approvers, `nodeAttrs` targets, tests and SSH tests, and `attrConfig`
    broadcasts.

### The project files

- **`lab.yaml` is read strictly.** An unknown key is an error, since the
  file is tailsuite's and a typo would otherwise be ignored. Version 1
  holds:
  - devices: `user`, `os`, `version` (the client's), `ip` (a pinned
    Tailscale IPv4), `listen`;
  - profiles (0015): `engine`, `only`, `skip`, `unproven`.
- **`listen` takes only what the node agent serves:** `tcp` and `http`
  (0020). Others fail to load, naming #56, rather than giving a test a
  port nothing can listen on.
- **`tofu/tailnet.auto.tfvars` is read with no evaluation context**, so a
  variable or a function call is an error, as OpenTofu makes it. It may
  set only `devices`, and each device only `tags`. DNS, settings and
  routes join as their milestones land.
- **Without the tfvars file**, no device has tags, and the load checks
  then ask every device for a user.
- **Every problem in a load is reported at once**, each naming its file
  and device.
- **`model.json`** goes through a generic value before it's written, so
  every object's keys come out sorted, struct fields included.

## Why

- **Flagging the overridden occurrence** names the key the typed copy
  ignored, which is what a reader needs to fix.
- **No pointer rather than a wrong one.** A pointer to the first of two
  same-named members would make a patch change the occurrence the typed
  copy didn't read.
- **Strict project files** turn a typo into an error with a line number,
  instead of a lab that quietly differs from what the user wrote.
- **Listeners limited to the agent's** keep a test from aiming at a port
  that can never answer.

## Evidence (2026-10-25)

- **Research 0005's three policies** parse with nothing unknown. The byte
  offsets of `/grants/0` match what 0005's trial measured with another
  parser: 174–215, 405–467, 623–722.
- **Every user 0005 counted is found:** 6 in the segmented policy, 4 in
  the other, including the one in `attrConfig`.
- **A fuzz test** (parse, list unknown keys, parse again, resolve every
  reference) ran 5.7 million inputs in 3 minutes with no failure. Its
  first run found the repeated-name case above within seconds; that
  input stays as a regression case.
- **Unit tests** cover:
  - capitalised keys, duplicates, and case-distinct map keys;
  - both posture syntaxes;
  - bad shapes, refused;
  - the startup project loading, with every load check failing on its
    own input;
  - tfvars with a function, a variable, another variable, or another
    value, each refused;
  - `model.json`, deterministic and sorted.

## Alternatives

- **Flag the later duplicate** as the "second" one. It names the key the
  typed copy actually read, and hides the one it ignored.
- **Point at the first of two same-named members.** It points a patch at
  the wrong place.
- **Lenient `lab.yaml`**, ignoring unknown keys. Typos would pass.
- **Accept any listener protocol**, and fail later. The failure would
  come at probe time, far from the line that caused it.

## Given up

- **A policy that repeats a key** loses its identity rewrites, which are
  REFUSED until the duplicate goes. That's rare, and Tailscale's own
  handling of it is unspecified.
- **Every new Tailscale policy field needs a type change**, or it shows
  as unknown. That's 0014's forcing function.
- **New listener protocols need the agent first** (#56).
