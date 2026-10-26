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
  count as understood: application capabilities, in grants and in
  `nodeAttrs` alike (app connectors among them), and `derpMap`. Numbers
  are kept exactly as written.
- **A key written twice:** the typed copy holds only the last occurrence.
  - Go's decoder would decode a repeated key *into* the earlier one:
    objects and maps merge, and list elements are reused. That gives a
    rule the file doesn't state.
  - So a first pass finds the overridden occurrences, they're removed
    from a copy, and the typed copy is decoded from that.
  - Each earlier occurrence is listed as a duplicate key, "overridden
    by" the last. An overridden occurrence of the wrong type doesn't stop
    the parse; the last one must have the right type.
  - Two keys are the same key when they land on one key of the typed
    copy. For a field, that's ignoring case (`acls`, `ACLs`). Map keys
    are case-sensitive, so `group:Eng` and `group:eng` are two groups.
- **Places under a name written twice exactly aren't addressable.** An
  RFC 6901 pointer can't tell two members of the same name apart, so no
  pointer is recorded there, and no reference is reported. The duplicate
  is in the unknown keys, so 0014 refuses any rewrite of identities
  anyway.
- **References list every place a name can appear**, with its pointer in
  the file's spelling, its kind and its role:
  - kinds: user, group, tag, autogroup, host, IP set, address, wildcard;
  - places:
    - rules' sources and destinations (`host:port` entries without their
      port), and `via`;
    - SSH recorders;
    - group members and tag owners, and the group and tag names
      themselves;
    - auto approvers;
    - `nodeAttrs` targets;
    - tests and SSH tests;
    - `attrConfig` broadcasts;
    - `externalTailnets` references.
  - Inside application payloads, every string shaped like a user, group,
    tag or autogroup, values and object keys alike, without any `:port`,
    so a rewrite can't miss one there.
  - Not covered: SSH logins (`users`), which are host accounts. Their
    `localpart:*@domain` form is refused by 0016.

### The project files

- **`lab.yaml` is read strictly**, as one YAML document. An unknown key,
  or a second document, is an error, since the file is tailsuite's and a
  typo would otherwise be ignored. Version 1 holds:
  - devices: `user`, `os`, `version` (the client's), `ip` (a pinned
    Tailscale IPv4), `listen`;
  - users, by login, with their roles (`admin`, `it-admin`…), which
    0016 lowers role autogroups to on headscale;
  - profiles (0015): `engine`, `only`, `skip`, `unproven`.
- **Logins and tags are checked by shape:** a login is `local@domain`,
  with no `:` before the `@` (so not `group:…` or `tag:…`), and a tag is
  `tag:` and a name of letters, digits and hyphens. A tag or a role given
  twice is an error.
- **`listen` takes only what the node agent serves:** `tcp` and `http`
  (decision 0020, in #53). Others fail to load, naming #56, rather than
  giving a test a port nothing can listen on.
- **`tofu/tailnet.auto.tfvars` takes literal syntax only**, stricter than
  OpenTofu, which also evaluates constant expressions:
  - objects with literal keys, written once each; lists; strings with no
    interpolation; numbers and booleans;
  - a variable, a function call, a template, a `for` expression, a
    conditional or a computed key is an error;
  - #9 edits the file with `hclwrite`, which needs what's written to be
    what's meant.
  - It may set only `devices`, and each device only `tags`. DNS,
    settings and routes join as their milestones land.
- **Without the tfvars file**, no device has tags, and the load checks
  then ask every device for a user.
- **Every problem in a load is reported at once**, each naming its file
  and device.

### The model

- **Devices and users**, each with its origin. Users come from
  `lab.yaml`'s users, and from its devices' users.
- **`model.json`** goes through a generic value before it's written, so
  every object's keys come out sorted, struct fields included, with
  numbers kept exact.

## Why

- **Removing the overridden occurrences** makes the typed copy say what
  the file says, rather than a merge no policy states.
- **No pointer rather than a wrong one.** A pointer to the first of two
  same-named members would make a patch change the occurrence the typed
  copy didn't read.
- **Searching payloads for identities** keeps 0014's dangerous case, a
  rewrite that misses a user, from hiding where tailsuite doesn't
  interpret the data.
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
- **Repeated keys no longer merge.** The review's three inputs (`acls`
  and `ACLs`, two `groups`, two `autoApprovers`) each give the last
  occurrence alone. With the removal step taken out, that test fails.
- **The reference's own app-connector example**, `routes` included, is
  understood, and its connector's tag is found. A user inside a grant's
  application payload is found, as a value or as a key, and a tag there
  loses its `:port`.
- **Fuzz tests** (parse, list unknown keys, parse again, resolve every
  reference) ran 5.7 million inputs, 2 million more after the first
  review's changes, and 2.4 million after the second's, with no failure. The first run found the repeated-name case
  above within seconds; that input stays as a regression case.
- **Unit tests** cover:
  - capitalised keys, duplicates, case-distinct map keys, and an
    overridden occurrence of the wrong type;
  - both posture syntaxes, and bad shapes refused;
  - the startup project loading, with every load check failing on its
    own input, a second YAML document and bad logins, tags and roles
    included;
  - tfvars with each kind of expression, or a repeated name, refused;
  - `model.json`: deterministic, sorted, and a 2⁵³+1 number kept.

## Alternatives

- **Make a repeated key a parse error.** Simpler, but it refuses a whole
  file over one key, where 0014 lists what it can't read.
- **Point at the first of two same-named members.** It points a patch at
  the wrong place.
- **Lenient `lab.yaml`**, ignoring unknown keys. Typos would pass.
- **OpenTofu's evaluation for tfvars.** It accepts constant expressions
  that `hclwrite` edits would then have to preserve or rewrite.
- **Accept any listener protocol**, and fail later. The failure would
  come at probe time, far from the line that caused it.

## Given up

- **A policy that repeats a key** loses its identity rewrites, which are
  REFUSED until the duplicate goes. That's rare, and Tailscale's own
  handling of it is unspecified.
- **A payload string that only looks like an identity** is reported as
  one. A rewrite may then refuse, or rewrite it, where Tailscale wouldn't
  have read it as one.
- **tfvars files that OpenTofu accepts can fail to load** if they use
  constant expressions.
- **Every new Tailscale policy field needs a type change**, or it shows
  as unknown. That's 0014's forcing function.
- **New listener protocols need the agent first** (#56).
