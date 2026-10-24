# Security

## Reporting a vulnerability

Report it privately through GitHub:
[**Report a vulnerability**](https://github.com/bulkhead-labs/tailsuite/security/advisories/new).
Please don't open a public issue.

You'll get an acknowledgement within 3 working days. We'll agree a fix and
a disclosure date with you, and credit you unless you'd rather not be named.

## In scope today

tailsuite has no code or releases yet. What exists:

- this repository;
- its docs;
- the [status page](https://bulkhead-labs.github.io/tailsuite/).

## In scope once there is a release (#2)

- **The tailsuite binary, its releases and its signatures.**
- **Anything that could make tailsuite:**
  - leak a credential;
  - write to a real tailnet outside an explicit `tailsuite deploy`;
  - send traffic outside its lab;
  - report a path as blocked when it is open.

## What tailsuite will do with credentials (planned)

Read-only credentials will be kept in the OS keychain. A write credential
will be held in memory for a single deploy and never stored
([decision 0007](docs/decisions/0007-read-only-by-default.md)).
