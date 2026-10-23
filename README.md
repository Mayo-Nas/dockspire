# Dockspire

<a href="web/brand/dockspire-horizontal-dark.png"><img src="web/brand/dockspire-horizontal-dark.png" alt="Dockspire — Dock. Deploy. Manage." width="800"></a>

[Final branding assets and usage](docs/BRANDING.md) · [Transparent mark](web/brand/dockspire-icon-256.png) · [Horizontal logo for light surfaces](web/brand/dockspire-horizontal-dark.png)

Docker and Compose management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory.

Public-readiness fixes on current main are unreleased and are not included in v0.1.2. Use the [current source build](docs/INSTALL.md#build-and-start-the-current-source) to try them; a new tag/image requires a separate reviewed release. Repository and GHCR package visibility are independent. See [security reporting and homelab scope](SECURITY.md) and [release preparation](docs/GITHUB.md).

## Install on a Linux VM

**v0.1.2 uses HTTP on port 8787 and browser administrator setup without a setup token.** Docker Engine and its Compose plugin are required; host Go and Node are unnecessary. The historical v0.1.2 release notes describe private distribution; check the package's actual access requirements. Authenticate source access if the repository is private, and authenticate Docker separately for private package pulls as described in [installation](docs/INSTALL.md#install-v012). Making the source public does not change package access.

On a fresh Linux amd64 host without an existing Dockspire installation or `dockspire_data` volume:

```sh
gh repo clone Mayo-Nas/dockspire -- --branch v0.1.2
cd dockspire
cd deploy
export DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.1.2
docker compose pull app
docker compose up -d --no-deps app
```

Use the matching tagged files for a fresh installation; existing installations must follow [upgrade and rollback](docs/INSTALL.md#upgrade-and-rollback). Keep Compose commands in `deploy/` and repeat the image export after reconnecting, or persist the selected image in a private `.env`. Open `http://VM-IP:8787` and create the `admin` password within five minutes.

HTTP carries passwords and sessions without encryption. Use a trusted management network: any reachable client can claim first administrator during the setup window. Choose any password, or explicitly leave both fields blank for persistent passwordless access: anyone who can reach Dockspire can then control this Docker host without signing in. Optional HTTPS and a pinned DNS origin remain available in the installation guide. No certificate fingerprint or log token is needed for HTTP setup.

The Compose file requires an explicit `DOCKSPIRE_IMAGE`. For an immutable image reference, `.env` configuration, source builds, optional HTTPS, persistence, or v0.1.0 compatibility, see [installation and upgrades](docs/INSTALL.md) and the [v0.1.2 release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.1.2). Publishing a release does not update existing running deployments.

Docker packages remain managed by the host. Dockspire does not install or upgrade them automatically.

## Repository layout

```text
cmd/dockspire/       Executable entry point
internal/app/       Backend implementation
web/                Browser interface, branding and embedded asset bundle
deploy/             Container installation and optional host-helper service
scripts/            Installation checks, host maintenance and development tools
tests/              Browser and installation regression tests
.github/            CI, dependency maintenance and manual image publication
docs/               Installation, operations and asset notices
licenses/           Third-party dependency notices
```

The backend remains one internal application package. Feature files stay together without exposing implementation types or introducing extra services.

Detailed application workflows and access/backup behavior are in the [operations manual](docs/OPERATIONS.md). Source build requirements and checks are in [development](docs/DEVELOPMENT.md); releases are covered by [repository and release policy](docs/GITHUB.md).

## Project status

The supported release target is Linux amd64 with Docker Engine and modern Compose. The application and installation workflow are covered by Go, UI and disposable-container tests. Other architectures have not been runtime-verified.

v0.1.2 is the branding and security maintenance release: refreshed supplied artwork, consistent navigation icons and a Go 1.27.3 build toolchain. Before using the versioned installation commands above, confirm its release notes record the verified private image and immutable digest. Source changes and version tags alone do not publish images or update running installations.

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Dockspire is licensed under [Apache 2.0](LICENSE); attribution is in [NOTICE](NOTICE). Files under `licenses/` retain third-party notices, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md). See [repository and release guidance](docs/GITHUB.md).
