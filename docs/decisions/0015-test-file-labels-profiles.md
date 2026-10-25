# 0015: The test file, labels and profiles

Status: **accepted** (2026-10-25). Settles #16, and how results that
couldn't be proven affect CI (asked under #17). The maintainer settled the
product questions on 2026-10-25. The implementation details are this
record's. Refines the `profiles` sketch in
[0014](0014-tailnet-model-and-project-layout.md)'s `lab.yaml`.

## Context

Tests are the part of tailsuite users write by hand, and the hardest to
change once people rely on them. A test is an assertion about who can
reach what, proven with real traffic in the lab
([0001](0001-truth-over-mocks.md)).

Tailscale's policy file has its own `tests` and `sshTests` (checked
2026-10-25):

- **When they run:** Tailscale evaluates them against the rules on every
  save, and rejects a save that breaks one. No packets move.
- **What they name:** identities only (users, groups, tags, hosts, IPs). A
  group is checked through "a representative member". They can't name a
  device or a protocol, and have no labels.
- **An omitted `proto`** means "either TCP or UDP access".

## Decision

### What a test says

```yaml
# tests/startup.yaml (synthetic)
version: 1
tests:
  - name: engineers reach the web tier
    labels: [smoke]
    from: group:eng
    to: tag:prod
    port: 443
    expect: allow

  - name: laptops can't reach the database
    from: laptop
    to: db
    port: 5432
    expect: deny

  - name: root on prod needs a fresh login
    labels: [ssh]
    from: group:eng
    to: tag:prod
    ssh: root
    expect: check
```

- **Files:** YAML files in `tests/`, any number, each with `version: 1`.
- **Names:** every test has a `name`, unique in the suite. Results, labels
  and `--run` refer to tests by name.
- **`from` and `to`:** a device name from `lab.yaml`, or any identity the
  policy uses: a user, group, tag, host, autogroup or Tailscale IP.
  - A name that's both a device and a host is fine when the host's address
    is that device's IP, as when a policy aliases a device under its own
    name.
  - Any other overlap is an error that names both.
- **An identity means every matching device.**
  - Results list each device pair.
  - **A test's result combines its pairs:**
    - any FAIL makes it a FAIL;
    - otherwise, any REFUSED makes it REFUSED;
    - otherwise, any UNKNOWN makes it UNKNOWN;
    - it's a PASS only when every pair passed.

    The two sides of a condition combine the same way.
  - Once equivalence classes land (#22, M4), "every device" becomes one
    device per class, and the result says so.
  - **An identity that matches no device in the lab** makes the test fail
    to load (exit 3), naming the identity. With no device, there's nothing
    to prove.
- **The probe:** the real protocol of whatever listens on that port,
  taken from `lab.yaml`'s `listen:` entries (HTTP, SSH, Postgres…).
  - `probe: tcp` forces a bare connection, and later `udp` and `icmp`.
  - A test aimed at a port where nothing listens in the lab fails to load,
    and says which `listen:` entry to add.
  - **A test without `ssh:` asserts network access**, even on an SSH
    listener. `allow` means the server answered, and `deny` that it
    didn't. The login result counts only in a test with `ssh:`. The
    policy's own `tests` assert network access the same way.
- **`expect`:** `allow` or `deny`. An SSH test (`ssh: <login>`) can also
  expect `check`. SSH results are judged by what the server does, not by
  any engine's evaluation:
  - **`allow`** means the login was accepted with no check demanded. A
    demanded check fails an `expect: allow`.
  - **`check`** is proven when the server demands that the person
    re-authenticate. tailsuite never completes a check, because no person
    is there to log in.
  - **Today, a `check` can be proven only on headscale** ([0016](0016-engine-interface-capabilities.md)).
    Tailscale's docs say "an SSH access rule from a tagged device cannot be
    in check mode", and the Tailscale engine's throwaway tailnets hold only
    tagged devices.
- **Every deny gets a positive control automatically** (0001).
- **Conditions are tested both ways.** When a rule on the test's path
  depends on a device condition (posture, OS, client version), tailsuite
  runs the test from a device that meets it and from one that doesn't.
  - `expect` covers the device that meets the condition.
  - The other device's result is checked against what the policy says it
    should be, and a mismatch is a FAIL.
  - `when:` pins one case, for example `when: {os: windows}`.
  - **A case the lab can't build is UNKNOWN.** For example, `ip:country`
    comes from the device's public IP.
  - M1 has no posture, but the format carries it from version 1.

### Tailscale's own tests are first-class

- **They run in every suite**, with real traffic, labelled
  `source:policy`. They follow the same rules as tailsuite's own tests:
  every matching device, the real protocol, positive controls.
- **That's stricter than Tailscale's evaluation**, which uses one
  representative member. A policy test that passes on save and fails in
  the lab is exactly the gap tailsuite exists to find.
- **The mapping:**
  - `src` becomes `from`;
  - each `accept` or `deny` entry (`host:port`) becomes one test, with its
    `to`, `port` and `expect`;
  - `srcPostureAttrs` becomes `when`;
  - in `sshTests`, each `dst` and each login under `accept`, `check` or
    `deny` becomes one SSH test, with `accept` read as `allow` above.

  The generated names point at the entry, for example
  `policy tests[2]: alice@example.com → tag:prod:443`.
- **`proto` decides the probes.**
  - An omitted `proto` means TCP and UDP, as Tailscale reads it.
  - An `accept` passes when either half is allowed, and fails when both
    are blocked.
  - A `deny` passes when both are blocked, and fails when either is
    allowed.
  - Anything in between is UNKNOWN.
  - Until a protocol's probe exists (UDP and ICMP come after M1), that
    half is UNKNOWN. A `deny` with an omitted `proto` is therefore UNKNOWN
    in M1, never a PASS on TCP alone.
- **A policy test whose identity matches no lab device is UNKNOWN**,
  naming the identity. The user didn't write it for the lab, so it
  doesn't stop the suite from loading.
- **A policy test aimed at a port where nothing listens is UNKNOWN**, for
  the same reason, and says which `listen:` entry to add. Once UDP probes
  exist, that also applies to each protocol half.
- **Engines never run the policy's tests.** The engines' copies of the
  policy leave them out, because tailsuite runs them itself (0016).
- **Copying tests into the policy happens only when the user asks.**
  - A command (#12 names it) copies the tests that fit Tailscale's format
    into the policy's `tests` and `sshTests`, and shows the diff first.
    tailsuite never edits the policy on its own.
  - Tests that use a device name, a `probe`, labels, or a `when` that
    `srcPostureAttrs` can't express stay tailsuite-only.

### Labels and selection

- **`labels: [...]`** on any test, as free-form words. The field is called
  labels because in Tailscale a tag is a device identity (`tag:prod`).
- **tailsuite adds automatic labels**, with a prefix so they can't collide
  with the user's:
  - `source:tests`, `source:policy`, and `source:baseline` for the M2
    baseline pack (#32);
  - `probe:http`, `probe:ssh` and so on;
  - `needs:<capability>`, from 0016's matrix, for example `needs:posture`.
- **Selection:**
  - `--only <label>` (repeatable) runs a test that has any of those labels.
    One that matches no test is an error (exit 3), on the command line or
    in a profile, because a typo there would silently run nothing.
  - `--skip <label>` (repeatable) wins over `--only`. tailsuite warns when
    it names a label no test has.
  - `--run <regex>` matches test names, as `go test -run` does. One that
    matches no test is an error (exit 3) too.

### Profiles

Profiles are named ways to run, kept in `lab.yaml`:

```yaml
# lab.yaml (synthetic)
profiles:
  default: { engine: headscale }
  pr:      { engine: headscale, skip: [slow], unproven: report }
  nightly: { engine: tailscale, unproven: fail }
  ssh:     { engine: headscale, only: [ssh] }
```

- **A profile sets** `engine`, `only`, `skip` and `unproven`.
- **Choosing one:** `tailsuite test --profile pr`.
- **Flags override the profile for one run:** `--engine`, `--only`,
  `--skip`, `--run` and `--strict`.
- **Without `--profile`**, the profile named `default` applies. With none,
  that's headscale, every test, and `unproven: report`.
- **Different CI jobs run different profiles.** One job tests on
  headscale, another on the Tailscale engine, each with its own labels and
  strictness.

### Results and CI

- **Every test that runs ends PASS, FAIL, UNKNOWN or REFUSED** (0001). A
  test a profile or flag leaves out is counted as skipped, with the
  reason. It never disappears silently.
- **`unproven: report`** (the default):
  - a FAIL fails the run;
  - UNKNOWN and REFUSED results are listed separately in every output,
    each with the profile and engine that can prove it, if any;
  - neither ever counts as a pass.
- **`unproven: fail`**, or `--strict`, fails the run on them too.
- **A run that proved nothing fails, even under `report`**: no test ran,
  or none ended PASS or FAIL.
  - Positive controls don't count. Only the user's tests and the policy's
    tests do.
  - Otherwise a misspelt profile, or a policy every test of which is
    refused on the engine, would show green.
- **`tailsuite deploy`'s lab gate** ([0007](0007-read-only-by-default.md))
  **is always strict.**
- **Exit codes:**
  - 0: at least one test proven, no FAIL, and nothing unproven under
    strict;
  - 1: a FAIL;
  - 2: nothing proven, or unproven results under strict;
  - 3: tailsuite couldn't run, for example an invalid test file, an
    `--only` or `--run` that matches nothing, or a lab that didn't start
    or didn't become ready (0016).
- **Warnings:**
  - a test that no profile ever runs;
  - a test that every profile's engine refuses, so nothing ever proves it.
- **Outputs:** human-readable, JSON and JUnit (#8). In JUnit:
  - UNKNOWN and REFUSED are written as skipped, with the reason;
  - under strict, they're written as `<error>`, so they stay distinct from
    a FAIL, which is a `<failure>`.

### The baseline pack is #32's proof

#16 asked for the baseline pack to be written in this format as its proof.
That moves to #32 (M2), where the pack is built. It will probably need:

- port lists;
- `*` as a destination;
- a check kind with no traffic, for things like "Funnel enabled" and
  "key expiry disabled";
- findings that report without failing.

#32 settles them, in a new record if they change this format.

## Why

- **Users assert things about people and about machines**, so tests name
  both.
- **Every device catches the one that's different.** Sampling one member
  is how a policy test passes while someone is locked out, for example
  when a grant names Alice instead of the group.
- **Real protocols tell the truth about access.** Tailscale SSH needs both
  network access to port 22 and an SSH rule (Tailscale's docs, checked
  2026-10-25). A connection to port 22 can succeed while every login is
  refused.
- **`check` is a real outcome.** Tailscale's own `sshTests` already
  separate accept, check and deny. Without it, "root only after a fresh
  login" can't be tested.
- **Policy tests come free.** A team's existing tests are proven with real
  traffic on day one.
- **Automatic both-sides testing** covers the case nobody writes, the
  outdated device.
- **Labels and profiles** let different jobs run different tests on
  different engines with different strictness, the way Go uses build tags
  and `-run`.
- **Counting skipped tests, failing a run that proved nothing, and
  warning about never-proven tests** keep that flexibility from hiding
  anything.

## Evidence (checked 2026-10-25)

- **Tailscale's policy file syntax:**
  - `tests` has `src`, `srcPostureAttrs`, `proto`, and `accept` and
    `deny` as `host:port` lists;
  - an omitted `proto` checks "for either TCP or UDP access";
  - a group as a destination means "a representative member";
  - `sshTests` has `src`, `dst`, `accept`, `check`, `deny` and
    `srcPostureAttrs`;
  - "An SSH access rule from a tagged device cannot be in check mode."
- **Tailscale SSH needs both:** a rule allowing network access to port 22,
  and an SSH rule for the login.
- **Tailscale's geolocation posture attributes**, `ip:country` and
  `ip:publicAddress`, are derived from the device's public IP (posture
  attributes docs, last validated 2026-10-06).
- **headscale 0.29.5** (`hscontrol/policy/v2/test.go`):
  - an omitted `proto` matches TCP, UDP and ICMP;
  - its own `sshTests` count a check rule as satisfying `accept`;
  - its policy tests have no `srcPostureAttrs` field;
  - it checks a policy's own tests against live devices
    ([research 0005](../research/0005-tailnet-model-trial.md)).

## Alternatives

- **The whole suite in Tailscale's format.**
  - It can't name a device, check every device, or say which protocol.
  - It has no labels.
  - Tailscale rejects any policy save while one of its tests fails, so a
    slow or nightly-only test written there would block admins' edits.
- **Identities only, or devices only.** Each leaves out half of what users
  assert.
- **One representative member.** It misses the device that's different.
- **Always TCP.** It reports SSH as open when logins are refused, and
  proves half of a policy test with an omitted `proto`.
- **`check` counted as deny, or as UNKNOWN.** The first hides a real
  setting. The second gives up something the lab can prove.
- **Following headscale's reading of SSH `accept`**, which a check rule
  satisfies. "Allowed without a fresh login" and "allowed after one" would
  look the same.
- **Strict by default.** Teams with posture or Macs would get a
  pull-request gate that's always red, and would learn to ignore red.
- **No strict mode at all.** Nothing would ever force an unproven test to
  be proven somewhere.
- **Warning, not failing, on an `--only` that matches nothing.** A typo
  would mean a green run of nothing.
- **`tags:` as the field name.** It collides with Tailscale's device tags.

## Given up

- **More probes per test**, since every matching device is checked. M4's
  equivalence classes bound it.
- **Two test formats exist side by side**, tailsuite's and Tailscale's,
  with a mapping between them.
- **Tests copied into the policy lose** what Tailscale's format can't
  hold.
- **`expect: check` is provable only on headscale** until Tailscale's
  throwaway tailnets hold human users (#21).
- **In M1, a policy `deny` with an omitted `proto` is UNKNOWN**, until UDP
  probes exist.
- **A green run can still carry unproven tests** under the default. The
  separate list, the never-proven warning, and failing a run that proved
  nothing keep that visible.
- **Free-form labels can be mistyped.** An `--only` typo is an error, and
  a `--skip` typo is a warning.
