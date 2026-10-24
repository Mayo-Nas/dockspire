# Install Dockspire on a Linux VM

Dockspire v0.2.0 runs as one container with a persistent data volume. Choose one installation method:

| Method | Use it when | Host requirements |
| --- | --- | --- |
| [Docker Compose (recommended)](#install-with-docker-compose) | You want a saved configuration file and automatic healthchecks. | Docker Engine and the Docker Compose plugin |
| [Docker run](#install-with-docker) | You prefer a single container command. | Docker Engine |

Both methods require a Linux amd64 server and permission to run Docker commands. Check Docker with `docker version`; for Compose, also check `docker compose version`. Dockspire bundles its own Compose executable for managing stacks; host Go and Node are unnecessary.

These fresh-install commands use HTTP on 8787 and browser administrator setup. Existing installations must follow [upgrade and rollback](#upgrade-and-rollback), retaining their configuration and data. v0.2.0 writes newer configuration identities and version-2 library archives; take a private whole-data backup before upgrading.

The public v0.2.0 image can be pulled without a registry login. The [release notes](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.2.0) record its immutable digest and validation. Both methods below select that version deliberately.

Use a fresh Linux amd64 host without an existing Dockspire installation, `dockspire` container or `dockspire_data` volume. Do not run two managers against the same data. HTTP exposes passwords and sessions without encryption, and any reachable client can claim first administrator while setup is open. Restrict access to a trusted management network. The Docker socket grants control of the host; mounting it `:ro` does not make the Docker API read-only.

## Install with Docker Compose

Compose keeps your installation settings in `compose.yaml` and the selected image in `.env`. It creates the data volume automatically. Use this method for optional HTTPS or Portainer source-copy access described later in this guide.

### 1. Create an installation directory

Run these commands on your Docker host. Stop if directory creation fails; choose a new, empty directory instead of replacing an existing installation's files.

```sh
mkdir dockspire && cd dockspire
```

### 2. Download or create the Compose file

Download the version-matched template:

```sh
curl --fail --location --output compose.yaml https://raw.githubusercontent.com/Mayo-Nas/dockspire/v0.2.0/deploy/compose.yaml
```

Alternatively, create `compose.yaml` in the installation directory and paste the complete configuration below. It matches the [release template](https://github.com/Mayo-Nas/dockspire/blob/v0.2.0/deploy/compose.yaml):

```yaml
name: dockspire
services:
  app:
    image: ${DOCKSPIRE_IMAGE:?Set DOCKSPIRE_IMAGE to a published version or image digest}
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
      test: ["CMD", "/stacklane", "--healthcheck"]
      interval: 30s
      timeout: 5s
      retries: 3
      start_period: 20s
volumes:
  dockspire_data:
    name: dockspire_data
```

### 3. Select the image and optional settings

Create `.env` beside `compose.yaml` with the following contents. This saves the image selection across shell sessions:

```dotenv
DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.2.0
# Optional HTTP port when 8787 is occupied:
# DOCKSPIRE_HTTP_PORT=8788
# Optional exact origin for a DNS name:
# DOCKSPIRE_EXTERNAL_URL=http://docker.example.test:8787
# Optional loopback-only publication:
# DOCKSPIRE_BIND_ADDRESS=127.0.0.1
```

For an immutable image, replace the `DOCKSPIRE_IMAGE` value with the complete `ghcr.io/mayo-nas/dockspire@sha256:...` reference from the release notes. The HTTP template accepts IP addresses and localhost by default. DNS access requires an exact `http://...` external URL; forwarded headers are ignored. With `DOCKSPIRE_HTTP_PORT=8788`, open `http://SERVER-IP:8788` and include that port in any external URL. Loopback-only publication allows access from the server or a verified SSH tunnel.

### 4. Start and verify Dockspire

Run from the installation directory. Unset an old shell image export so the saved `.env` value is used:

```sh
unset DOCKSPIRE_IMAGE
docker compose config --quiet
docker compose pull app
docker compose up -d --no-deps app
docker compose ps
docker compose logs --tail 50 app
docker compose exec -T app /stacklane --healthcheck
```

Stop and correct any validation or pull error before starting. `docker compose ps` should show `app` running; its automatic healthcheck may take a short time to report healthy. Open `http://SERVER-IP:8787` and continue with [administrator setup](#create-the-administrator).

Keep `compose.yaml` and `.env`, and run subsequent Compose commands from their directory. Shell exports override matching `.env` values. The template fixes project `dockspire`, service `app` and volume `dockspire_data`; another directory does not isolate an existing installation. Dockspire's saved data persists in that volume at `/appdata`. Never use `docker compose down -v` to upgrade or reset access.

## Install with Docker

Use `docker run` if you do not want to manage a Compose file. Create the persistent data volume, then start Dockspire:

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

Check that the container is running and healthy:

```sh
docker ps --filter name=^/dockspire$
docker logs --tail 50 dockspire
docker exec dockspire /stacklane --healthcheck
```

Open `http://SERVER-IP:8787` and continue with [administrator setup](#create-the-administrator). If setup expires, run `docker restart dockspire`. Save your complete `docker run` command for future upgrades. The data survives container replacement in `dockspire_data`; never remove that volume to upgrade or reset access.

To pin an immutable image, replace the versioned image above with the complete `ghcr.io/mayo-nas/dockspire@sha256:...` reference from the release notes. To publish only on loopback, replace `-p 8787:8787` with `-p 127.0.0.1:8787:8787`. For DNS access, append `--allow-http --setup --data-dir /appdata --external-url http://docker.example.test:8787` after the image name, using your exact origin. App arguments go after the image and replace its default command.

## Build and start the current source

Use a fresh host without an existing Dockspire installation or `dockspire_data` volume. The template has fixed project `dockspire` and volume `dockspire_data`; a second checkout does not isolate an existing installation. HTTP exposes passwords/sessions without encryption; restrict access to your trusted management network. Any reachable client can claim first administrator while setup is open.

```sh
git clone --branch main https://github.com/Mayo-Nas/dockspire.git
cd dockspire
docker build -t dockspire:local .
cd deploy
export DOCKSPIRE_IMAGE=dockspire:local
docker compose up -d --pull never
```

Already cloned? Start at `docker build` from the repository root. Private source requires authenticated source access; private packages require separate registry access. Docker downloads its build toolchain/dependencies. Keep subsequent Compose commands in `deploy/`; after reconnecting, return there and export `DOCKSPIRE_IMAGE=dockspire:local` again. Default Compose discovery loads `compose.override.yaml` beside `compose.yaml`.

Alternatively, manually select a matching built image in `.env` beside `compose.yaml`; preserve existing settings:

```dotenv
DOCKSPIRE_IMAGE=dockspire:local
# Optional HTTP port when 8787 is occupied:
# DOCKSPIRE_HTTP_PORT=8788
# Optional exact origin for a DNS name:
# DOCKSPIRE_EXTERNAL_URL=http://docker.example.test:8787
# Optional loopback-only publication:
# DOCKSPIRE_BIND_ADDRESS=127.0.0.1
```

`DOCKSPIRE_IMAGE` is required; a shell export overrides `.env`. An unset/empty value produces the Compose image-selection error before startup. Downloading Compose alone does not provide an image. The HTTP template accepts IP addresses and localhost by default. DNS access requires an exact `http://...` external URL; forwarded headers are ignored. With `DOCKSPIRE_HTTP_PORT=8788`, use `http://VM-IP:8788`.

## Create the administrator

Open `http://SERVER-IP:8787`. Username is fixed to `admin`; enter and confirm any password, with no password length or composition policy. Explicitly leaving both fields blank selects passwordless access, opens the dashboard immediately and persists across visits/restarts without a sign-in page. Anyone who can reach a passwordless instance can control its Docker host; restrict network access accordingly. Existing password accounts are not converted. No setup token, certificate or fingerprint is requested. Setup is open for five minutes after startup; `docker restart dockspire` opens a new window if it expires (`docker compose restart app` for Compose installations). All attempts are rate-limited per transport peer (NAT/proxy clients share an allowance); wait a minute after throttling or restart. Setup cannot replace an existing administrator, and missing/invalid stored access modes never imply passwordless access.

Authentication JSON requests have a generic 1 MiB body limit, including field names, JSON escaping and both setup fields. This transport ceiling applies even though passwords have no separate minimum, maximum or character policy. Use matching strings; omitted or null fields cannot enable passwordless mode.

The password hash and library persist in `dockspire_data` at `/appdata`; sessions end on restart. Back up the stopped app's data privately and workload data separately. Library exports exclude administrator credentials and TLS keys. Never run `docker compose down -v` unless intending to erase this installation. Offline password recovery remains in [operations](OPERATIONS.md#administrator-access); do not delete `admin.json` or create an empty volume to reset a password.

## Optional HTTPS

This section uses the Compose installation. For an existing `docker run` installation, retain its current settings and use a deliberate migration; do not start another manager against the same volume.

Native `--https` still defaults to 9443; the container can serve TLS on its existing 8787 mapping. Set `DOCKSPIRE_TLS_HOSTS` to your DNS/IP before first TLS startup and save `compose.override.yaml` beside `compose.yaml`:

```yaml
services:
  app:
    command: ["--https", "--listen", ":8787", "--setup", "--data-dir", "/appdata", "--tls-hosts", "${DOCKSPIRE_TLS_HOSTS:-}", "--external-url", "${DOCKSPIRE_EXTERNAL_URL:-}"]
    healthcheck:
      test: ["CMD", "/stacklane", "--healthcheck", "--https", "--listen", "127.0.0.1:8787"]
```

Run `docker compose up -d --no-deps app` and use `https://VM-IP:8787`. Any external URL must use `https://` in this mode. For trusted certificates, mount a private certificate directory read-only and add `--tls-cert /certs/cert.pem --tls-key /certs/key.pem` to the command; keep the listener/healthcheck settings above. Include matching certificate SANs and renew/restart externally.

Generated self-signed TLS identities persist; existing identities are not silently replaced to add SANs. Authenticate a self-signed certificate before entering credentials. From a trusted VM console/SSH session, `docker compose exec -T app /stacklane --tls-fingerprint --data-dir /appdata` prints its public SHA-256 fingerprint for comparison with the browser certificate viewer. This is an optional HTTPS trust diagnostic, not a setup token. Custom certificates need `--tls-cert` pointing to the served public certificate for this check. Saved TLS keys remain private even when HTTP is used.

An existing TLS reverse proxy can use the native HTTP listener with `--listen 0.0.0.0:8787 --setup --data-dir /appdata --external-url https://docker.example.test` (without `--allow-http`). Restrict that backend to loopback/a private proxy network and preserve Host. Secure browser cookies follow the pinned HTTPS origin; forwarded headers never authorize another origin. `--allow-http` requires an HTTP external URL; direct `--https` requires HTTPS.

## v0.1.1 compatibility

The v0.1.0 and v0.1.1 GHCR versions have been retired; fresh pulls are unavailable. Their Git tags, source and release history remain available for reference. This compatibility guidance applies only to existing deployments or locally retained images. Use the current versioned instructions for a fresh installation.

Existing v0.1.1 installations use the same HTTP setup, Compose project and persistent volume as v0.1.2. Keep their deployed files, overrides, administrator and data; follow [upgrade and rollback](#upgrade-and-rollback) to select the new image deliberately. The historical [v0.1.1 release](https://github.com/Mayo-Nas/dockspire/releases/tag/v0.1.1) and its tagged files remain available. v0.1.2 adds no storage migration.

## Locally retained v0.1.0 compatibility

A locally retained v0.1.0 image uses HTTPS 9443 and the old setup-token flow. It does not recognize `--allow-http`. Keep its deployed configuration; its [tagged Compose file](https://github.com/Mayo-Nas/dockspire/blob/v0.1.0/deploy/compose.yaml) and [matching TLS/setup guide](https://github.com/Mayo-Nas/dockspire/blob/v0.1.0/docs/INSTALL.md) are historical references, not instructions for a fresh registry installation. The current HTTP template does not match that image.

Before upgrading, record and retain the old local image ID/digest, and preserve the existing Compose files, overrides, project/data volume, administrator and TLS state. Follow [upgrade and rollback](#upgrade-and-rollback) for a private stopped whole-data backup with original numeric ownership and permissions. Recovery requires that compatible backup and a retained old image; the retired registry tag cannot be pulled again. Source pushes do not publish images or update running v0.1.0 instances.

## Optional Portainer configuration import

Portainer file import is available without a direct source connection: choose **Stacks → Import stack → Portainer → Load exported Compose file** and load or paste standard Compose YAML exported or copied from Portainer. This creates an ordinary new draft with an unused stack name; it does not adopt, deploy or stop the existing workload. Review ports, paths and volumes before any later deployment. Load only files you are authorized to copy; environment files are optional and require a separate deliberate choice.

Use matching checkout files in deploy/. Its Portainer override supplies the HTTP command. If optional HTTPS is also enabled, merge the command lists with the TLS settings above; the last command override replaces earlier lists rather than appending.

Dockspire can preview and save configuration copies from supported Portainer Docker Compose stacks on the same Docker host. Importing does not stop Portainer, adopt workloads, edit its source files, or deploy containers. Copies stay read-only until a separate [takeover review](OPERATIONS.md#portainer-takeover) is explicitly confirmed and completes. No Portainer API token, database access, or full Portainer data-volume mount is needed. Takeover is included since v0.1.1; it is unavailable in the v0.1.0 image.

Download `deploy/compose.portainer.yaml` from the same release/commit as the primary Compose file, saving it beside `compose.yaml` as `compose.portainer.yaml`. Enable it only when authorized to read those stack definitions, which can contain secrets even without associated environment files. In your private local `.env`, set:

```dotenv
# Example only: use the actual authorized host compose subdirectory.
DOCKSPIRE_PORTAINER_COMPOSE_DIR=/srv/portainer/data/compose
# Optional, separate authorization to copy .env and stack.env values:
# DOCKSPIRE_IMPORT_ENVIRONMENT=true
```

Mount only Portainer's `compose` subdirectory, never its entire data volume. For a named Portainer data volume, an administrator must identify its actual host compose directory without copying database files or credentials into Dockspire. The override requires an explicit existing host path, mounts it read-only at `/portainer/compose`, and does not create a missing path. Environment-file copying defaults to false. Files referenced from Compose beyond `.env` and `stack.env`, and values held only in Portainer's database, are not copied.

Apply only the manager service:

```sh
docker compose -f compose.yaml -f compose.portainer.yaml config --quiet
docker compose -f compose.yaml -f compose.portainer.yaml up -d --no-deps app
```

Use the same two `-f` arguments for subsequent HTTP manager operations while this feature is enabled. For optional HTTPS, put the merged TLS/import command and the HTTPS healthcheck from above in `compose.override.yaml`, and explicitly select it last: `docker compose -f compose.yaml -f compose.portainer.yaml -f compose.override.yaml up -d --no-deps app`. Explicit `-f` selection does not automatically load the default override. Preserve all TLS certificate/listener and import arguments; command lists replace rather than append. Preserve the existing project, image, port and data volume. This may restart Dockspire and end browser sessions; it does not restart managed workloads.

After signing in, choose **Stacks → Import stack → Portainer**. Select a discovered stack, review the masked Compose preview, warnings and environment-file inclusion status, then choose **Import definition · leave containers running**. Dockspire rechecks the source fingerprint before saving. Source access requires existing containers whose Compose source label identifies `/data/compose/<numeric-id>/docker-compose.yml` or `.yaml`. Swarm stacks, other source layouts, and stacks with no existing containers are not discovered by this import path. Uploaded ordinary Compose files remain available through the other import choices for creating independent new drafts with unused project names.

Only the supported Compose and explicitly authorized environment files are copied. Portainer database files, separate key files, arbitrary attachments and workload data are excluded. The copied Compose text and environment files can contain workload credentials or keys. Keep the library and exported archives private. Removing the override and restarting `app` with the base file disables further Portainer reads; previously saved copies remain in the library. Pending takeover recovery still needs the same authorized source bundle to verify its fingerprint; keep that read-only source available until the handoff finishes.

## Upgrade and rollback

Use the method that created your installation. Keep its existing volume, container/project identity, port bindings, app arguments, mounts and resource/security settings. Read the target release notes and record the old image ID/digest before changing it. Back up the stopped application's whole data volume privately, including administrator/TLS state, with original numeric ownership and permissions; library exports are not a whole-data backup. Keep workload backups separately. Retain the old image locally.

### Docker run installations

For the quick-install container named `dockspire`, record the running image with `docker inspect --format '{{.Image}}' dockspire`. Pull the target release first, then stop the manager with `docker stop dockspire` and take the private whole-volume backup. After verifying that backup, remove only the stopped manager container with `docker rm dockspire` (without `-v`). Re-run your saved `docker run` command with the target image, the same `dockspire_data:/appdata` mount and every existing customization. The fresh-install command above is suitable only if it still matches your installation. Do not run `docker volume create` against a differently named volume or switch to Compose as an implicit upgrade. Sign in again and check inventory, saved stacks and installation status. Managed workloads do not need restarting.

v0.2.0 retains legacy bundle IDs but writes new presence-aware identities and version-2 library archives. After a new-format write, image-only rollback to an older release is insufficient. Stop Dockspire, preserve the upgraded volume separately, and restore your compatible pre-upgrade whole-volume backup into a fresh isolated volume/path with the retained old image. Preserve administrator/TLS state, original numeric ownership and private permissions, and verify recovery before resuming operations. Never run old and new managers against the same data. Workload data needs a separate restore plan.

### Compose installations

When selecting an image in a private `.env`, first run `unset DOCKSPIRE_IMAGE` in that shell so a stale export cannot override it. Alternatively, export the exact selected version or digest for every upgrade and rollback command. Record the effective reference before pulling or recreating `app`.

Existing v0.1.0 deployments retain HTTPS on 9443 and matching tagged configuration; replacing the deployed template is an explicit listener change. Preserve the existing project/data volume/administrator and back up before upgrading.

Passwordless mode stores an explicit version-2 access marker that older releases reject. Before downgrading, restore a compatible private backup or, with the current server stopped, use its offline reset command to restore a nonempty password that the older release supports (v0.1.0 accepts at most 1024 UTF-8 bytes). Keep the administrator file; never erase it to force setup. Existing version-1 password accounts remain compatible.

For a source-built installation, retain the existing checkout and data volume. Build the reviewed source into a new local image tag from the repository root, return to `deploy/`, and export `DOCKSPIRE_IMAGE` with that tag. Recreate only `app` with `docker compose up -d --no-deps --pull never app`; skip `docker compose pull`. Retain the previous image tag for rollback and back up the stopped application's data before upgrading.

For a published-image installation, select a tested image version or digest in `.env`, read its release notes, record the previous reference/digest, and back up the data volume privately while `app` is stopped. Retain the old image locally. Keep the same Compose file and overrides for every operation; when using `compose.v0.1.0.yaml`, include `-f compose.v0.1.0.yaml` in each command below. Then run:

```sh
docker compose pull app
docker compose up -d --no-deps app
docker compose ps
```

Sign in again and check inventory, saved stacks and installation status. Preserve the Compose project, volume name and mount. Upgrading the manager does not require restarting managed workloads. Current source retains legacy bundle IDs but writes new presence-aware identities and version-2 library archives. After any new-format write, do not point an older image at that upgraded library. Stop the app, retain the upgraded data separately, and restore the private pre-upgrade whole-volume backup into a fresh isolated volume/path with the old image. Preserve administrator/TLS state, original numeric file ownership and private permissions, then verify recovery before resuming operations. Copying the same bytes with different ownership can prevent startup. Image-only rollback is insufficient after a format change; workload data is backed up/restored separately.

Legacy preview and offline archive distribution have been retired from this source tree. Existing `stacklane-preview` installations use different project/data/access settings: retain their deployed Compose files and data mounts. This cleanup does not migrate or rename installations. Updating one needs a reviewed deployment preserving its existing project, data, ports and administrator/TLS state; do not start the current template against an empty volume and assume the old library was migrated. Historical templates remain in Git history.
