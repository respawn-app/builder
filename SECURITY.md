# Security Policy

Please report security issues privately.

Do not open a public GitHub issue for an unpatched vulnerability.

For Builder's default tool trust model and container/VM isolation guidance, see the [Sandboxing and Security guide](https://opensource.respawn.pro/builder/sandboxing/).

## Reporting a Vulnerability

Use one of these private channels:

- `hello@respawn.pro`
- GitHub private vulnerability reporting.

If you are unsure whether something is security-sensitive, report it privately first.

## What to Include

Please include as much of the following as possible:

- affected version, tag, or commit
- impact and attack scenario
- clear reproduction steps or proof of concept
- relevant environment details
- whether the issue is already publicly known

Reports that are concrete and reproducible are much easier to triage quickly.

## Response Expectations

We aim to acknowledge security reports within 7 days and follow up on a best-effort basis until resolution.

We do not currently publish a fixed remediation SLA.

## Supported Versions

Security fixes are handled on a best-effort basis for the latest release and the current `main` branch.

Older versions may be asked to upgrade instead of receiving backported fixes.

## Scope

Examples of issues that should be reported through the private security process include:

- credential disclosure
- authentication or session-handling flaws
- unsafe file access or workspace-boundary bypasses
- command-execution vulnerabilities
- supply-chain or release-integrity issues

## Disclosure

Please allow maintainers a reasonable opportunity to investigate and ship a fix before public disclosure.

Once a fix is available, we may coordinate timing for public disclosure and release notes.

## Bug Bounty

There is currently no bug bounty program.
