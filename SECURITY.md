# Security policy

## Supported versions

Iceberg Sentry is currently pre-1.0. Security fixes land on the most recent
minor release; older minors get patches only for critical CVEs.

| Version | Supported |
| ------- | :-------: |
| 0.3.x   | ✅        |
| < 0.3   | ❌        |

## Reporting a vulnerability

**Do not open a public GitHub issue for security reports.**

Please report vulnerabilities via one of:

1. **GitHub Security Advisories** — the preferred channel.
   [Report a vulnerability here](https://github.com/jaybilgaye/iceberg-sentry/security/advisories/new).
2. **Email** — `security@icebergsentry.io` (encrypted with the PGP key at
   <https://icebergsentry.io/.well-known/pgp>, if applicable).

Expect an acknowledgement within 3 business days and a status update at
least every 7 days until the issue is resolved.

## Scope

Concrete examples of what qualifies as a security report:

* Any bug that lets an unprivileged caller of the CLI escalate to reading data
  outside the scanning identity's permitted scope.
* Any code path that persists PII values to disk or emits them in logs
  (violates the zero-persistence guarantee documented in
  [Concepts → Zero-persistence PII scanning](site/docs/concepts.html)).
* Credential leakage in output formats (`text`, `json`, `sarif`, `prometheus`).
* Panic or DoS on maliciously crafted Iceberg metadata / manifest input.

Out of scope:

* Attacks that require pre-existing access to the machine running the CLI.
* Denial-of-service that requires an authenticated cluster caller.
* Reports about dependencies that are not exercised in Sentry's code paths.

## Safe-harbour

We consider security research conducted in good faith consistent with this
policy to be authorised, and we won't pursue legal action.
