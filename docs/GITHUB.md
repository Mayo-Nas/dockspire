# Repository and release policy

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Keep it private unless public visibility is explicitly approved. Source pushes, remote creation, version tags, container publication, GitHub Releases and application deployment are separate actions requiring explicit authorization; routine local changes do not authorize any of them.

## Branches and pull requests

`main` contains the complete maintained source: application code, tests, CI, build/release tooling, dependency manifests, user and contributor documentation, assets and required notices. Use short-lived feature/fix branches and reviewed pull requests to `main`. Merge the full reviewed change rather than selecting application files for a separate branch. The legacy `development` branch has been removed. Do not recreate a permanent split between application files and maintained tests/tools. Removing remote branches requires separate approval.

Follow [CONTRIBUTING.md](../CONTRIBUTING.md): independent code and security reviews plus successful CI are required before merge. Configure branch protection/rulesets with required pull requests, reviews and checks when the account plan supports them. The private repository currently has unprotected branches and a plan restriction on the needed ruleset/protection capability. These requirements must therefore be followed manually; do not claim automatic enforcement.

Keep installations minimal using the multistage Dockerfile and deliberate release packaging. The final image contains the executable, bundled Compose and required notices; tests and contributor tools remain in source rather than the runtime image. Private assistant configuration, caches, operational records, credentials and generated artifacts do not belong in tracked source.

## Continuous integration

CI runs on pushes and pull requests targeting `main`, and can be dispatched manually. It checks Go on Linux/Windows, browser JavaScript and regression tests, installation fixtures, a disposable Linux container installation and explicit takeover integration. It scans the exact two shipped Go executables with pinned govulncheck. Actions are pinned to commit hashes; repository permissions default to read-only and checkout does not persist credentials. Normal CI does not publish images or deploy applications.

Wait for hosted CI on the final change before merging. Local Docker checks require a disposable Linux host; record unavailable checks honestly and use hosted CI to supply them. Never substitute a successful build for missing tests or security reviews.

## Versioned releases

Use one version tag such as `v0.2.0` pointing to the reviewed, tested `main` commit. Do not move or reuse existing version tags, and do not create a separate build tag on another branch. Existing historical tags are preserved; the restored release workflow requires its selected tag to include the maintained checks and tools.

For each authorized release:

1. Merge the reviewed change after successful CI. Review the exact commit, dependency notices, source license decision and branding provenance, release notes, installation/upgrade instructions and rollback guidance.
2. Create and push a new version tag for that approved commit only after explicit tag authorization. Never tag an unreviewed branch or assume source-push authorization includes tags.
3. With separate image-publication authorization, run **Publish container image** from `main` in GitHub Actions and provide the existing version tag. The workflow validates the version, checks out that exact tag with full history, requires dispatch from `main`, and verifies the tagged commit belongs to `main` history.
4. The workflow runs tagged Go, browser and installation tests, builds the Linux amd64 image with that version, checks a disposable installation and takeover, and scans both exact executables before publishing the version to GHCR. Only its publishing job has package-write permission; authentication uses stdin and temporary Docker configuration. Selected tagged scripts are trusted release code and must have been reviewed before dispatch.
5. Record the resulting immutable image digest and hosted results. Verify a fresh installation and applicable upgrade/rollback behavior before recommending the release. Vulnerability databases change; repeat scans for each release. Do not overwrite a published version with different content.
6. After explicit GitHub Release authorization, publish release notes for the same tag: changes, supported platform, versioned image and digest, installation/upgrade/rollback steps and known limitations. Attach only intentionally packaged, checked user-facing artifacts when needed, retaining required notices. A GitHub Release is a separate step; the image workflow does not create it automatically.

The workflow is deliberately manual. Ordinary pushes and tag pushes do not publish an image. Restoring release tooling does not itself create a tag, publish an image, create a GitHub Release or deploy an application. Older tags may lack the restored tools and must not be retagged to make this workflow run.

## Before publishing source or artifacts

Inspect the exact branch/ref, all history it will publish, the final tree and untracked files that might be staged. Review deleted files, author/committer identity and email, binary metadata, assets, release metadata, private endpoints, logs, CI permissions, dependencies and license/provenance. A current `.gitignore` does not clean history. Run a redacted secret scan and manual privacy review; never print secret matches or access known credential stores without authorization. Missing required publication checks block publication. Keep private evidence outside tracked source.

Use only intentional contributor identities and approved repository/package visibility. Push only the reviewed branch or tag, never `--all` or `--mirror`, and never force-push or rewrite history without explicit authorization. Use authenticated GitHub CLI for GitHub operations. Verify hosted CI and report the repository URL and pushed branch after an authorized push.

Dockspire's own source license remains undecided. Files under `licenses/` cover third-party components and do not grant a license to Dockspire itself. Do not call the project open source or make a public release without resolving the required licensing/provenance review. Branding retains its supplied [asset notes](branding/ASSET_LICENSE_NOTES.md).

This policy follows [GitHub flow](https://docs.github.com/en/get-started/using-github/github-flow), [manual workflow dispatch](https://docs.github.com/en/actions/how-tos/manage-workflow-runs/manually-run-a-workflow), and [GitHub Releases](https://docs.github.com/en/repositories/releasing-projects-on-github/about-releases). A complete source repository and a minimal installed product are compatible.
