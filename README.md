# tailsuite

**Prove what your tailnet allows.** tailsuite rebuilds your tailnet in a
local lab, on real Tailscale clients, sends real traffic between them, and
shows what can actually reach what. Then it helps you lock it down, proves
the lockdown works on every pull request, and deploys it through OpenTofu.

> **Status: building the first milestone.** Nothing to install yet.
> Built from source, `init`, `up`, `test` and `down` work on headscale;
> the UI, `import` and `deploy` are still the plan. Follow along on the
> [status page](https://bulkhead-labs.github.io/tailsuite/), the
> [plan](docs/plan.md) and the
> [milestones](https://github.com/bulkhead-labs/tailsuite/milestones).

## Why

A new tailnet starts with an allow-all policy: every device can reach every
other device on every port. Most teams love what Tailscale does and never
change that. Existing tools lint the policy or evaluate rules. None of them
sends real traffic to prove what gets through, so nothing catches the gap
between what a policy says and what a network does.

## How it will work

```
tailsuite init --template startup   # a project folder you own; --nix for a pinned dev shell
tailsuite up                        # the lab plus a local UI in your browser, until Ctrl-C
tailsuite test --profile pr         # (in another terminal) real SSH, HTTP(S), TCP and more; allow and deny, both proven
tailsuite import tailscale          # read-only: your tailnet as a project, with OpenTofu
tailsuite deploy                    # lab tests → tofu plan → blast-radius diff → apply
```

- **Real, not mocked.** Real `tailscaled` in containers, on a real control
  plane: [headscale](https://github.com/juanfont/headscale), or a throwaway
  Tailscale tailnet. Anything the lab can't reproduce shows as UNKNOWN,
  never as a pass.
- **Every deny has a positive control.** A dead service looks exactly like
  a blocked one, so each deny is paired with proof that the target is alive.
- **OpenTofu owns the config; tailsuite owns the machines.** The module that
  configures the lab is the one that configures production. On headscale,
  for now, tailsuite applies the module's values itself.
- **Read-only by default.** Writing to your real tailnet is an explicit,
  gated step.
- **Local first.** Your policy and your credentials stay on your machine or
  in your CI.

More in the [architecture](docs/architecture.md) and the
[decisions](docs/decisions/README.md).

## Contributing

Development is in the open: every piece of work is a
[GitHub issue](https://github.com/bulkhead-labs/tailsuite/issues), and the
[open design questions](https://github.com/bulkhead-labs/tailsuite/issues?q=is%3Aopen+label%3Atype%3Adesign)
are where to weigh in. See [CONTRIBUTING.md](CONTRIBUTING.md) and
[docs/process.md](docs/process.md).

## Licence

[Apache-2.0](LICENSE). Maintained by [Bulkhead Labs](https://bulkheadlabs.com).

tailsuite is an independent project, not affiliated with or endorsed by
Tailscale Inc. "Tailscale" is a trademark of Tailscale Inc. See
[TRADEMARK.md](TRADEMARK.md).
