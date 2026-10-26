# 0006: Tailscale's throwaway tailnets, end to end

Run on 2026-10-25 against the dedicated Tailscale test organization (#3).
The questions:

- do Tailscale's API-only tailnets do what the docs say, on a real
  organization;
- the two checks [0016](../decisions/0016-engine-interface-capabilities.md)
  gave this spike: how Tailscale reads `{"grants": []}`, and whether the
  readiness marker survives beside an allow-all grant.

Evidence for #6, the Tailscale engine, and for
[0021](../decisions/0021-empty-grants-and-the-marker-on-tailscale.md).

**Verdict: go.** Every step of the engine's flow works, both of 0016's
checks came out in the engine's favour, and six findings change how #6
has to be built (below).

## How it was run

- **Throwaway programs** (not committed), using only Python's standard
  library, ran seven times. The tables name each run.
  - **Run 1** stopped early on a bug in the program, after creating its
    tailnet. Its cleanup still deleted it.
  - **Runs 2–4** ran the whole flow below.
  - **Run 5** ran 0016's checks and the review's questions.
  - **Run 6** only validated policies.
  - **Run 7** validated the check that 0021 puts in every Tailscale-engine
    run.
- **The whole flow:**
  1. create an API-only tailnet with the test organization's OAuth client
     (scope `tailnets` only);
  2. take a token for the new tailnet from the OAuth client that the
     creation returned;
  3. set a policy, and mint two tagged, ephemeral, pre-authorized,
     single-use auth keys;
  4. join two Docker nodes, each running real tailscaled in kernel TUN
     mode under tailsuite's node agent (#7, in #53, not merged yet);
  5. probe between them with the agent;
  6. change the policy, set a custom posture attribute, and pin a node's
     IPv4 address, timing how long real traffic took to change;
  7. delete the tailnet.
- **The nodes:** `tailscale/tailscale:v1.102.5`
  (`sha256:c507f3a2a6ab1cabd8d809b98edeb41edbd5c3fb6ad9632ffd098b4c7d0b4065`),
  on a 4 vCPU, 8 GB VM with Docker 26.1.5, 128 MiB each.
- **The policy:** synthetic. `tag:spike-a` may reach `tag:spike-b` on TCP
  8080. TCP 8082 is allowed only from a device whose `custom:spike`
  attribute is `ok`. TCP 8081 is allowed or not, as the test needs.
- **Secrets:** the programs kept every credential in their own memory.
  - Auth keys reached the nodes on stdin (`tailscale up
    --auth-key=file:/dev/stdin`), never in an argument, environment
    variable or file.
  - No output contained a credential, and none is recorded here.
- **Where traffic went:**
  - Probes went only to the other node's Tailscale address.
  - The nodes' egress wasn't restricted or recorded.
  - In runs 2–4, tailscaled ran with its default logging, so it also sent
    its client logs to Tailscale. Run 5 used `--no-logs-no-support`.

## Results

### The API

| Step | Result | Time |
|---|---|---|
| Token for the organization's client | 200, scope `tailnets` | 0.27 s |
| Create a tailnet | 200. The response has `id`, `displayName`, `orgId`, `dnsName`, `createdAt` and `oauthClient`. The client has the `all` scope, and its secret is shown once. There's no `alreadyExists` field, though the schema lists one | Runs 2–4: 3.96, 3.08, 4.30 s. Run 5: 0.27 s |
| Create again, with a name that exists | **500, "failed to create tailnet"**, in runs 1–4. The schema documents this case as a 400 ("already in use"), and has an `alreadyExists` field; it lists a 500 only as a generic error | 0.26–0.29 s |
| Create a second tailnet with another name, while the first exists (run 5) | 200. So the 500 is about the name, not about two live tailnets | 0.27 s |
| A token for the new tailnet from the **organization's** client (`tailnet` parameter) | **Refused:** 403 "client does not have the all scope" (runs 2 and 4), 404 "not found" (runs 1 and 3) | 0.26 s |
| A token from the **new tailnet's** client | 200, scope `all` | 0.26 s |
| Set the policy (`tagOwners` with empty lists) | 200 | Mostly 0.26–0.29 s; three calls took 1.2, 2.3 and 4.0 s |
| Two auth keys: tagged, ephemeral, pre-authorized, 15-minute expiry | 200 | 0.27 s each |
| Delete the tailnet with its own token | 200 | Runs 2–4: 0.27, 0.28, **26.2 s**. Run 5: 0.28 s each |
| The deleted tailnet's token afterwards | 401 | |
| **List the organization's tailnets** | **Lagged in run 5.** Right after it created a second tailnet, the list's count was 1. Right after it deleted both, the list still held 2, one of them its own. Checked again about 15 s later, it held only the original. Runs 1–4's lists were right at once, and so were the checks after runs 6 and 7 | |

### Nodes and traffic

| Measure | Result |
|---|---|
| `tailscale up` to Running, with its tag, per node | 5.4, 6.1, 12.2, 8.7, 5.3, 14.2 s (runs 2–4); 7.6, 5.6 s (run 5) |
| tailscaled in kernel TUN mode under the agent | Yes: `tailscale0` is a TUN device on the node checked (node a) |
| Each node's view of the other, right after both were Running (run 5) | Online, in the network map, and not yet in the WireGuard engine. Likely Tailscale adds a peer to the engine only when traffic needs it; that wasn't measured |
| The first probe of an allowed port, right after both were Running | **Timed out (3 s) in run 2**. It succeeded at once in runs 3–5. Run 2 didn't record why; the next probe of that port, about 9 s later, succeeded |
| A grant added (TCP 8081), seen in real traffic, from the API call | 6.27, 1.48, 2.41 s |
| The grant removed, seen in real traffic | within 1.5 s each time (the probes' 1 s timeout limits the precision) |
| The positive control (TCP 8080), probed once after both changes | Allowed |
| Posture: `custom:spike = ok` set on the source; TCP 8082 opens | within 1.5 s each time |
| Posture: the attribute deleted; TCP 8082 closes | within 1.5 s, 6.1 s, 1.5 s (that delete call itself took 4.9 s) |
| Pin the server's IPv4 (`POST /device/{id}/ip`, to `100.80.0.2`) | The call took 5.18, 0.28 and 4.25 s. The node saw its new address within 0.06 s of the call returning, and the other node reached it within 0.01 s |
| `tailscale debug netmap` | Has `PacketFilterRules` |
| A whole run, create to delete | 48, 49, 75 s (runs 2–4) |

### 0016's two checks (run 5)

Each policy shape was set from a known state: 8080 allowed and 8081
denied. A shape that allows everything opens 8081; one that denies
everything closes 8080. The compiled filter was read on the receiving
node.

| Policy | Traffic | Compiled filter |
|---|---|---|
| No `acls` and no `grants` | **Allows everything:** 8081 opened within 1.2 s | One rule: every tailnet address, every port |
| `"grants": []`, no `acls` | **Denies everything:** 8080 closed within 3.2 s | Empty |
| `"acls": []`, no `grants` | **Denies everything:** 8080 closed within 3.2 s | Empty |

**The readiness marker.** Two marker rules went into the policy:

- from `192.0.2.77/32`, outside the tailnet's range, to every node on
  TCP 41999;
- from `100.99.99.99/32`, inside it, on TCP 41998.

| Beside | In both nodes' compiled filters |
|---|---|
| One explicit grant (8080) | Both markers, within 0.13 s of the call returning |
| An allow-all grant | **Both markers, as rules of their own**, within 0.12 s of the call returning |

A marker beside an allow-all in `acls`, rather than in `grants`, wasn't
tried.

**The check 0021 adds to every run (run 7).** A deny test from
`tag:tailsuite-check` to itself, through `/acl/validate`, with no
devices: valid with `"grants": []` and with `"acls": []`; "test(s)
failed" (`want: Drop, got: Accept`) with neither section, and with an
allow-all in `acls`. So the check tells the readings apart.

### Policies through `/acl/validate`

**Validation runs the policy's own tests.** A grant from `tag:spike-a`
to `tag:spike-b` on TCP 80, with a test that expects that path denied,
came back "test(s) failed": `want: Drop, got: Accept` (run 5). The same
test expecting accept validated. Tailscale's API reference says validate
"runs tests to validate the existing rules" (checked 2026-10-25).

Research 0005 left one thing unchecked: whether Tailscale accepts its
three synthetic policies.

| Policy | Tailscale's answer |
|---|---|
| 1. Untouched default | Accepted |
| 2. Segmented startup | Accepted, and its own `tests` pass, with no users or devices in the tailnet. headscale 0.29.5 failed the same tests: "resolved to no IP addresses" (research 0005) |
| 3. Everything at once | **Rejected, for its `attrConfig` entry.** As written: "`broadcastToPeers` … must be '*' or absent". With `"*"`: "tailnet is not permitted to use attrConfig". 0005 flagged the entry as illustrative and unvalidated; it was wrong |
| 3, without `attrConfig` (run 6) | **Everything else is accepted**: posture with `defaultSrcPosture`, IP sets with `via`, SSH check mode with a recorder, Funnel in `nodeAttrs`, `autoApprovers`. Only its own `sshTests` fail: Alice's `check` as `ubuntu` on `tag:prod` gives `deny`. Without the SSH rule's `recorder` and `enforceRecorder`, or without the `sshTests`, it validates |

## What this means for the Tailscale engine (#6)

1. **Go.** Creating, configuring, joining, probing, posture, IP pinning,
   the compiled filter and deleting all work on a throwaway tailnet. So
   0016's matrix holds for the Tailscale engine, as far as this spike
   tested it:
   - posture native, through custom attributes and `srcPosture`;
   - pinned IPv4 native;
   - the compiled filter dump native.

   One row didn't hold: **a throwaway tailnet in this organization can't
   use `attrConfig`** ("tailnet is not permitted to use attrConfig"),
   which the matrix calls native from M4. Whether that's the plan, a flag,
   or API-only tailnets is unknown; #57 settles it before M4.
2. **Both of 0016's checks came out in the engine's favour.**
   - `{"grants": []}` denies everything on Tailscale, as on headscale. So
     it no longer has to be REFUSED: it's handled like `"acls": []`, with
     no default added. No lab ever applies the shape bare, so running labs
     couldn't see a change in that reading. So every Tailscale-engine run
     checks it through `/acl/validate` (run 7), and #5 tests headscale's.
   - The marker survives beside an allow-all grant on Tailscale, so the
     readiness wait can rely on it there. #5 checks it on headscale.
   - Both are recorded in
     [0021](../decisions/0021-empty-grants-and-the-marker-on-tailscale.md).
3. **A run that loses its tailnet's credentials can't delete it with the
   organization's client.**
   - Deleting needs a token for that tailnet, with the `all` scope. That
     comes from the client returned once at creation, or from an
     organization client that itself has `all`. That's from Tailscale's
     API reference, "Delete a tailnet" (alpha, checked 2026-10-25).
   - The test organization's client has only `tailnets`, and the token
     request with the `tailnet` parameter was refused every time.
   - [0014](../decisions/0014-tailnet-model-and-project-layout.md) keeps
     those credentials off disk. So a run that's killed outright leaks
     its tailnet:
     - it counts against the organization's limit of 10, which includes
       the original tailnet, so 9 throwaway tailnets at most;
     - it doesn't show in the admin console (Tailscale's docs).
   - **This breaks one line of 0016 on this engine:** "`tailsuite down`
     can also run it after a crash, from the lab state in `.tailsuite/`".
     With credentials off disk and a `tailnets`-only client, `down` can't
     delete the tailnet. It also corrects #3's comment of 2026-10-25
     14:53, which said the `tailnets` client could delete leftovers.
   - **How leaked tailnets get cleaned up was a question for the
     maintainer, on #6.** Answered 2026-10-26: a scheduled cleanup job
     deletes them, with a second OAuth client (`all` scope) held only in
     the `nightly` environment. #6 records it as a decision.
4. **Names must be unique.** A name that exists gets a 500, rather than
   the documented `alreadyExists` or 400. The run ID goes in the name.
5. **Wait for the nodes before any probe.** Run 2's allowed port timed out
   right after both nodes were Running, for a reason the run didn't
   record. That's evidence for 0016's existing wait, which holds the run
   until every node's filter holds the marker and its peer list has every
   peer the plan expects.
6. **Budget for slower, noisier timings than headscale.**
   - Joining took 5–14 s per node; headscale had 10 nodes online in
     3.3 s (research 0004, with client 1.104.2 rather than 1.102.5).
   - Policy changes reached traffic in up to 6.3 s; headscale took up to
     2.5 s.
   - A policy call once took 4.0 s, pinning an address up to 5.2 s, and
     deleting a tailnet up to 26 s.
   - Join in parallel, and wait, bounded, only for propagation, as the
     plan's stability rules say. All of this fits nightly runs, where 0008
     already puts the Tailscale engine.

Also:

- **Listing tailnets can lag creates and deletes** (run 5). A cleanup job
  has to allow for it.
- **Lab nodes should run tailscaled with `--no-logs-no-support`**, so a
  lab sends nothing to Tailscale beyond what the engine needs.
- **Tailscale and headscale treat a policy's own tests differently.**
  Tailscale evaluates them against the rules; headscale against live
  devices. That matters to the conformance check
  ([0002](../decisions/0002-two-engines.md)) and to how
  [0015](../decisions/0015-test-file-labels-profiles.md)'s policy tests
  are set, not to tailsuite's own tests, which always use real traffic.
- **The alpha API's errors aren't stable**: the same refused request came
  back as 403 or 404. The engine shouldn't match on error text.
- **Empty `tagOwners` lists are accepted.** Tailscale's reference makes
  `[]` shorthand for `autogroup:admin` (with `autogroup:network-admin`).
  In a tailnet with no users, that leaves the tailnet's own `all`-scope
  OAuth client as the only one that can apply such a tag, which is what
  the engine uses.
- **Research 0005's evidence that an unknown key can hide a user** rested
  on policy 3's `attrConfig` entry, which Tailscale rejects. So the
  example doesn't exist as written. 0014's rule (REFUSED when a key the
  typed copy doesn't understand meets a rewrite of identities) fails
  safe either way, so the decision stands. A real example, if one exists,
  is still to find.

## Not checked yet

- **The 9-tailnet limit itself.** The organization was never filled.
- **A marker beside an allow-all in `acls`.**
- **How long the list's lag can last.** One run saw it.
- **More than two nodes**, joining in parallel.
- **Pinning to an address another device holds.**
- **Why run 2's first probe timed out.**
- **Recovering a leaked tailnet** with an `all`-scope client. The
  approach is decided (above); it needs the client, and #6's cleanup job.
