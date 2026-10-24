# Repository and releases

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Repository source and GHCR packages have independent visibility. Public source does not publish an image or make an existing package public. Check each release's actual access requirements in [installation](INSTALL.md).

## Development and checks

Dockspire is maintained by its owner. `main` contains the complete maintained source, tests, CI, release tooling and documentation. Use focused branches and reviewed pull requests to `main`. See [development checks](DEVELOPMENT.md).

CI checks Go on Linux/Windows, fast JavaScript regressions, a real-browser setup/sign-in/draft/review smoke test, installation fixtures, a disposable Linux container and takeover, and the exact image executables for vulnerabilities. Full-history Gitleaks is pinned and fully redacted; a weekly schedule repeats CI. Dependabot proposes application Go, browser-test, Actions and builder-image updates. Bundled Compose uses a standalone build manifest and follows the [manual pinned update procedure](../deploy/compose-runtime/README.md#dependency-updates). Updates require review; no automatic merge, image publication or deployment is configured.

Credentialed baseline checks accept canonical trusted main or an explicitly reviewed owner-dispatched feature ref. The feature path requires both actor identities to be the owner, an exact non-main `refs/heads/...` input, and a 40-character commit matching the workflow SHA and checked-out HEAD. Both PR event types and forks are rejected before package access. Only the baseline-pull step receives its token; its private Docker authentication directory is removed before candidate execution. Ordinary CI does not publish images.

Before merging a reviewed feature commit, run the four ordinary PR checks and the full upgrade job on that same off-main commit. After approving its exact ref/SHA and credentialed test scope, the owner dispatches:

```sh
branch=$(git branch --show-current)
head=$(git rev-parse HEAD)
gh workflow run ci.yml --ref "$branch" \
  -f expected_ref="refs/heads/$branch" -f expected_commit="$head"
```

The workflow must report the selected HEAD, all four ordinary checks successful, and `Check release upgrade and rollback` successful with both the instrumented and original-entrypoint upgrade/rollback steps successful. Each step covers HTTP/HTTPS and password/passwordless modes using fresh disposable data. Missing inputs, a skipped job, or results for an older HEAD do not satisfy this gate. Review and rerun after source changes; do not merge to main merely to discover test failures. The existing main workflow registration permits dispatching the workflow version on the selected branch.

## Releases and independent visibility expectations

1. Choose a new version tag, such as `v0.2.0`, for a reviewed/tested commit on licensed `main`. Keep existing tags unchanged. The first public release must include Dockspire's Apache 2.0 LICENSE and NOTICE. Historical v0.1.1/v0.1.2 tagged trees and release notes predate that licensing change; do not silently relabel or rebuild old artifacts.
2. Obtain separate action-specific owner authorization for the tag, image publication, release notes and any visibility change. Run **Publish container image** manually from canonical `main`, supplying the existing tag and the expected current **repository visibility** and **package visibility** independently.
3. The workflow verifies the selected tag belongs to main, licensed source exists, and repository/package metadata matches both expectations. It runs source/browser/installation/image/takeover/upgrade tests and exact executable scans, rejects an already-used version, and verifies destination metadata/digest again after pushing. It never changes visibility.
4. Record the immutable digest, validation results, installation/access requirements, backup/rollback procedure and known limitations in release notes for the same tag. The image workflow does not create a GitHub Release or update existing installations.

A public repository with a private GHCR package is valid; pulls still require package access. A public GHCR package allows anonymous pulls. GitHub does not inherit package visibility from repository visibility, and a public package cannot be changed back to private. Treat that as a separate explicit owner decision: [GitHub container registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry), [package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility).

The upgrade baseline is released v0.2.0 at `ghcr.io/mayo-nas/dockspire@sha256:265b222a8993679dc6920e5e06b7184b57159571f68a6cd1de66047e05c0f639`, built from `16fe9995ed5f7b7896dec78cf8de90c314ab9147`. The pull helper verifies package visibility, the version's unique digest, Linux/amd64 and source/version/revision OCI labels. The runtime matrix exercises v0.2.0 to candidate and restored-v0.2.0 rollback; fixed historical fingerprint vectors and existing legacy identity, archive v1/v2 and recovery tests retain older-format coverage in Go tests.

`DOCKSPIRE_BASELINE_PACKAGE_VISIBILITY` is a repository Actions variable describing its expected actual package visibility; it defaults to `private`. If the package is deliberately made public, separately approve updating that variable to `public`. Metadata/tag/digest verification remains required in both cases; mismatches fail closed. Baseline migration does not change visibility or delete old images. Historical tagged workflows retain their old pins and may no longer rerun after those images are retired; use maintained main workflows and never rewrite existing tags.

Use versioned images or immutable digests. Do not overwrite a published version, invent a second build tag or selectively promote source files. Runtime images contain the application, bundled Compose and notices; development/browser-test tooling is excluded.

## Owner configuration before public launch

These are proposed settings, not changes made by this repository:

- Enable and verify GitHub private vulnerability reporting; [SECURITY.md](../SECURITY.md) does not enable it.
- Add main protection/rulesets when the plan and visibility allow it. Require successful `Check (ubuntu-latest)`, `Check (windows-latest)`, `Scan source history` and `Check Linux container installation` checks. The Linux check includes the real-browser smoke test. The privileged upgrade job never receives fork-PR credentials; the owner must complete the exact-HEAD manual upgrade gate before merging. The manual publisher also retains its upgrade checks.
- Keep a documented owner recovery/bypass path. Do not require an unavailable second maintainer's approval for every owner PR. Local independent code/security reviews remain required; direct push/bypass is not a substitute for review.
- Review Actions token permissions, dependency/security alerts, historical logs/metadata and exact publication refs/assets before changing visibility. Public conversion exposes Actions history/logs. Remote settings, pushes and publication each require their own authorization.

Dockspire is Apache 2.0 with [LICENSE](../LICENSE), [NOTICE](../NOTICE) and third-party notices. Images retain these files under `/licenses`; supplied branding retains [asset notes](branding/ASSET_LICENSE_NOTES.md). No legal or trademark clearance is inferred from automated checks.
