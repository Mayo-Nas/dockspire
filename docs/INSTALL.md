# Install Dockspire on a Linux VM

Current source defaults to HTTP on 8787 with tokenless first-administrator setup. It needs Docker Engine and the modern Compose plugin, not host Go/Node. Only Linux amd64 is supported. These HTTP changes are not in published v0.1.0: use a source-built image with matching checkout configuration. Existing v0.1.0 installations keep their HTTPS 9443 configuration and data unchanged.

## Build and start the current source

Use a fresh host without an existing Dockspire installation or `dockspire_data` volume. The template has fixed project `dockspire` and volume `dockspire_data`; a second checkout does not isolate an existing installation. HTTP exposes passwords/sessions without encryption; restrict access to your trusted management network. Any reachable client can claim first administrator while setup is open.

```sh
gh repo clone Mayo-Nas/dockspire
cd dockspire
docker build -t dockspire:local .
cd deploy
export DOCKSPIRE_IMAGE=dockspire:local
docker compose up -d --pull never
```

Private source requires an authenticated `gh` account with repository access. Already cloned? Start at `docker build` from the repository root. Docker downloads its build toolchain/dependencies. Keep subsequent Compose commands in `deploy/`; after reconnecting, return there and export `DOCKSPIRE_IMAGE=dockspire:local` again. Default Compose discovery loads `compose.override.yaml` beside `compose.yaml`.

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

`DOCKSPIRE_IMAGE` is required; a shell export overrides `.env`. An unset/empty value produces the Compose image-selection error before startup. Downloading Compose alone does not provide an image. The HTTP template accepts IP addresses and localhost by default. DNS access requires an exact `http://...` external URL; forwarded headers are ignored. With `DOCKSPIRE_HTTP_PORT=8788`, use `http://VM-IP:8788`. Portainer's 9443 can remain running.

## Create the administrator

Open `http://VM-IP:8787`. Username is fixed to `admin`; enter and confirm any password, with no password length or composition policy. Explicitly leaving both fields blank selects passwordless access, opens the dashboard immediately and persists across visits/restarts without a sign-in page. Anyone who can reach a passwordless instance can control its Docker host; restrict network access accordingly. Existing password accounts are not converted. No setup token, certificate or fingerprint is requested. Setup is open for five minutes after startup; `docker compose restart app` opens a new window if it expires. All attempts are rate-limited per transport peer (NAT/proxy clients share an allowance); wait a minute after throttling or restart. Setup cannot replace an existing administrator, and missing/invalid stored access modes never imply passwordless access.

Authentication JSON requests have a generic 1 MiB body limit, including field names, JSON escaping and both setup fields. This transport ceiling applies even though passwords have no separate minimum, maximum or character policy. Use matching strings; omitted or null fields cannot enable passwordless mode.

The password hash and library persist in `dockspire_data` at `/appdata`; sessions end on restart. Back up the stopped app's data privately and workload data separately. Library exports exclude administrator credentials and TLS keys. Never run `docker compose down -v` unless intending to erase this installation. Offline password recovery remains in [operations](OPERATIONS.md#administrator-access); do not delete `admin.json` or create an empty volume to reset a password.

## Optional HTTPS

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

## Published v0.1.0 compatibility

The existing private image `ghcr.io/mayo-nas/dockspire:v0.1.0` uses HTTPS 9443 and the old setup-token flow. It does not recognize `--allow-http`. Use its [tagged Compose file](https://github.com/Mayo-Nas/dockspire/blob/v0.1.0/deploy/compose.yaml) and [matching TLS/setup guide](https://github.com/Mayo-Nas/dockspire/blob/v0.1.0/docs/INSTALL.md), never the current HTTP template. Manually set `DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.1.0` in `.env` beside that downloaded file.

On a fresh installation directory, download the matching template with authenticated GitHub CLI (choose a new filename; do not replace an existing deployment):

```sh
(set -C; gh api 'repos/Mayo-Nas/dockspire/contents/deploy/compose.yaml?ref=v0.1.0' --header 'Accept: application/vnd.github.raw+json' > compose.v0.1.0.yaml)
```

Use `docker compose -f compose.v0.1.0.yaml` for that installation's commands, with its `.env` in the same directory. Use a fresh shell without the source-image export, so `.env` selects the intended published image.

Private registry downloads require an account granted package access and a token with `read:packages`. Run `docker login ghcr.io --username YOUR_GITHUB_USER` and enter the token at its password prompt, then `docker compose -f compose.v0.1.0.yaml pull` and `docker compose -f compose.v0.1.0.yaml up -d`. Keep tokens out of commands/Compose/chat; [GitHub registry authentication](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry) describes access requirements. Normal source pushes do not publish images. This change does not update any running v0.1.0 instance.

## Optional Portainer configuration import

Portainer file import is available without a direct source connection: choose **Stacks → Import stack → Portainer → Load exported Compose file** and load or paste standard Compose YAML exported or copied from Portainer. This creates an ordinary new draft with an unused stack name; it does not adopt, deploy or stop the existing workload. Review ports, paths and volumes before any later deployment. Load only files you are authorized to copy; environment files are optional and require a separate deliberate choice.

Use matching checkout files in deploy/. Its Portainer override supplies the HTTP command. If optional HTTPS is also enabled, merge the command lists with the TLS settings above; the last command override replaces earlier lists rather than appending.

Dockspire can preview and save configuration copies from supported Portainer Docker Compose stacks on the same Docker host. Importing does not stop Portainer, adopt workloads, edit its source files, or deploy containers. Copies stay read-only until a separate [takeover review](OPERATIONS.md#portainer-takeover) is explicitly confirmed and completes. No Portainer API token, database access, or full Portainer data-volume mount is needed. Takeover is current-source functionality, not part of the published v0.1.0 image.

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

Existing v0.1.0 deployments retain HTTPS on 9443 and matching tagged configuration; replacing the deployed template is an explicit listener change. Preserve the existing project/data volume/administrator and back up before upgrading.

Passwordless mode stores an explicit version-2 access marker that older releases reject. Before downgrading, restore a compatible private backup or, with the current server stopped, use its offline reset command to restore a nonempty password that the older release supports (v0.1.0 accepts at most 1024 UTF-8 bytes). Keep the administrator file; never erase it to force setup. Existing version-1 password accounts remain compatible.

For a source-built installation, retain the existing checkout and data volume. Build the reviewed source into a new local image tag from the repository root, return to `deploy/`, and export `DOCKSPIRE_IMAGE` with that tag. Recreate only `app` with `docker compose up -d --no-deps --pull never app`; skip `docker compose pull`. Retain the previous image tag for rollback and back up the stopped application's data before upgrading.

For a published-image installation, select a tested image version or digest in `.env`, read its release notes, record the previous reference/digest, and back up the data volume privately while `app` is stopped. Retain the old image locally. Keep the same Compose file and overrides for every operation; when using `compose.v0.1.0.yaml`, include `-f compose.v0.1.0.yaml` in each command below. Then run:

```sh
docker compose pull app
docker compose up -d --no-deps app
docker compose ps
```

Sign in again and check inventory, saved stacks and installation status. Preserve the Compose project, volume name and mount. Upgrading the manager does not require restarting managed workloads. Rollback selects the retained previous image and runs `docker compose up -d --no-deps --pull never app`; follow the release's data-restore instructions if it changes the storage format. This change does not migrate the library format.

Legacy preview and offline archive distribution have been retired from this source tree. Existing `stacklane-preview` installations use different project/data/access settings: retain their deployed Compose files and data mounts. This cleanup does not migrate or rename installations. Updating one needs a reviewed deployment preserving its existing project, data, ports and administrator/TLS state; do not start the current template against an empty volume and assume the old library was migrated. Historical templates remain in Git history.
