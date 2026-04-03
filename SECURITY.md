# Security Policy

## Supported Versions

| Version | Supported |
|---------|-----------|
| latest  | Yes       |

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please report security issues by emailing the maintainer directly or by using
[GitHub's private vulnerability reporting](https://github.com/fgouteroux/haproxy-otel-spoe/security/advisories/new).

Include as much detail as possible:

- Description of the vulnerability
- Steps to reproduce
- Potential impact
- Suggested fix (if any)

You will receive a response within **5 business days**. If the issue is confirmed,
a patch will be prepared and a CVE requested where appropriate. We ask that you
give us **90 days** before public disclosure to allow time for remediation.

## Dependency Scanning

This project uses:

- `govulncheck` — scans Go module dependencies against the Go vulnerability database
- `gosec` — static analysis for common Go security mistakes
- `trivy` — container image scanning
- Dependabot — automated dependency update PRs (weekly)

All of the above run automatically in CI on every pull request and on a weekly schedule.
