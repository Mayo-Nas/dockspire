# Dockspire project instructions

Read [CONTRIBUTING.md](CONTRIBUTING.md), [development checks](docs/DEVELOPMENT.md), and [repository/release policy](docs/GITHUB.md) before changing this project.

- Inspect Git status and existing code first; preserve other contributors' work.
- Keep `main` a complete source repository, including tests, CI, build/release tools and documentation. Use focused feature branches and pull requests to `main`; the legacy `development` branch has been removed.
- Keep runtime images and user downloads lean through packaging. Do not split maintained source and tests across branches.
- Prefer simple, maintainable changes with minimal dependencies. Ask before major architectural changes and document migrations affecting installations or saved data.
- Run appropriate checks. Independent code and security reviews must cover the final tree and diff, including new files and executable configuration, before commit and completion. Fix blocking findings and repeat affected checks/reviews; never self-approve or claim unavailable checks passed.
- Keep review evidence, assistant settings, credentials, environment values, diagnostics and generated files outside tracked source. Do not access credential stores without explicit authorization or expose secret matches.
- Source pushes, remote repository creation, version tags, image publication, GitHub Releases, deployment and public visibility each need explicit authorization for that action. Never force-push or rewrite published history without explicit approval.
- Release one reviewed, tested version tag from `main`. Dispatch the image publisher from `main` for that tag; keep its full checks and executable scans. Do not invent a second build tag or selectively promote files.
- Preserve Dockspire's Apache 2.0 LICENSE and NOTICE in source and runtime images, together with third-party notices and branding provenance.

These are persistent project instructions, not automatically enforced gates. Repository protection availability and release details are documented in [docs/GITHUB.md](docs/GITHUB.md).
