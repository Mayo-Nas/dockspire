# Dockspire

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="web/brand/dockspire-horizontal-dark.png">
  <img src="docs/branding/dockspire-horizontal-light.png" alt="Dockspire — Dock. Deploy. Manage." width="560">
</picture>

Docker and Compose management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory. Multi-host management is planned. The implementation is original; no Portainer source is included.

## Install on a Linux VM

**Current source uses HTTP on port 8787 and browser administrator setup without a setup token.** This behavior is not in published v0.1.0; build the checkout to use it. Docker Engine and its Compose plugin are required; host Go and Node are unnecessary.

On a fresh Linux amd64 host without an existing Dockspire installation or `dockspire_data` volume:

```sh
gh repo clone Mayo-Nas/dockspire
cd dockspire
docker build -t dockspire:local .
cd deploy
export DOCKSPIRE_IMAGE=dockspire:local
docker compose up -d --pull never
```

Use an authenticated GitHub CLI account with repository access. If already cloned, start at `docker build` from the repository root. Keep Compose commands in `deploy/` and repeat the image export after reconnecting. Open `http://VM-IP:8787` and create the `admin` password within five minutes. Portainer can continue using 9443 alongside Dockspire.

HTTP carries passwords and sessions without encryption. Use a trusted management network: any reachable client can claim first administrator during the setup window. Choose any password, or explicitly leave both fields blank for persistent passwordless access: anyone who can reach Dockspire can then control this Docker host without signing in. Optional HTTPS and a pinned DNS origin remain available in the installation guide. No certificate fingerprint or log token is needed for HTTP setup.

The Compose file requires an explicit `DOCKSPIRE_IMAGE` (the source commands select `dockspire:local`). For `.env` configuration, optional HTTPS, persistence, or the existing published v0.1.0 HTTPS installation, see [installation and upgrades](docs/INSTALL.md). Existing running deployments are unchanged by this source update.

Docker packages remain managed by the host. Dockspire does not install or upgrade them automatically. The optional [Portainer import setup](docs/INSTALL.md#optional-portainer-configuration-import) saves configuration copies without changing workloads. Current source also offers a separate, explicitly confirmed [takeover review](docs/OPERATIONS.md#portainer-takeover) for supported self-contained stacks; importing alone never transfers management. This is unreleased functionality, unavailable in v0.1.0.

## Repository layout

```text
cmd/dockspire/       Executable entry point
internal/app/       Backend implementation and Go regression tests
web/                Browser interface, branding and embedded asset bundle
deploy/             Container installation, demo and optional host-helper service
scripts/            Installation checks, dependency notices and host maintenance
tests/              JavaScript and installation regression tests
docs/               Installation, development, operations and branding guides
licenses/           Third-party dependency notices
.github/            CI and pull-request template
```

The backend remains one internal application package. Feature files stay together without exposing implementation types or introducing extra services.

## Develop

Requires Go 1.27.2; Node 24 runs the browser-code regression tests. No frontend dependency installation or build is required.

```sh
go test ./...
go vet ./...
go build -o .local/bin/dockspire ./cmd/dockspire
node tests/test-auth.mjs
node tests/test-setup.mjs
node tests/test-navigation.mjs
node tests/test-stacks.mjs
node tests/test-revisions.mjs
```

See [development and checks](docs/DEVELOPMENT.md) for demo mode, source Docker builds and local workspace paths. Detailed application workflows and access/backup behavior are in the [operations manual](docs/OPERATIONS.md).

## Project status

The supported release target is Linux amd64 with Docker Engine and modern Compose. The application and installation workflow are covered by Go, UI and disposable-container tests. Other architectures have not been runtime-verified.

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). [Publication guidance](docs/GITHUB.md) covers source/privacy review and image releases. Dockspire's own source license remains undecided; files under `licenses/` cover YAML and bundled Docker Compose, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md).

Development checks and contributor information are in [the development guide](docs/DEVELOPMENT.md).
