# Contributing

Thanks for looking. tailsuite is in its planning stage, so the most useful
thing right now is a view on the
[open design questions](https://github.com/bulkhead-labs/tailsuite/issues?q=is%3Aopen+label%3Atype%3Adesign).

## How work flows

[docs/process.md](docs/process.md) has the whole flow. In short:

1. Every change starts from an issue. Open one if it doesn't exist.
2. Open your pull request **as a draft**, with `Closes #n`, and fill in the
   template's checklist.
3. Mark it ready when the checklist is done. The heavier test tiers run
   then, once CI exists (#1, #13).
4. Maintainers squash-merge.

## Sign your commits off (DCO)

Every commit must carry a `Signed-off-by` line, certifying the
[Developer Certificate of Origin](https://developercertificate.org/): you
wrote the change, or have the right to submit it under the project's
licence.

```
git commit -s -m "Add the TCP probe"
```

There is no CLA. Contributions are under the project's
[Apache-2.0](LICENSE) licence.

## Development environment

The repo has a Nix flake. `nix develop` gives the toolchain CI uses. Plain
`go build` will work without Nix once there is code.

## Never commit

- Real tailnet data: policies, device names, IPs, keys, user names. Sample
  configs must be synthetic.
- Secrets of any kind.

## Security issues

Not in public issues. See [SECURITY.md](SECURITY.md).
