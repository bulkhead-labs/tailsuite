# 0006: Tailscale's throwaway tailnets, end to end

Run on 2026-10-25 against the dedicated Tailscale test organization (#3).
The question was whether Tailscale's API-only tailnets do what the docs
say, on a real organization. Evidence for #6, the Tailscale engine.

**Verdict: go.** Every step the engine needs works. Five findings change
how #6 has to be built (below).

## How it was run

- **A throwaway program** (not committed), using only Python's standard
  library, ran four times. The first run stopped early on a bug in the
  program; the other three ran the whole flow. The tables give those
  three.
- **Each run:**
  1. created an API-only tailnet with the test organization's OAuth client
     (scope `tailnets` only);
  2. took a token for the new tailnet from the OAuth client that the
     creation returned;
  3. set a policy, and minted two tagged, ephemeral, pre-authorized auth
     keys;
  4. joined two Docker nodes, each running real tailscaled in kernel TUN
     mode under tailsuite's node agent (#7, decision 0020);
  5. probed between them with the agent;
  6. changed the policy, set a custom posture attribute, and pinned a
     node's IPv4 address, timing how long real traffic took to change;
  7. deleted the tailnet.
- **The nodes:** `tailscale/tailscale:v1.102.5`, pinned by digest, on a
  4 vCPU, 8 GB VM with Docker 26.1.5, 128 MiB each.
- **The policy:** synthetic. `tag:spike-a` may reach `tag:spike-b` on TCP
  8080. TCP 8082 is allowed only from a device whose `custom:spike`
  attribute is `ok`. TCP 8081 is allowed or not, as the test needs.
- **Secrets:** the program kept every credential in its own memory. Auth
  keys reached the nodes on stdin (`tailscale up
  --auth-key=file:/dev/stdin`), never in an argument, environment
  variable or file. No output contained a credential, and none is
  recorded here.
- **Rule 3:** the nodes reached only Tailscale's control plane and DERP,
  and each other. Probes went only to the other node's Tailscale address.

## Results

### The API

| Step | Result | Time (three runs) |
|---|---|---|
| Token for the organization's client | 200, scope `tailnets` | 0.27 s each |
| Create a tailnet | 200. The response has `id`, `displayName`, `orgId`, `dnsName`, `createdAt` and `oauthClient` (an `all`-scope client whose secret is shown once). It has no `alreadyExists` field, though the schema lists one | 3.96, 3.08, 4.30 s |
| Create again, same name | **500, "failed to create tailnet"**, every time | 0.26–0.29 s |
| A token for the new tailnet from the **organization's** client (`tailnet` parameter) | **Refused:** 403 "client does not have the all scope" twice, 404 "not found" twice (including the first run) | 0.26 s |
| A token from the **new tailnet's** client | 200, scope `all` | 0.26 s |
| Set the policy (`tagOwners` with empty lists) | 200 | 0.26–0.27 s |
| Two auth keys: tagged, ephemeral, pre-authorized, 15-minute expiry | 200 | 0.27 s each |
| Delete the tailnet with its own token | 200 | 0.27, 0.28, **26.2 s** |
| The deleted tailnet's token afterwards | 401 | |
| The organization's tailnets afterwards | 1, the original; none of the spike's left | |

### Nodes and traffic

| Measure | Result (three runs) |
|---|---|
| `tailscale up` to Running, with its tag, per node | 5.3, 6.0, 12.2, 8.6, 5.3, 14.2 s |
| tailscaled in kernel TUN mode under the agent | Yes: `tailscale0` is a TUN device |
| The first probe of an allowed port, right after both nodes were Running | **Timed out (3 s) in one run of three**; succeeded at once on the next probe |
| A grant added (TCP 8081), seen in real traffic, from the API call | 6.27, 1.48, 2.41 s |
| The grant removed, seen in real traffic | within 1.5 s each time (the probes' 1 s timeout limits the precision) |
| The positive control (TCP 8080) during both changes | Allowed throughout |
| Posture: `custom:spike = ok` set on the source; TCP 8082 opens | within 1.5 s each time |
| Posture: the attribute deleted; TCP 8082 closes | within 1.5 s, 6.1 s, 1.5 s (that delete call itself took 4.9 s) |
| Pin the server's IPv4 (`POST /device/{id}/ip`, to `100.80.0.2`) | The call took 5.18, 0.28 and 4.25 s. The node saw its new address as the call returned, and the other node reached it at once |
| `tailscale debug netmap` on the server | Has `PacketFilterRules` (one rule) |
| A whole run, create to delete | 48, 49, 75 s |

### Research 0005's policies through `/acl/validate`

Research 0005 left one thing unchecked: whether Tailscale accepts its
three synthetic policies.

| Policy | Tailscale's answer |
|---|---|
| 1. Untouched default | Accepted |
| 2. Segmented startup | Accepted, and its own `tests` pass. Tailscale evaluates them against the rules alone, with no users or devices in the tailnet. headscale 0.29.5 failed the same tests: "resolved to no IP addresses" (research 0005) |
| 3. Everything at once | **Rejected:** `unsupported broadcastToPeers value "dave@example.com" for attrConfig "custom:team"; must be '*' or absent`. 0005 flagged this entry as illustrative and unvalidated; it was wrong |

## What this means for the Tailscale engine (#6)

1. **Go.** Creating, configuring, joining, probing, posture, IP pinning,
   the compiled filter and deleting all work on a throwaway tailnet. So
   [0016](../decisions/0016-engine-interface-capabilities.md)'s matrix
   holds for the Tailscale engine, as far as this spike tested it:
   - posture native, through custom attributes and `srcPosture`;
   - pinned IPv4 native;
   - the compiled filter dump native.
2. **A run that loses its tailnet's credentials can't delete it with the
   organization's client.**
   - Deleting needs a token for that tailnet, with the `all` scope. That
     comes from the client returned once at creation, or from an
     organization client that itself has `all`.
   - The test organization's client has only `tailnets` (the plan's
     prerequisite), and the token request with the `tailnet` parameter
     was refused every time.
   - [0014](../decisions/0014-tailnet-model-and-project-layout.md) keeps
     those credentials off disk. So a run that's killed outright leaks its
     tailnet, which counts against the organization's limit of 10 and
     doesn't show in the admin console.
   - How leaked tailnets get cleaned up is a question for the maintainer,
     on #6.
3. **Names must be unique per run.** Creating a tailnet whose name exists
   fails with a 500, despite the schema's `alreadyExists`. The run ID
   goes in the name.
4. **Wait for the nodes to see each other before any probe.** Right after
   both nodes were Running, an allowed port once timed out, which a test
   would read as a deny.
   - [0001](../decisions/0001-truth-over-mocks.md)'s positive controls
     guard against reporting that as a deny.
   - The engine should still wait, bounded, until every node lists its
     peers as online, before the runner starts.
5. **Budget for slower, noisier timings than headscale.**
   - Joining took 5–14 s per node; headscale had 10 nodes online in
     3.3 s (research 0004).
   - Policy changes reached traffic in up to 6.3 s; headscale took up to
     2.5 s.
   - Pinning an address took up to 5 s, and deleting a tailnet up to
     26 s.
   - Join in parallel, and wait, bounded, only for propagation, as the
     plan's stability rules say. All of this fits nightly runs, where
     0008 already puts the Tailscale engine.

Also:

- **Tailscale and headscale treat a policy's own tests differently.**
  Tailscale evaluates them against the rules; headscale against live
  devices. That matters to the conformance check
  ([0002](../decisions/0002-two-engines.md)) and to how
  [0015](../decisions/0015-test-file-labels-profiles.md)'s policy tests
  are set, not to tailsuite's own tests, which always use real traffic.
- **The alpha API's errors aren't stable**: the same refused request
  came back as 403 or 404. The engine shouldn't match on error text.
- **Empty `tagOwners` lists are accepted**, so the engine's tags need no
  owner in a tailnet with no users.

## Not checked yet

- **The 10-tailnet limit itself.** The organization was never filled.
- **More than two nodes**, joining in parallel.
- **Pinning to an address another device holds.**
- **Recovering a leaked tailnet** with an `all`-scope client. That needs
  the maintainer's decision on #6, and the client.
