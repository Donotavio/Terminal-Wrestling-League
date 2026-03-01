# Security Policy

## Supported Versions

The `main` branch is the supported baseline for security fixes.
Security fixes are applied against `main` and then propagated as needed.

## Reporting a Vulnerability

Use GitHub private vulnerability reporting (GitHub Security Advisories) for all reports.
Do not open public issues for undisclosed vulnerabilities.

To report:

1. Go to the repository Security tab.
2. Open a private vulnerability report.
3. Include complete technical details listed below.

## What to Include in Your Report

Please include:

- Impact summary (confidentiality/integrity/availability)
- Affected commit, branch, or version range
- Reproduction steps (clear and deterministic)
- Proof-of-concept or logs (sanitized; no secrets)
- Environment details (Go version, PostgreSQL version, OS)
- Suggested remediation, if available

## Response and SLA Expectations

Target handling timeline:

- Acknowledgment: within 72 hours
- Initial triage: within 7 calendar days
- Status updates: at least weekly while the report is open

Complex issues may require longer remediation windows; updates will be provided in the advisory thread.

## Disclosure Policy

This project follows coordinated disclosure.
Please do not publicly disclose vulnerability details until:

- a fix or mitigation is available, and
- maintainers confirm disclosure readiness.

## Security Scope Examples

High-priority examples for this project:

- Determinism/replay divergence that can corrupt authoritative outcomes
- Authentication or rate-limit bypass in SSH/login/match flows
- Telemetry pipelines leaking sensitive operational or player-identifying data
- SQL integrity issues, unsafe query patterns, or migration flaws affecting data correctness
- Session/spectator flow vulnerabilities that expose unauthorized match data
