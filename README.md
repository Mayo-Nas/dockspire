# Dockspire

<img src="web/brand/dockspire-horizontal-dark.png" alt="Dockspire — Dock. Deploy. Manage." width="800">

Docker management for self-hosted Linux servers. Dockspire provides live inventory, container diagnostics, a saved Compose stack library, reviewed deployments and updates, configuration history, and portable backups.

It runs as one small application container with an embedded browser interface and one persistent data directory.

See [security reporting and homelab scope](SECURITY.md) and [Releases](https://github.com/Mayo-Nas/dockspire/releases) for image availability, digests, validation and access requirements.

## Install on a Linux VM

Choose [Docker Compose](#docker-compose-recommended) for a saved configuration file, or [Docker run](#docker-run) for a single command. These examples target v0.3.0; confirm image availability on [Releases](https://github.com/Mayo-Nas/dockspire/releases) before installing. Public images need no registry login.

You need Docker Engine on a Linux amd64 server and permission to run Docker commands. Compose installation also needs the host Docker Compose plugin (`docker compose version`); `docker run` does not. Dockspire bundles Compose for managing your stacks, so host Go and Node are unnecessary.

Use only one method on a fresh host without an existing Dockspire installation or `dockspire_data` volume. Existing installations must follow [upgrade and rollback](docs/INSTALL.md#upgrade-and-rollback).

### Docker Compose (recommended)

To install using Docker Compose, download the Compose file into a new, empty installation directory:

```sh
curl --fail -L https://raw.githubusercontent.com/Mayo-Nas/dockspire/main/deploy/compose.yaml -o dockspire-compose.yaml
```

Alternatively, create a `dockspire-compose.yaml` file with the following contents:

```yaml
name: dockspire
services:
  app:
    image: ${DOCKSPIRE_IMAGE:-ghcr.io/mayo-nas/dockspire:v0.3.0}
    command: ["--allow-http", "--setup", "--data-dir", "/appdata", "--external-url", "${DOCKSPIRE_EXTERNAL_URL:-}"]
    restart: unless-stopped
    ports:
      - "${DOCKSPIRE_BIND_ADDRESS:-0.0.0.0}:${DOCKSPIRE_HTTP_PORT:-8787}:8787"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
      - dockspire_data:/appdata
    read_only: true
    tmpfs:
      - /tmp:rw,noexec,nosuid,size=64m
    cap_drop: [ALL]
    security_opt: ["no-new-privileges:true"]
    mem_limit: 256m
    cpus: 0.5
    pids_limit: 64
    healthcheck:
      test: ["CMD", "/dockspire", "--healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 20s
volumes:
  dockspire_data:
    name: dockspire_data
```

Once you have created or downloaded the Compose file, deploy Dockspire with:

```sh
docker compose -f dockspire-compose.yaml up -d
```

Compose creates the necessary resources, including the persistent data volume. Check that the container has started:

```sh
docker ps
```

Continue with [Logging in](#logging-in). No `.env` file or image export is required. For optional settings and diagnostics, see [Docker Compose installation](docs/INSTALL.md#install-with-docker-compose).

### Docker run

First, create the volume that Dockspire will use to store its data:

```sh
docker volume create dockspire_data
```

Then, download and start the Dockspire container:

```sh
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
  ghcr.io/mayo-nas/dockspire:v0.3.0
```

Check that the container has started:

```sh
docker ps
```

For optional settings and diagnostics, see [Docker run installation](docs/INSTALL.md#install-with-docker).

### Logging in

Now that installation is complete, open a web browser and go to:

```text
http://localhost:8787
```

Replace `localhost` with your server's IP address when browsing from another computer, and adjust the port if you changed it. For a DNS name, configure the exact external origin using the [Compose settings](docs/INSTALL.md#optional-compose-settings-and-diagnostics) or [Docker run settings](docs/INSTALL.md#optional-docker-run-settings).

You will be presented with Dockspire's initial setup page. Create the `admin` password within five minutes. If setup expires, run `docker compose -f dockspire-compose.yaml restart app` from the installation directory, or `docker restart dockspire` for Docker run. See [administrator setup](docs/INSTALL.md#create-the-administrator).

HTTP carries passwords and sessions without encryption. Use a trusted management network: any reachable client can claim first administrator during the setup window. Choose any password, or explicitly leave both fields blank for persistent passwordless access: anyone who can reach Dockspire can then control this Docker host without signing in. Optional HTTPS and a pinned DNS origin remain available in the installation guide. No certificate fingerprint or log token is needed for HTTP setup.

The Docker socket grants Dockspire control of the host, including when mounted `:ro`. Both methods store Dockspire's data in `dockspire_data` at `/appdata`; do not start a second manager against that volume. For immutable image references, DNS access, optional HTTPS and backups, see [installation and upgrades](docs/INSTALL.md). Publishing a release does not update running deployments.

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

v0.3.0 uses Dockspire names consistently for the executable, browser protocol, archives and optional host helper. It is a clean break from previous internal archive naming. Before upgrading, take a private whole-data backup and retain the previous image. Check [Releases](https://github.com/Mayo-Nas/dockspire/releases) for availability and validation.

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Dockspire is licensed under [Apache 2.0](LICENSE); attribution is in [NOTICE](NOTICE). Files under `licenses/` retain third-party notices, and branding retains its supplied [asset notes](docs/branding/ASSET_LICENSE_NOTES.md). See [repository and release guidance](docs/GITHUB.md).
