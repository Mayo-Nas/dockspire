# Repository and releases

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Repository source and GHCR packages have independent visibility. Public source does not publish an image or make an existing package public. Check each release's actual access requirements in [installation](INSTALL.md).

## Development and checks

Dockspire is maintained by its owner. `main` contains the complete maintained source, tests, CI, release tooling and documentation. Use focused branches and reviewed pull requests to `main`. See [development checks](DEVELOPMENT.md).

CI checks Go on Linux/Windows, fast JavaScript regressions, a real-browser setup/sign-in/draft/review smoke test, installation fixtures, a disposable Linux container and takeover, and the exact image executables for vulnerabilities. Full-history Gitleaks is pinned and fully redacted; a weekly schedule repeats CI. Dependabot proposes Go, bundled Compose, browser-test, Actions and builder-image updates. Updates require review; no automatic merge, image publication or deployment is configured.

Credentialed baseline checks run only on canonical trusted main, never fork pull requests or fork-main workflows. Ordinary CI does not publish images.

## Releases and independent visibility expectations

1. Choose a new version tag, such as `v0.2.0`, for a reviewed/tested commit on licensed `main`. Keep existing tags unchanged. The first public release must include Dockspire's Apache 2.0 LICENSE and NOTICE. Historical v0.1.1/v0.1.2 tagged trees and release notes predate that licensing change; do not silently relabel or rebuild old artifacts.
2. Obtain separate action-specific owner authorization for the tag, image publication, release notes and any visibility change. Run **Publish container image** manually from canonical `main`, supplying the existing tag and the expected current **repository visibility** and **package visibility** independently.
3. The workflow verifies the selected tag belongs to main, licensed source exists, and repository/package metadata matches both expectations. It runs source/browser/installation/image/takeover/upgrade tests and exact executable scans, rejects an already-used version, and verifies destination metadata/digest again after pushing. It never changes visibility.
4. Record the immutable digest, validation results, installation/access requirements, backup/rollback procedure and known limitations in release notes for the same tag. The image workflow does not create a GitHub Release or update existing installations.

A public repository with a private GHCR package is valid; pulls still require package access. A public GHCR package allows anonymous pulls. GitHub does not inherit package visibility from repository visibility, and a public package cannot be changed back to private. Treat that as a separate explicit owner decision: [GitHub container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry), [package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

The upgrade baseline remains the fixed v0.1.1 immutable digest. `DOCKSPIRE_BASELINE_PACKAGE_VISIBILITY` is a repository Actions variable describing its expected actual package visibility; it defaults to `private`. If the package is deliberately made public, separately approve updating that variable to `public`. Metadata/tag/digest verification remains required in both cases; mismatches fail closed.

Use versioned images or immutable digests. Do not overwrite a published version, invent a second build tag or selectively promote source files. Runtime images contain the application, bundled Compose and notices; development/browser-test tooling is excluded.

## Owner configuration before public launch

These are proposed settings, not changes made by this repository:

- Enable and verify GitHub private vulnerability reporting; [SECURITY.md](../SECURITY.md) does not enable it.
- Add main protection/rulesets when the plan and visibility allow it. Require successful `Check (ubuntu-latest)`, `Check (windows-latest)`, `Scan source history` and `Check Linux container installation` checks. The Linux check includes the real-browser smoke test. The privileged upgrade job is not a fork-PR requirement; the manual publisher still runs upgrade checks.
- Keep a documented owner recovery/bypass path. Do not require an unavailable second maintainer's approval for every owner PR. Local independent code/security reviews remain required; direct push/bypass is not a substitute for review.
- Review Actions token permissions, dependency/security alerts, historical logs/metadata and exact publication refs/assets before changing visibility. Public conversion exposes Actions history/logs. Remote settings, pushes and publication each require their own authorization.

Dockspire is Apache 2.0 with [LICENSE](../LICENSE), [NOTICE](../NOTICE) and third-party notices. Images retain these files under `/licenses`; supplied branding retains [asset notes](branding/ASSET_LICENSE_NOTES.md). No legal or trademark clearance is inferred from automated checks.
