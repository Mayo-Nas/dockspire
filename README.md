# Dockspire

<picture>
  <source media="(prefers-color-scheme: dark)" srcset="web/brand/dockspire-horizontal-dark.png">
  <img src="docs/branding/dockspire-horizontal-light.png" alt="Dockspire — Dock. Deploy. Manage." width="560">
</picture>

Docker and Compose management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory. Multi-host management is planned. The implementation is original; no Portainer source is included.

## Install on a Linux VM

The recommended installation uses a **versioned container image and Docker Compose**, with administrator setup in your browser and direct HTTPS. The image bundles the application and its Compose executable. The VM needs Docker Engine and the Compose plugin to launch it; no Go, Node or source build is required.

1. Install Docker Engine and the Compose plugin using your distribution's supported Docker packages.
2. Download the release's [Compose configuration](deploy/compose.yaml) and select the published image version.
3. Run `docker compose up -d`, then open `https://VM-IP:9443` (choose another host port if Portainer already uses 9443).
4. Verify the certificate fingerprint through a trusted VM console/SSH session as described in the installation guide. Then copy the one-time setup token from `docker compose logs app`, create the administrator within five minutes, and sign in. Trusted custom certificates are also supported.

**No source, release or container image has been published yet.** Registry installation becomes available after an explicitly approved push and image publication. See [Linux installation and upgrades](docs/INSTALL.md) for complete commands, private-registry access, certificates, persistent storage and rollback. [Offline archives](docs/INSTALL-ARCHIVE.md) remain an alternative.

Docker packages remain managed by the host. Dockspire does not install or upgrade them automatically. Existing Portainer projects remain view-only unless a future migration is explicitly requested.

## Repository layout

```text
cmd/dockspire/       Executable entry point
internal/app/       Backend implementation and Go regression tests
web/                Browser interface, branding and embedded asset bundle
deploy/             Development configuration and compatible release templates
scripts/            Packaging, installation and legacy administration tools
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

See [development and packaging](docs/DEVELOPMENT.md) for demo mode, source Docker builds and local workspace paths. Detailed application workflows and access/backup behavior are in the [operations manual](docs/OPERATIONS.md).

## Project status

The supported release target is Linux amd64 with Docker Engine and modern Compose. The application and installation workflow are covered by Go, UI and disposable-container tests. Other architectures have not been runtime-verified.

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). [Publication guidance](docs/GITHUB.md) covers source/privacy review and image releases. Dockspire's own source license remains undecided; files under `licenses/` cover YAML and bundled Docker Compose, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md).

Development checks, packaging, and contributor information are in [the development guide](docs/DEVELOPMENT.md).
