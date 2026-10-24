# Install Dockspire on Linux

You need Docker Engine on a Linux amd64 server and permission to run Docker commands. Docker Compose installation also needs the host Compose plugin (`docker compose version`). Dockspire bundles Compose for managing stacks; host Go and Node are unnecessary.

Choose one method for a fresh installation. Existing installations should use [Upgrade and rollback](#upgrade-and-rollback). Examples target `v0.3.0`; check [Releases](https://github.com/Mayo-Nas/dockspire/releases) for image availability and its digest before installing.

Use a trusted management network: HTTP is unencrypted, and Docker socket access gives Dockspire control of the host even when mounted `:ro`. Both methods use `dockspire_data` at `/appdata`; keep that volume and run only one manager against it.

## Install with Docker Compose

To install using Docker Compose, download the Compose file using the following `curl` command. Use a new, empty installation directory:

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

Once you have created or downloaded the Compose file, deploy it with the following command:

```sh
docker compose -f dockspire-compose.yaml up -d
```

Docker Compose creates the necessary resources and starts Dockspire. Check that the container has started:

```sh
docker ps
```

Continue with [Logging in](#logging-in).

No `.env` file or image export is required. The maintained Compose file defaults to the v0.3.0 image reference. Keep your saved `dockspire-compose.yaml` and run subsequent commands from its directory. The template fixes project `dockspire`, service `app` and volume `dockspire_data`; another directory does not isolate an existing installation. Data persists in that volume at `/appdata`.

## Install with Docker

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

Continue with [Logging in](#logging-in). Save your complete `docker run` command for future upgrades. The data survives container replacement in `dockspire_data`; never remove that volume to upgrade or reset access.

## Logging in

Open a browser and go to:

```text
http://localhost:8787
```

Replace `localhost` with the server IP when browsing remotely, and adjust the port if changed. A DNS name needs the exact external origin configured in the [Compose](#optional-compose-settings-and-diagnostics) or [Docker run](#optional-docker-run-settings) settings.

You will be presented with the initial setup page.

### Create the administrator

Create the `admin` password within five minutes. Any reachable client can claim the first administrator while setup is open. Explicitly leaving both fields blank enables persistent passwordless access; anyone who can reach it can control the host.

If setup expires, run `docker compose -f dockspire-compose.yaml restart app` or `docker restart dockspire`, using your installation method. See [administrator access and password recovery](OPERATIONS.md#administrator-access) for detailed access behavior.

## Upgrade and rollback

Keep the installation method, data volume and existing settings. These steps cover the standard installation above; customized/source-built installations must retain their files, mounts, arguments and overrides.

### Compose installations

These commands assume the base-only standard installation. Include every existing `-f` override in each command. After a previous rollback, keep its recovered-volume override last and update the image there.

1. Take a [stopped whole-data backup](OPERATIONS.md#whole-data-backup), including administrator/TLS state, and retain the previous image. Library exports are not a whole-data backup.
2. Save your current Compose file and set its complete `image:` value to the released version or digest you want to install.
3. Recreate only Dockspire:

```sh
docker compose -f dockspire-compose.yaml pull app
docker compose -f dockspire-compose.yaml up -d --no-deps app
docker ps
```

### Docker run installations

1. Take the same [stopped whole-data backup](OPERATIONS.md#whole-data-backup) and retain the previous image and full run command.
2. Follow the [copy-and-paste Docker run upgrade](OPERATIONS.md#upgrade-a-docker-run-installation) to replace only the stopped manager while preserving its data and runtime settings.
3. Sign in again and check inventory, saved stacks and installation status. Managed workloads do not need restarting.

### Roll back

1. Stop Dockspire using the same configuration that started it.
2. Follow [restore the backup first](OPERATIONS.md#roll-back-restore-the-backup-first): restore the compatible pre-upgrade archive into a fresh volume, then start the retained old image against that volume.
3. Verify administrator/TLS state and saved stacks before resuming changes. Keep the upgraded volume separately until recovery is verified.

Image-only rollback can fail after new-format writes. Restore the original Docker tar stream with its numeric ownership and private permissions; do not re-pack it. Workload data needs a separate backup and restore plan.

## Optional configuration

### Optional Compose settings and diagnostics

See [image pinning, ports, DNS, loopback binding and diagnostics](OPERATIONS.md#compose-settings-and-diagnostics). A fresh installation needs no `.env` file.

### Optional Docker run settings

See [image pinning, loopback binding and DNS arguments](OPERATIONS.md#docker-run-settings).

## Optional HTTPS

See [HTTPS and certificate setup](OPERATIONS.md#optional-https), including explicit override selection and certificate verification.

## Optional Portainer configuration import

See [Portainer file import and optional source-access setup](OPERATIONS.md#optional-portainer-configuration-import), then the separate [takeover workflow](OPERATIONS.md#portainer-takeover). Import alone does not adopt or stop existing workloads.

## Build and start the current source

See [source-build commands](OPERATIONS.md#build-and-start-the-current-source) and [development checks](DEVELOPMENT.md). Keep an explicit local-image selection when using a source build.
