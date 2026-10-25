# 0005: The tailnet model, tried against three policies

Checked 2026-10-24. Evidence for
[decision 0014](../decisions/0014-tailnet-model-and-project-layout.md)
(#15, #18).

## Facts the design rests on

| Fact | Source |
|---|---|
| **A headscale lab can't run `tofu plan`.** The module finds devices with `data "tailscale_device"` (by `name` or `hostname`), which is read at plan time from a Tailscale API. Since commit [5f48e22](https://github.com/tailscale/terraform-provider-tailscale/commit/5f48e2297c) (2026-06-12), `tailscale_acl` also calls `/acl/validate` during plan, and an error there fails the plan | terraform-provider-tailscale: `ModifyPlan` in `resource_acl.go` on `main`; not in a release yet (latest v0.29.2, 2026-05-26) |
| **A tfvars file holds literal values only.** OpenTofu 1.13.1 rejects a function call there ("Functions may not be called here") and a variable reference ("Variables may not be used here"). So tailsuite can read one without running tofu | Run here, in `ghcr.io/opentofu/opentofu:1.13.1` with no network |
| **headscale rejects policy keys it doesn't know** (`json.RejectUnknownMembers(true)`) | [`hscontrol/policy/v2/types.go`](https://github.com/juanfont/headscale/blob/main/hscontrol/policy/v2/types.go) on `main`; confirmed on 0.29.5 below |
| **The official Go client drops them.** Its `ACL` type has no `sshTests`, and decoding discards any key it doesn't model | [tailscale-client-go-v2](https://github.com/tailscale/tailscale-client-go-v2/blob/main/policyfile.go) v2.12.0 (2026-10-09, MIT) |
| **hujson edits a policy without losing comments.** `Value.Patch` applies an RFC 6902 patch, `Find` takes an RFC 6901 pointer, and every value has `StartOffset` and `EndOffset` | [tailscale/hujson](https://github.com/tailscale/hujson), BSD-3, untagged; commit `b80ff77` (2026-07-27) |
| **What the provider can set.** `tailscale_acl.acl` takes JSON or HuJSON. Device tags and enabled routes are per-device resources. Advertised routes "cannot be managed through Terraform". Enabled routes must also list the routes `autoApprovers` approved, or the plan drifts. No resource sets a device's IP or its custom posture attributes | Provider docs on `main` |
| **The policy file's sections**: grants, acls, ssh, autoApprovers, nodeAttrs, postures, tagOwners, groups, hosts, ipsets, tests, sshTests. The official client also models `defaultSrcPosture`, `derpMap`, `disableIPv4`, `oneCGNATRoute`, `randomizeClientPort`, `externalTailnets` and `attrConfig` | [Policy file syntax](https://tailscale.com/kb/1337/policy-syntax); the client's `ACL` type |
| **headscale has its own API and a community provider.** A REST API at `/api/v1` and gRPC, with API keys. [awlsring/terraform-provider-headscale](https://github.com/awlsring/terraform-provider-headscale) v0.5.1 (2026-03-20, Apache-2.0, about 47 stars) has `headscale_policy`, `headscale_device_tags`, `headscale_device_subnet_routes`, users and keys. Its resources differ from the official provider's, so the same module can't target both | [headscale API docs](https://github.com/juanfont/headscale/blob/main/docs/ref/api.md); the provider's `docs/resources` |
| **`hashicorp/hcl` is MPL-2.0** (v2.25.0, 2026-09-15), fine as a dependency of Apache-2.0 code | GitHub |

## The trial

A throwaway Go program (not committed; the Go module arrives with #1), run
in `golang:1.27.2-bookworm` with `tailscale/hujson` at `b80ff77`. For each
policy it:

1. parses the file with hujson, standardizes a copy, and decodes it into a
   typed copy;
2. lists every key the typed copy didn't understand, at any depth, by
   comparing the file with the typed copy encoded again. The trial's types
   have no `omitempty`, so a known key at its zero value survives the
   round trip; the comparison was case-sensitive, and the policies use
   lower-case keys only;
3. lists every user named anywhere, and whether the typed copy found it;
4. reads a grant's byte offsets.

Then it applies a users-to-tags patch to one file, and headscale checks
every file.

The trial's types covered every section above **except two, left out on
purpose** to prove they get listed: `attrConfig`, and SSH session
recording (`recorder`, `enforceRecorder`).

headscale was `headscale/headscale:0.29.5`
(`sha256:780ed2f9e85719a2211277433f2c8afb45105d0d25031949a6848a485f9e216d`),
run from its `-debug` variant for the shell, offline, with users alice,
bob, carol and dave, and a dummy DERP map. `headscale policy check`
validates against the running server.

### The policies

All three are synthetic. Addresses are from the documentation range
`192.0.2.0/24`.

**1. Untouched default.** The shape of a new tailnet's policy.

```hujson
// Synthetic. Shaped like a new tailnet's untouched policy:
// everything reaches everything, plus SSH into your own devices.
{
	"grants": [
		// Every device, every port.
		{"src": ["*"], "dst": ["*"], "ip": ["*"]},
	],

	"ssh": [
		{
			"action": "check",
			"src":    ["autogroup:member"],
			"dst":    ["autogroup:self"],
			"users":  ["autogroup:nonroot", "root"],
		},
	],
}
```

**2. Segmented startup.**

```hujson
// Synthetic. A small startup after its first segmentation pass.
{
	"groups": {
		"group:eng": ["alice@example.com", "bob@example.com"],
		"group:ops": ["carol@example.com"],
	},

	"tagOwners": {
		"tag:prod": ["group:ops"],
		"tag:db":   ["group:ops"],
		"tag:ci":   ["group:ops"],
	},

	"hosts": {
		"office-printer": "192.0.2.10",
	},

	"grants": [
		// Engineers reach the web tier over HTTPS only.
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:443"]},
		// Only ops reaches the databases.
		{"src": ["group:ops"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
		// The web tier talks to the databases.
		{"src": ["tag:prod"], "dst": ["tag:db"], "ip": ["tcp:5432"]},
		// CI deploys to prod over SSH.
		{"src": ["tag:ci"], "dst": ["tag:prod"], "ip": ["tcp:22"]},
		// Alice prints. A user named directly, which the rewrite has to find.
		{"src": ["alice@example.com"], "dst": ["office-printer"], "ip": ["tcp:631"]},
	],

	"tests": [
		{"src": "alice@example.com", "accept": ["tag:prod:443"], "deny": ["tag:db:5432"]},
		{"src": "carol@example.com", "accept": ["tag:db:5432"]},
	],
}
```

**3. Everything at once.** Features a lab has to treat carefully. The
`attrConfig` entry is illustrative: it puts a user inside a section the
trial's types skip, and wasn't validated against Tailscale.

```hujson
// Synthetic. The features a lab has to treat carefully, in one file.
{
	"groups": {
		"group:eng": ["alice@example.com", "dave@example.com"],
	},

	"tagOwners": {
		"tag:prod":     ["autogroup:admin"],
		"tag:router":   ["autogroup:admin"],
		"tag:exit":     ["autogroup:admin"],
		"tag:recorder": ["autogroup:admin"],
	},

	// Posture: only up-to-date Macs reach prod.
	"postures": {
		"posture:stableMac": [
			"node:os == 'macos'",
			"node:tsReleaseTrack == 'stable'",
		],
	},
	"defaultSrcPosture": ["posture:stableMac"],

	"ipsets": {
		"ipset:office": ["add 192.0.2.0/24", "remove 192.0.2.10"],
	},

	"grants": [
		{"src": ["group:eng"], "dst": ["tag:prod"], "ip": ["tcp:443"], "srcPosture": ["posture:stableMac"]},
		// The office LAN, only through the office router.
		{"src": ["group:eng"], "dst": ["ipset:office"], "ip": ["*"], "via": ["tag:router"]},
		{"src": ["autogroup:member"], "dst": ["autogroup:self"], "ip": ["*"]},
		{"src": ["autogroup:member"], "dst": ["autogroup:internet"], "ip": ["*"]},
	],

	"ssh": [
		{
			"action":      "check",
			"src":         ["group:eng"],
			"dst":         ["tag:prod"],
			"users":       ["autogroup:nonroot"],
			"checkPeriod": "12h",
			// Session recording: left out of the trial's types on purpose.
			"recorder":        ["tag:recorder"],
			"enforceRecorder": true,
		},
	],

	"sshTests": [
		{"src": "alice@example.com", "dst": ["tag:prod"], "check": ["ubuntu"], "deny": ["root"]},
	],

	"nodeAttrs": [
		{"target": ["tag:prod"], "attr": ["funnel"]},
	],

	"autoApprovers": {
		"routes":   {"192.0.2.0/24": ["tag:router"]},
		"exitNode": ["tag:exit"],
	},

	// Custom device attributes: left out of the trial's types on purpose.
	// The owner is named here, which a users-to-tags rewrite would miss.
	"attrConfig": {
		"custom:team": {"type": "string", "broadcastToPeers": ["dave@example.com"]},
	},
}
```

## Results

| | 1. Default | 2. Segmented | 3. Everything |
|---|---|---|---|
| Parses into the typed copy | Yes | Yes | Yes |
| Keys not understood | None | None | `/attrConfig`, `/ssh/0/recorder`, `/ssh/0/enforceRecorder` |
| Users named | None | 6, all found by the typed copy | 4: 3 found, and `/attrConfig/custom:team/broadcastToPeers/0` flagged as one a rewrite would miss |
| Offsets of `/grants/0` | Bytes 174–215 | Bytes 405–467 | Bytes 623–722 |
| headscale 0.29.5 | Valid | Parses. Its own `tests` fail: "resolved to no IP addresses", because the users have no devices | Rejected; see below |

**The patch.** M1's minimal rewrite for the Tailscale engine (#6) was
applied to policy 2 as two RFC 6902 operations: replace
`/grants/4/src/0` with `tag:user-alice`, and add
`/tagOwners/tag:user-alice`.

- All six comment lines survived.
- `diff` showed only the two touched lines changed.
- The patched file parsed again.
- hujson doesn't reformat, so the added member sits on the line before it:
  `"tag:ci":   ["group:ops"],"tag:user-alice":["tag:tailsuite"]`. That's
  harmless in an engine's copy, which users see as the patch, not as a
  file.
- headscale rejected the patched file: `tag "tag:user-alice" references
  undefined tag "tag:tailsuite"`.

**What headscale 0.29.5 rejects in policy 3.** It reports one error at a
time, so each was removed in turn:

1. `autogroup:admin` as a tag owner (role autogroups): `invalid owner
   format`.
2. `postures` (and with it `defaultSrcPosture` and `srcPosture`): `unknown
   field`.
3. `ipsets`: `unknown field`.
4. SSH `recorder`: `unknown field`.
5. `nodeAttrs` with `funnel`: "a feature headscale does not yet support",
   [headscale#2527](https://github.com/juanfont/headscale/issues/2527).
6. `attrConfig`: `unknown field`.

With those removed, the policy parses: `via`, `autogroup:self`,
`autogroup:internet`, SSH check mode with `checkPeriod`, `sshTests` and
`autoApprovers` (routes and exit nodes) are all accepted. The `sshTests`
then fail for the same reason as policy 2's tests: no devices.

## What this means

- **The model holds up (#15).** A typed copy finds every user it needs
  to, and lists what it doesn't understand, at any depth. A patch on the
  user's file keeps everything the user wrote.
- **A key the typed copy doesn't understand can hide a user.** The
  rewrite would miss that user. So a run whose patch rewrites identities
  is REFUSED when such a key is present (0014).
- **For #5, the headscale engine:** headscale checks a policy's own
  `tests` and `sshTests` against live devices, so the policy is set after
  the nodes join, or the tests fail.
- **For #6 and #21, the rewrite:** a patch must define every tag it
  introduces, and must also rewrite the users named in `groups`, `tests`
  and `sshTests`. The trial's patch changed one grant only, to show the
  mechanics.
- **For #17, the capability matrix:** the six rejections above are
  headscale's gaps in this policy, confirmed on 0.29.5.
- **For #16, the test format:** tests that name a user need that user's
  device in the lab.

## Not checked yet

- **Whether Tailscale accepts each policy.** That needs `/acl/validate` on a
  tailnet, which the test organization provides (#3). Checked on
  2026-10-25 in [research 0006](0006-throwaway-tailnets-spike.md):
  - Tailscale accepts policies 1 and 2.
  - It rejects policy 3's `attrConfig` entry: `broadcastToPeers` must be
    `*` or absent, and this tailnet may not use `attrConfig` at all.
  - Without that entry, it accepts the rest of policy 3, except that the
    policy's own `sshTests` fail while the SSH rule enforces recording.
  - So the user hidden in `attrConfig`, above, is an example that
    Tailscale wouldn't accept.
- **More than three policies.** The M2 sample configs can grow from these.
- **Capitalised keys** (`ACLs`, `Groups`), which older policies use. Go's
  decoder matches them to the typed copy ignoring case, so the comparison
  has to ignore case too, and patch pointers have to come from the file's
  own keys (0014).
