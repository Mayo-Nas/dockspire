# Security policy

Dockspire is intended for a trusted homelab management network, with one administrator and Linux amd64 Docker/Compose hosts. It is not designed for direct Internet exposure or mutually untrusted tenants. Docker socket access grants host control, even with a read-only socket mount.

HTTP and tokenless initial setup remain intentional options. Passwordless access deliberately gives every reachable visitor host control. Restrict network reachability; use the documented optional HTTPS or SSH protection where appropriate. The public-readiness changes do not alter administrator setup or existing access modes.

## Supported versions

Security fixes target the latest published release and development on main, on a best-effort basis. Older releases are not actively maintained. Unreleased main changes are not included in an existing versioned image until a new reviewed version is explicitly published. No response deadline or long-term support commitment is promised.

## Report a vulnerability privately

Use [GitHub private vulnerability reporting](https://github.com/Mayo-Nas/dockspire/security/advisories/new) when enabled. Include the affected version/commit or image digest, impact, and a minimal reproduction with fictional data. Do not include passwords, session cookies, access tokens, private host addresses, full backups or real stack environment files.

If private reporting is unavailable, open an issue asking only for a private security contact, without vulnerability details, and wait for the maintainer to provide a private channel. Do not publish an exploit or sensitive diagnostic material in an ordinary issue.

The maintainer must explicitly enable and verify private reporting before advertising it as available. Adding this file does not change GitHub settings. Repository visibility, package visibility, release publication and deployment remain separate owner decisions.

## Maintenance

CI runs pinned full-history secret scanning with complete redaction, application/container executable vulnerability checks, tests and a real-browser smoke test. A weekly schedule repeats CI; Dependabot proposes source dependency/action/image updates. No automatic merge, publication, host-package upgrade or deployment is configured. Scanner results and tests reduce risk but do not establish that every vulnerability has been found.
