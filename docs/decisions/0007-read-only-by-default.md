# 0007: Read-only by default; earn each permission; local first

Status: **accepted** (2026-10-24). Credentials UX is open (#24); apply
ordering is open (#25); hosting is open (#27).

## Decision

**Connect read-only.** A one-click "Connect with Tailscale" isn't possible
for a third-party tool: Tailscale's OAuth apps are in alpha and work only
inside the tailnet that created them ("does not support users from other
tailnets", docs validated 2026-06-30). So the user creates an OAuth client
with read scopes, and pastes it into the local UI, which opens the right
admin-console page:

- `policy_file:read`, `devices:core:read`, `devices:routes:read`;
- `devices:posture_attributes:read`, `users:read`, `services:read`;
- `networking_settings:read`, `feature_settings:read`.

Working assumption until #24 settles it: the secret is kept in the OS
keychain.

**Apply only on `deploy`,** with a separate write credential (`policy_file`,
plus `devices:core` or `devices:routes` only when needed). It is held in
memory for that one apply and never stored.

**Gates before any write:**

1. The lab passes on the new config.
2. Tailscale's `/acl/validate` passes (it also runs the policy's `tests`).
3. The blast-radius diff is confirmed by typing.
4. A lockout check: "this cuts this machine off from …".
5. The write uses `If-Match` with the policy's ETag, so a concurrent edit
   is never overwritten.
6. The previous policy is saved for one-click rollback, and the result is
   read back.
7. If GitOps or Terraform manages the tailnet, open a PR instead of
   applying.

**Local first.** Policies and credentials stay on the user's machine or in
their CI. A hosted mode needs a threat model first.

## Why

A tool that holds write access to many networks is a prime target. A
security tool earns trust by asking for as little as possible, as late as
possible.

A detail that helps the first screen: `If-Match: ts-default` tells whether
a policy is still the untouched default, which means allow-all.

## Given up

- **Connecting takes about a minute, not one click.**
- **Applying needs a second credential.**
