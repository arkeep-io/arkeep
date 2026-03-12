# Security Policy

## Reporting a Vulnerability

**Do not open a public GitHub issue for security vulnerabilities.**

Please email **crotti.business@gmail.com** with the following information:

- A clear description of the vulnerability
- Steps to reproduce the issue
- The potential impact (what an attacker could achieve)
- Any suggested mitigations, if you have them

You will receive a response within **48 hours** acknowledging your report.
We will keep you informed of progress and ask that you give us reasonable time
to develop and release a patch before any public disclosure.

## Scope

The following are considered in scope:

- `arkeep-server` — authentication, authorization, credential storage, API endpoints
- `arkeep-agent` — gRPC authentication, binary extraction, restic invocation
- Docker images published to ghcr.io/arkeep-io and Docker Hub

The following are **out of scope**:

- Vulnerabilities in Restic or Rclone themselves — report those upstream
- Issues requiring physical access to the host machine
- Social engineering attacks
- Denial of service via resource exhaustion

## Supported Versions

Security fixes are applied to the latest released version only.
We do not backport fixes to older versions.

| Version | Supported |
|---|---|
| Latest | ✓ |
| Older | ✗ |