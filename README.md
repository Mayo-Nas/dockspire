# Dockspire

`main` contains the complete maintained source, tests, CI and release tooling. Installation uses a minimal runtime image; contributor files are not shipped in that image.

<img src="web/brand/dockspire-icon-256.png" alt="Dockspire mark" width="96">

Dock. Deploy. Manage.

Docker and Compose management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory. Multi-host management is planned. The implementation is original; no Portainer source is included.

## Install on a Linux VM

**v0.1.1 uses HTTP on port 8787 and browser administrator setup without a setup token.** Docker Engine and its Compose plugin are required; host Go and Node are unnecessary. The repository and image are private: use an authenticated GitHub CLI account with repository access, and authenticate Docker to GHCR with package access as described in [installation](docs/INSTALL.md#install-the-published-v011-image).

On a fresh Linux amd64 host without an existing Dockspire installation or `dockspire_data` volume:

```sh
gh repo clone Mayo-Nas/dockspire -- --branch v0.1.1
cd dockspire
cd deploy
export DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.1.1
docker compose pull app
docker compose up -d --no-deps app
```

Use the matching tagged files for a fresh installation; existing installations must follow [upgrade and rollback](docs/INSTALL.md#upgrade-and-rollback). Keep Compose commands in `deploy/` and repeat the image export after reconnecting, or persist the selected image in a private `.env`. Open `http://VM-IP:8787` and create the `admin` password within five minutes. Portainer can continue using 9443 alongside Dockspire.

HTTP carries passwords and sessions without encryption. Use a trusted management network: any reachable client can claim first administrator during the setup window. Choose any password, or explicitly leave both fields blank for persistent passwordless access: anyone who can reach Dockspire can then control this Docker host without signing in. Optional HTTPS and a pinned DNS origin remain available in the installation guide. No certificate fingerprint or log token is needed for HTTP setup.

The Compose file requires an explicit `DOCKSPIRE_IMAGE`. For an immutable image reference, `.env` configuration, source builds, optional HTTPS, persistence, or v0.1.0 compatibility, see [installation and upgrades](docs/INSTALL.md) and the [v0.1.1 release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.1.1). Publishing a release does not update existing running deployments.

Docker packages remain managed by the host. Dockspire does not install or upgrade them automatically. The optional [Portainer import setup](docs/INSTALL.md#optional-portainer-configuration-import) saves configuration copies without changing workloads. v0.1.1 also offers a separate, explicitly confirmed [takeover review](docs/OPERATIONS.md#portainer-takeover) for supported self-contained stacks; importing alone never transfers management. Takeover is unavailable in v0.1.0.

## Repository layout

```text
cmd/dockspire/       Executable entry point
internal/app/       Backend implementation
web/                Browser interface, branding and embedded asset bundle
deploy/             Container installation and optional host-helper service
scripts/            Installation checks, host maintenance and contributor tools
tests/              Browser and installation regression tests
.github/            CI, manual image publication and pull request template
docs/               Installation, operations and asset notices
licenses/           Third-party dependency notices
```

The backend remains one internal application package. Feature files stay together without exposing implementation types or introducing extra services.

## Contribute

Create a short-lived feature branch from `main`, open a pull request to `main`, and run the documented checks before merging. Tests, contributor tools and release workflows stay alongside the application source. The legacy `development` branch has been removed; use short-lived branches and pull requests to `main`.

See [CONTRIBUTING.md](CONTRIBUTING.md), [development and checks](docs/DEVELOPMENT.md), and [repository and release policy](docs/GITHUB.md). Native source builds require Go 1.27.2; Node 24 runs browser regression tests. Published-image installation requires no host Go or Node; optional source builds run in Docker. Detailed application workflows and access/backup behavior are in the [operations manual](docs/OPERATIONS.md).

## Project status

The supported release target is Linux amd64 with Docker Engine and modern Compose. The application and installation workflow are covered by Go, UI and disposable-container tests. Other architectures have not been runtime-verified.

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). [Repository and publication guidance](docs/GITHUB.md) covers source/privacy review and image releases. Dockspire's own source license remains undecided; files under `licenses/` cover YAML and bundled Docker Compose, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md).
