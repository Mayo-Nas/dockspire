# Dockspire

<a href="web/brand/dockspire-horizontal-dark.png"><img src="web/brand/dockspire-horizontal-dark.png" alt="Dockspire — Dock. Deploy. Manage." width="800"></a>

[Final branding assets and usage](docs/BRANDING.md) · [Transparent mark](web/brand/dockspire-icon-256.png) · [Horizontal logo for light surfaces](web/brand/dockspire-horizontal-dark.png)

Docker and Compose management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory.

See [security reporting and homelab scope](SECURITY.md) and the [v0.2.0 release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.2.0) for the image digest, validation and access requirements.

## Install on a Linux VM

Choose [Docker Compose](#docker-compose-recommended) for a saved configuration file, or [Docker run](#docker-run) for a single command. Both use the public v0.2.0 image without a registry login. See its [release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.2.0) for validation and the immutable image digest.

You need Docker Engine on a Linux amd64 server and permission to run Docker commands. Compose installation also needs the host Docker Compose plugin (`docker compose version`); `docker run` does not. Dockspire bundles Compose for managing your stacks, so host Go and Node are unnecessary.

Use only one method on a fresh host without an existing Dockspire installation or `dockspire_data` volume. Existing installations must follow [upgrade and rollback](docs/INSTALL.md#upgrade-and-rollback).

### Docker Compose (recommended)

Create a new installation directory and download the version-matched Compose file. Stop if directory creation fails; use a new, empty directory.

```sh
mkdir dockspire && cd dockspire
curl --fail --location --output compose.yaml https://raw.githubusercontent.com/Mayo-Nas/dockspire/v0.2.0/deploy/compose.yaml
```

Create `.env` beside `compose.yaml` with:

```dotenv
DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.2.0
```

Then validate and start Dockspire from that directory:

```sh
unset DOCKSPIRE_IMAGE
docker compose config --quiet
docker compose pull app
docker compose up -d --no-deps app
docker compose ps
```

Compose creates the persistent `dockspire_data` volume automatically. Check startup with `docker compose logs app` and `docker compose exec -T app /stacklane --healthcheck`. For the complete copy-and-paste YAML and configuration options, see [Docker Compose installation](docs/INSTALL.md#install-with-docker-compose).

### Docker run

Create the persistent volume and start the container:

```sh
docker volume create dockspire_data
docker run -d \
  --name dockspire \
  --restart unless-stopped \
  -p 8787:8787 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v dockspire_data:/appdata \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --memory 256m --cpus 0.5 --pids-limit 64 \
  ghcr.io/mayo-nas/dockspire:v0.2.0
```

Check startup with `docker ps --filter name=^/dockspire$`, `docker logs dockspire` and `docker exec dockspire /stacklane --healthcheck`. See [Docker run installation](docs/INSTALL.md#install-with-docker) for configuration options.

### Open Dockspire

Open `http://SERVER-IP:8787` and create the `admin` password within five minutes. Replace `SERVER-IP` with your server's IP address, or use `localhost` when browsing on the server. If setup expires, run `docker compose restart app` from the installation directory, or `docker restart dockspire` for Docker run. See [administrator setup](docs/INSTALL.md#create-the-administrator).

HTTP carries passwords and sessions without encryption. Use a trusted management network: any reachable client can claim first administrator during the setup window. Choose any password, or explicitly leave both fields blank for persistent passwordless access: anyone who can reach Dockspire can then control this Docker host without signing in. Optional HTTPS and a pinned DNS origin remain available in the installation guide. No certificate fingerprint or log token is needed for HTTP setup.

The Docker socket grants Dockspire control of the host, including when mounted `:ro`. Both methods store Dockspire's data in `dockspire_data` at `/appdata`; do not start a second manager against that volume. For immutable image references, DNS access, optional HTTPS, persistence and older installations, see [installation and upgrades](docs/INSTALL.md). Publishing a release does not update running deployments.

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

v0.2.0 includes Apache 2.0 licensing, bounded log tails and subprocess output, stricter bundled Compose checks, presence-aware configuration identities, and expanded browser and upgrade/rollback verification. Before upgrading, take a private whole-data backup: new-format writes require that compatible backup for rollback to an older image. See the [release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.2.0).

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Dockspire is licensed under [Apache 2.0](LICENSE); attribution is in [NOTICE](NOTICE). Files under `licenses/` retain third-party notices, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md). See [repository and release guidance](docs/GITHUB.md).
