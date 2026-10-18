# Contributing to Dockspire

Start from current `main`, create a focused feature or fix branch, and open a pull request back to `main`. The complete maintained project belongs on `main`: application source, tests, build and release tooling, dependency manifests, notices and documentation. Keep private assistant settings, credentials, caches, diagnostics and generated binaries out of Git. The legacy `development` branch is historical, not an active contribution target.

Before merging:

1. Explain the problem, resulting behavior, validation and deployment impact in the pull request.
2. Run the applicable [development checks](docs/DEVELOPMENT.md) and wait for all hosted CI checks on the final change. Container checks require a disposable Linux Docker host.
3. Obtain separate independent code and security reviews of the final tree and diff, including new files, scripts and workflow changes. Fix blocking findings, retest and repeat affected reviews. Keep private review records outside tracked source; identify the exact reviewed tree and checks.
4. Check that user documentation and dependency notices match the change. Preserve installation and saved-data contracts unless an explicit migration is approved.
5. Merge the reviewed pull request normally. Never force-push shared branches or rewrite published history without explicit approval.

Branch protection and required status checks should enforce these rules when the repository plan supports them. They are currently a manually followed policy because the private repository's plan does not provide the required protection/ruleset capability. CI running is not proof that reviews or merge gates are automatically enforced.

Keep Docker images lean through the multistage Dockerfile, and package user-facing release files separately when needed. Do not remove tests or CI from `main` to reduce an installation artifact. Normal development and CI do not publish images or deploy applications.

Follow [repository and release policy](docs/GITHUB.md) for tagged releases and publication approvals. Dockspire's own source license is undecided; third-party notices do not license this project's source. Do not describe it as open source until an appropriate license is chosen and reviewed.