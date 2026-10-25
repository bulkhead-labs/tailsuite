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

- Tailscale evaluates them against the rules on every save, and rejects a
  save that breaks one. No packets move.
- A test names identities only: users, groups, tags, hosts, IPs. It
  checks a group through "a representative member".
- It can't name a device or a protocol, and it has no labels.

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
  policy uses: a user, group, tag, host, autogroup or Tailscale IP. A name
  that could be both a device and a host is an error that names both.
- **An identity means every matching device.**
  - Results list each device pair.
  - Once equivalence classes land (#22, M4), "every device" becomes one
    device per class, and the result says so.
- **The probe:** the real protocol of whatever listens on that port,
  taken from `lab.yaml`'s `listen:` entries (HTTP, SSH, Postgres…).
  - `probe: tcp` forces a bare connection, and later `udp` and `icmp`.
  - A test aimed at a port where nothing listens in the lab fails to load,
    and says which `listen:` entry to add. With nothing listening, there's
    nothing to prove.
- **`expect`:** `allow` or `deny`. An SSH test (`ssh: <login>`) can also
  expect `check`.
  - `check` is proven when the server demands that the person
    re-authenticate.
  - tailsuite never completes a check, because no person is there to log
    in.
- **Every deny gets a positive control automatically** (0001).
- **Conditions are tested both ways.** When a rule on the test's path
  depends on a device condition (posture, OS, client version), tailsuite
  runs the test from a device that meets it and from one that doesn't.
  - `expect` covers the device that meets the condition.
  - The other device's result is checked against what the policy says it
    should be, and a mismatch is a FAIL.
  - `when:` pins one case, for example `when: {os: windows}`.
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
  - `proto` becomes the probe;
  - `srcPostureAttrs` becomes `when`;
  - in `sshTests`, each `dst` and each login under `accept`, `check` or
    `deny` becomes one SSH test.

  The generated names point at the entry, for example
  `policy tests[2]: alice@example.com → tag:prod:443`.
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
  - `needs:<capability>`, from [0016](0016-engine-interface-capabilities.md)'s
    matrix, for example `needs:posture`.
- **Selection:**
  - `--only <label>` (repeatable) runs a test that has any of those labels.
  - `--skip <label>` (repeatable) wins over `--only`.
  - `--run <regex>` matches test names, as `go test -run` does.
  - tailsuite warns when `--only` or `--skip` names a label no test has.

### Profiles

Profiles are named ways to run, kept in `lab.yaml`:

```yaml
# lab.yaml (synthetic)
profiles:
  default: { engine: headscale }
  pr:      { engine: headscale, skip: [slow], unproven: report }
  nightly: { engine: tailscale, unproven: fail }
  ssh:     { engine: tailscale, only: [ssh] }
```

- **A profile sets** `engine`, `only`, `skip` and `unproven`.
- **Choosing one:** `tailsuite test --profile pr`. Flags override the
  profile for one run.
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
  - only a FAIL fails the run;
  - UNKNOWN and REFUSED results are listed separately in every output,
    each with the profile and engine that can prove it, if any;
  - neither ever counts as a pass.
- **`unproven: fail`**, or `--strict`, fails the run on them too. That's
  for nightly runs and release gates.
- **Exit codes:**
  - 0: no FAIL, and nothing unproven under strict;
  - 1: a FAIL;
  - 2: unproven results under strict;
  - 3: tailsuite couldn't run, for example an invalid test file or a lab
    that didn't start.
- **Warnings:**
  - a test that no profile ever runs;
  - a test that every profile's engine refuses, so nothing ever proves it.
- **Outputs:** human-readable, JSON and JUnit (#8). JUnit has no
  "unproven", so UNKNOWN and REFUSED are written as skipped with the
  reason, or as failures under strict.

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
  and `-run`. Counting skipped tests and warning about never-proven ones
  keeps that flexibility from hiding anything.

## Evidence (checked 2026-10-25)

- **Tailscale's policy file syntax:**
  - `tests` has `src`, `srcPostureAttrs`, `proto`, and `accept` and
    `deny` as `host:port` lists;
  - a group as a destination means "a representative member";
  - `sshTests` has `src`, `dst`, `accept`, `check`, `deny` and
    `srcPostureAttrs`.
- **Tailscale SSH needs both:** a rule allowing network access to port 22,
  and an SSH rule for the login.
- **headscale 0.29.5 checks a policy's own tests against live devices**
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
- **Always TCP.** It reports SSH as open when logins are refused.
- **`check` counted as deny, or as UNKNOWN.** The first hides a real
  setting. The second gives up something the lab can prove.
- **Strict by default.** Teams with posture or Macs would get a
  pull-request gate that's always red, and would learn to ignore red.
- **No strict mode at all.** Nothing would ever force an unproven test to
  be proven somewhere.
- **`tags:` as the field name.** It collides with Tailscale's device tags.

## Given up

- **More probes per test**, since every matching device is checked. M4's
  equivalence classes bound it.
- **Two test formats exist side by side**, tailsuite's and Tailscale's,
  with a mapping between them.
- **Tests copied into the policy lose** what Tailscale's format can't
  hold.
- **A green run can still carry unproven tests** under the default. The
  separate list and the never-proven warning are what keep that visible.
- **Free-form labels can be mistyped.** The warning for a label no test
  has catches most of it.
