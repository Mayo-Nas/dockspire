# Dockspire operations manual

For first installation and certificate verification, start with [INSTALL.md](INSTALL.md). Source builds and fictional demos are described in [the development guide](DEVELOPMENT.md). Examples below use generic paths and fictional stack names.

## Administrator access

v0.1.1 containers start HTTP on 8787 and browser administrator setup without a token. Use a trusted management network: HTTP has no transport encryption and the first reachable client can claim the administrator during the setup window. Optional HTTPS is available. Setup expires after five minutes; restart app if unfinished. The fixed username is admin. Passwords have no length/composition policy; explicit matching blank browser fields persist passwordless access and take every visitor directly to the dashboard. Anyone who can reach that instance can control its Docker host. Setup cannot replace an existing administrator, and existing password accounts remain protected. Locally retained v0.1.0 keeps its tagged HTTPS/token instructions; see INSTALL.md before upgrading.

For password accounts, sign-out revokes the browser session; sessions expire after eight hours and end on restart. Passwordless mode hides sign-out and automatically creates a new browser session without sign-in; per-browser CSRF and exact Host/Origin checks still protect mutations. Password verification is limited per actual connection source, with one expensive verification at a time. Forwarded address headers are ignored. Clients sharing a NAT or reverse proxy share that source allowance; restrict access to the management network. Setup attempts share that allowance. Restarting clears temporary throttling state without changing stored access mode.

Native/legacy operation can initialize an administrator offline using piped private input, with the server stopped:

```sh
stacklane --data-dir ./data --init-admin < /path/to/private/password-file
```

Never put passwords in arguments, environment variables, shell history or chat. Nonempty offline stdin is literal UTF-8: trailing newlines, spaces and control characters are part of the password, with no trimming. Keep a password file's exact intended contents; empty EOF fails safely and cannot select passwordless mode. Offline input is bounded to 1 MiB and must also fit a 1 MiB encoded JSON login request; browser JSON bodies share that generic transport limit, including escaping and both setup fields. Oversize input is rejected, never truncated. Remove temporary plaintext input files after use. Do not run more than one server or maintenance process against a library.

The Compose examples below use the downloaded `dockspire-compose.yaml`. For source-built or older installations, substitute the actual base filename and retain every existing `-f` override argument.

To reset a forgotten password in the container installation:

```sh
docker compose -f dockspire-compose.yaml stop app
docker compose -f dockspire-compose.yaml run --rm -T --no-deps app --data-dir /appdata --reset-admin < /path/to/private/password-file
docker compose -f dockspire-compose.yaml start app
```

Reset requires a valid existing private administrator file. Do not delete that file or initialize a new volume as a reset shortcut. Keep whole-volume backups private: they include the password hash, TLS key and potentially secret stack configuration. Portable library archives exclude these authentication/TLS files.

For a TLS reverse proxy, configure an explicit HTTPS `--external-url`, preserve that Host header and restrict the backend HTTP listener to the proxy. Forwarded headers do not authorize alternate origins. The current container template uses explicit LAN HTTP; a TLS proxy command must omit --allow-http and pin the HTTPS external URL. Direct HTTPS remains optional. See the installation guide for exact settings.

## Container monitoring

Open a container's **Overview & health** for state, uptime, restart/exit information, ports and mounts. Environment values, commands, arbitrary labels and health-check output are omitted. Running state alone is not proof of application health.

**Resources** shows CPU, memory, network and disk I/O, and process/thread counts. Network/rate measurements need two samples; missing/reset counters are shown as unavailable. Only the selected view polls, and hiding/closing it pauses polling. Docker/cgroup support can limit available metrics.

**Logs** shows bounded timestamped stdout/stderr with search, pause/resume, refresh and time filters. Log output can itself contain secrets; Dockspire displays it as text and does not retain it. Shorten the range when a response is truncated. Monitoring does not adopt another manager's resources.

## Adding stacks

Choose **Stacks → Add stack**, enter an unused Compose project name and YAML, and optionally supply environment entries. **Validate Compose** checks the model without creating containers. **Save and review** saves a draft; deployment is a separate confirmed action.

Image-based services, named volumes, absolute Linux bind paths, and `.env`/`stack.env` are supported. Builds, includes, extends and arbitrary attachments are not supported by the editor. Treat command/comment text as sensitive too: automatic masking cannot identify every arbitrary secret.

**Import stack** accepts ordinary Compose/environment files through the draft editor as new drafts with unused project names. It does not automatically adopt running workloads. Proprietary backup formats are unsupported. Choose **Portainer** to preview and save a read-only copy after completing the [optional source-access setup](INSTALL.md#optional-portainer-configuration-import). The preview explains whether environment files are included and masks recognized environment values. Importing alone leaves Portainer and its containers unchanged.

### Portainer takeover

v0.1.1 offers **Review takeover** on an imported Portainer copy. Nothing changes until you review the exact targets, type the project name and explicitly acknowledge the handoff. Back up mounted workload data and disable Portainer GitOps, webhooks and all other writers first. Portainer retains its visibility and manual controls; Dockspire cannot revoke its access or enforce a cross-manager lock. Do not delete the Portainer stack to transfer it: deletion may remove the same running workload. Future foreign-manager replacements cause Dockspire operations to be blocked.

Takeover force-recreates the existing services with downtime and loss of container writable-layer changes. It preserves the Compose project, existing container names, immutable image IDs, verified named volumes, networks and absolute host binds. Named resources are recorded as external to avoid recreating them; missing bind sources must not be auto-created. Images stay pinned locally. Ordinary redeploy reuses them; edit an image reference explicitly before requesting a registry upgrade.

The initial supported subset is deliberately narrow: one existing container per service, matching source/configuration hashes, explicit environment values, fixed published ports and ordinary named-volume/absolute-bind networking. Replicas, one-offs, profiles, dependency graphs, builds, inherited environment entries, file attachments, anonymous/image-declared volumes, custom network aliases/addresses, advanced mount options, unused resource declarations, models/providers and the manager's own project are blocked. Missing Portainer-only values or a Compose-version hash mismatch must be resolved without bypassing the check. Unsupported stacks stay unchanged and view-only. Interpolation is accepted only inside service environment values; paths, commands, labels and other fields must be explicit. Literal dollar signs use Compose's $$ escape.

The reviewed source and container/resource identities are checked again before recreation. A private immutable revision and pending intent are saved first. Only verified Dockspire provenance and retained resource identities unlock editing, redeployment, revision restore and lifecycle controls. A failed/interrupted handoff stays locked behind **Review takeover recovery**, accepting only the captured originals or verified Dockspire replacements. Recovery reviews include missing services that will be recreated. Keep the same source mount and authorized environment inputs available for this check. No automatic rollback, orphan removal, stack deletion or volume deletion is performed. Restoring application data from backups remains an operator task.

After every stack you want to retain has completed takeover and is managed by Dockspire, you can remove the Portainer container. Completed stacks use Dockspire's saved revisions for future edits and redeployments; pending recovery still requires the original Portainer source. Keep all workload volumes, networks and bind-mounted data, including any paths inside Portainer's data directory. Do not delete Portainer stacks as a removal method. If you later remove the optional Portainer source mount from Dockspire, do so only after all handoffs are complete; an unchanged bind mount still requires its host path to exist.

## Starting, stopping and restarting

Lifecycle actions operate on saved, owned deployments after a review/confirmation. Discovered external projects remain view-only. Reviews show the intended scope and become invalid when configuration/resource identity changes. Watch the operation result and observed inventory; an accepted job is not proof that every container changed successfully.

## Updating stacks and services

Open a saved stack's **Review stack update**, or a container's **Review service update**. Review the images and target containers and confirm the stack name. Pinned tags remain pinned; changing versions requires editing the selected tag. Service-only updates leave unrelated services outside the selected scope.

Observed before/after image IDs are recorded when available. They are evidence of the operation, not an image archive or automatic rollback mechanism. Keep the previous configuration/images and workload backups required for recovery.

## Configuration history and recovery

Saved revisions preserve exact Compose/environment bytes. Compare saved and applied configurations, inspect historical revisions and restore an older revision as a draft before reviewing a deployment. Secret-bearing values are masked in comparison/history views; exports deliberately include their selected configuration.

An interrupted or failed deployment may have changed some resources. Inspect the recorded attempt, current container identities and persistent workload data before retrying. Unfinished operations are not replayed automatically. Do not assume reverting source or selecting an old image restores a database or volume.

Operation history is retained with bounded recent records plus protected deployment/recovery references. Configuration cleanup is separately reviewed and protects references required by active/current/recovery state. Damaged entries become unavailable with warnings rather than hiding unrelated stacks.

One heavy Compose operation runs at a time. Resource identity/configuration checks are repeated before mutation. Keep one application instance per data volume; there is no cross-process maintenance lock.

## Backup, export and restore

The browser can download a complete saved-library archive or selected Compose configuration after explicit acknowledgement that it contains secrets. Workload data, images, Docker credentials, external-manager files, administrator credentials and TLS keys are excluded. Keep archives private and back up workload data separately.

Offline commands run with the server stopped, as the data owner, against unused output/destination paths:

```sh
mkdir -p ../backups
stacklane --data-dir ./data --backup ../backups/library.zip
stacklane --data-dir ./data --export-stack example-stack --backup ../backups/example-stack.zip
stacklane --data-dir ./restored-data --restore ../backups/library.zip
```

A selected-stack export is not a complete library restore archive. Library archives preserve saved configuration, revisions and retained jobs with a versioned manifest and checksums; they are not encrypted or independently signed. Restore rejects unsafe paths/links, duplicate entries, invalid sizes/versions/checksums and inconsistent references. It never merges into an existing library. The content limit is 128 MiB/20,000 files, with 512 KiB per library file.

For a container backup, mount a separate writable backup directory:

```sh
mkdir -p backups
docker compose -f dockspire-compose.yaml stop app
docker compose -f dockspire-compose.yaml run --rm --no-deps -v "$PWD/backups:/backups" app --data-dir /appdata --backup /backups/library.zip
docker compose -f dockspire-compose.yaml start app
```

A restored library needs its own administrator/TLS setup. Inspect configuration and recovery state before reviewing any deployment. Restoring records does not authorize adoption of external containers or restore workload data. Moving a library to a new path may affect ownership checks; plan migration explicitly.

## Host packages and compatibility

Docker Engine and the Compose plugin used to launch the application are host-managed. Dockspire does not install or upgrade host packages. Its runtime image bundles a separately pinned Compose executable for stack operations. The optional host helper is not connected by the default installation; its runtime support and maintenance scripts remain available for existing installations.

The unchanged service template is now `deploy/host-helper/stacklane-host.service`. Existing helper maintainers can stage that file, a reviewed Linux `stacklane` executable built from `cmd/dockspire`, and the needed `scripts/*host*.sh` maintenance scripts together in a private directory. The scripts expect these files beside them; review each script before explicitly running it with root privileges on the intended Linux host. Keep existing helper service/socket paths and container overrides. The retired preview Compose override used old HTTP/host-Compose settings and must not be applied to the current container template.
For `restrict-host-helper.sh`, stage a `SHA256SUMS` file from the verified build that includes the `stacklane` executable; the replacement script verifies it before installation.

Existing formats and native executable names retain `stacklane`, `STACKLANE_*`, the `stacklane-library` archive kind and related compatibility identifiers. The product/browser name is Dockspire. Preserve an installation's project name and data mount when upgrading; see [INSTALL.md](INSTALL.md) for backup and rollback.

## Configuration identity and archive compatibility

Current source writes presence-aware, lossless configuration identities: an absent environment file differs from a present empty file, including raw non-UTF-8 bytes. Existing initial bundle IDs, revision directory names, applied/recovery references and operation history remain unchanged and readable. A newly saved revision or newly imported/created bundle uses the new identity scheme; old bundles are not renamed or rewritten automatically. Editor/stale-review tokens use exact current content, separately from historical persisted IDs.

Full library archives now use manifest version 2; current source restores both version 1 and version 2. Older binaries reject version-2 archives and may not understand new identities in a live library. Before upgrading, stop Dockspire and take a private whole-data-directory/volume backup including administrator/TLS state. After writing new-format data, rollback requires restoring that compatible pre-upgrade backup with the retained old image into a fresh isolated data volume/path; selecting only the old image is insufficient. Preserve the original numeric file ownership and private permissions when restoring administrator/TLS state; changed ownership can prevent startup even when the bytes match. Preserve the upgraded data separately until recovery is verified. Workload data needs its own backup.

## Upgrade and rollback

These commands cover the standard named-volume installation in the install guide. For customized installations, use your saved command/files and preserve every mount, port, argument, security setting and Compose override. Keep workload backups separately.

### Whole-data backup

Choose your installation's container. For Compose:

```sh
container=$(docker compose -f dockspire-compose.yaml ps -q app)
```

For Docker run:

```sh
container=dockspire
```

Run the following in the same terminal. It stops Dockspire and saves the complete data volume, including administrator and TLS state. Library exports are not a substitute.

```sh
set -eu
set -C
umask 077
test -n "$container"
backup_dir=$(mktemp -d "$PWD/dockspire-backup.XXXXXXXX")
old_image=$(docker inspect --format '{{.Image}}' "$container")
rollback_image="dockspire:rollback-$(basename "$backup_dir")"
if docker image inspect "$rollback_image" >/dev/null 2>&1; then
  printf '%s\n' 'Rollback image tag already exists; stop and choose a new tag.' >&2
  exit 1
fi
docker image tag "$old_image" "$rollback_image"
printf '%s\n' "$old_image" > "$backup_dir/image-id.txt"
printf '%s\n' "$rollback_image" > "$backup_dir/rollback-image.txt"
docker stop "$container"
docker cp "$container:/appdata/." - > "$backup_dir/appdata.tar"
test -s "$backup_dir/appdata.tar"
tar -tf "$backup_dir/appdata.tar" >/dev/null
printf 'Backup directory: %s\n' "$backup_dir"
```

Stop if any command fails. Keep the printed backup path, original archive and retained image private and available; do not prune the image before recovery is verified. Save your Compose/override files and optional `.env`, or your complete Docker run command, alongside the backup. Preserve the archive unchanged: extracting and re-packing it can change numeric ownership and private permissions.

### Upgrade a Compose installation

The commands below assume the base-only standard installation. Include every existing `-f` override in each command. After a previous rollback, keep its recovered-volume override last and update the image there.

After the backup, save the standard configuration:

```sh
cp dockspire-compose.yaml "$backup_dir/dockspire-compose.yaml"
```

Replace the complete `image:` value in your saved Compose file with the target release reference. For example, the current published release is:

```yaml
image: ghcr.io/mayo-nas/dockspire:v0.2.0
```

Then recreate only Dockspire:

```sh
docker compose -f dockspire-compose.yaml pull app
docker compose -f dockspire-compose.yaml up -d --no-deps app
docker ps
```

### Upgrade a Docker run installation

After the backup, select a released target image and replace only the stopped manager container. The example uses the current public release; change it to the release you intend to install.

The current data-volume name is read from the stopped container, including a volume created by an earlier rollback.

```sh
target_image=ghcr.io/mayo-nas/dockspire:v0.2.0
data_volume=$(docker inspect --format '{{range .Mounts}}{{if and (eq .Destination "/appdata") (eq .Type "volume")}}{{.Name}}{{end}}{{end}}' "$container")
test -n "$data_volume"
docker pull "$target_image"
docker rm "$container"
docker run -d \
  --name dockspire \
  --restart unless-stopped \
  -p 8787:8787 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$data_volume:/appdata" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --memory 256m --cpus 0.5 --pids-limit 64 \
  "$target_image"
docker ps
```

Sign in again and check inventory, saved stacks and installation status. Managed workloads do not need restarting.

### Roll back: restore the backup first

Image-only rollback can fail after new-format writes. Keep the upgraded volume intact and restore the compatible pre-upgrade backup into a fresh volume. Never run two managers against the same data.

Set `backup_dir` to the actual directory printed during backup. Then select the current manager, including a stopped container if the upgrade failed. For Compose:

```sh
backup_dir=/path/to/your/dockspire-backup.XXXXXXXX
container=$(docker compose -f dockspire-compose.yaml ps -aq app)
```

For Docker run:

```sh
backup_dir=/path/to/your/dockspire-backup.XXXXXXXX
container=$(docker ps -aq --filter 'name=^/dockspire$')
```

Restore the original Docker tar stream into a new volume. The helper container is created but never started; no shell or tar executable is required inside the image.

```sh
set -eu
set -C
umask 077
old_image=$(cat "$backup_dir/rollback-image.txt")
test "$(docker image inspect --format '{{.Id}}' "$old_image")" = "$(cat "$backup_dir/image-id.txt")"
test -s "$backup_dir/appdata.tar"
tar -tf "$backup_dir/appdata.tar" >/dev/null
if [ -n "$container" ]; then docker stop "$container"; fi
restore_volume="dockspire_restore_$(date -u +%Y%m%dT%H%M%SZ)_$$"
restore_container="$restore_volume-loader"
if docker volume inspect "$restore_volume" >/dev/null 2>&1; then
  printf '%s\n' 'Restore volume already exists; choose an unused name.' >&2
  exit 1
fi
docker volume create "$restore_volume"
docker create --pull never --name "$restore_container" -v "$restore_volume:/appdata" "$old_image"
docker cp - "$restore_container:/appdata" < "$backup_dir/appdata.tar"
docker rm "$restore_container"
printf 'Restored volume: %s\n' "$restore_volume"
```

Do not extract/re-pack the archive or add `docker cp -a`: the original stream preserves the numeric ownership and permissions needed by the administrator and TLS files. Continue with one method below, in the same terminal.

#### Start the restored Compose installation

Create a persistent rollback override in the private backup directory:

```sh
cat > "$backup_dir/rollback-compose.yaml" <<EOF
services:
  app:
    image: $old_image
volumes:
  dockspire_data:
    name: $restore_volume
    external: true
EOF
docker compose -f dockspire-compose.yaml -f "$backup_dir/rollback-compose.yaml" up -d --no-deps --pull never app
docker ps
```

For customized installations, include every original override before the rollback file. Keep the rollback file last in every later command, including upgrades; its image and recovered-volume mapping override the base file. When upgrading again, change its image while retaining the recovered volume. Keep the original configuration if it differs from the current template.

#### Start the restored Docker run installation

Replace only the stopped manager container; the upgraded data volume remains untouched:

```sh
if [ -n "$container" ]; then docker rm "$container"; fi
docker run -d --pull never \
  --name dockspire \
  --restart unless-stopped \
  -p 8787:8787 \
  -v /var/run/docker.sock:/var/run/docker.sock:ro \
  -v "$restore_volume:/appdata" \
  --read-only \
  --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  --cap-drop ALL \
  --security-opt no-new-privileges:true \
  --memory 256m --cpus 0.5 --pids-limit 64 \
  "$old_image"
docker ps
```

Verify the original administrator, TLS state, inventory and saved stacks before resuming changes. Keep the upgraded data and backups until recovery is verified; workload data requires its own restore plan.

### Source-built and older installations

Source builds must keep their explicit local-image selection and existing files; build a new local tag, back up first, and use `docker compose up -d --no-deps --pull never app` from the existing `deploy/` directory. Retain the previous local image. For older base filenames, substitute the actual filename and retain every override.

Existing v0.1.0 keeps HTTPS on 9443 and its matching tagged configuration. Passwordless/version-2 data may be rejected by older releases: restore a compatible whole-data backup before downgrading. Never erase the administrator file to force setup. Legacy `stacklane-preview` installations use different project/data/access settings and require a deliberate migration preserving their existing data, ports and administrator/TLS state.

## Compose settings and diagnostics

The download follows maintained `main`; its image default is versioned. Existing installations should retain their saved file and follow [upgrade and rollback](#upgrade-and-rollback), rather than downloading a replacement as an upgrade. Shell exports or an existing `.env` can override the optional `DOCKSPIRE_IMAGE`, `DOCKSPIRE_HTTP_PORT`, `DOCKSPIRE_BIND_ADDRESS` and `DOCKSPIRE_EXTERNAL_URL` settings.

To pin an immutable image, replace the complete `image:` value in your saved file with the `ghcr.io/mayo-nas/dockspire@sha256:...` reference from the release notes. For a different HTTP port, set `DOCKSPIRE_HTTP_PORT`; with `8788`, open `http://SERVER-IP:8788`. DNS access requires `DOCKSPIRE_EXTERNAL_URL` set to the exact HTTP origin, including any custom port. IP addresses and localhost work by default; forwarded headers are ignored. `DOCKSPIRE_BIND_ADDRESS=127.0.0.1` restricts access to the server or a verified SSH tunnel.

For troubleshooting, validate the saved configuration, view recent logs, or run the healthcheck:

```sh
docker compose -f dockspire-compose.yaml config --quiet
docker compose -f dockspire-compose.yaml logs --tail 50 app
docker compose -f dockspire-compose.yaml exec -T app /stacklane --healthcheck
```

The automatic healthcheck may take a short time to report healthy. Never use `docker compose -f dockspire-compose.yaml down -v` to upgrade or reset access.

## Docker run settings

To pin an immutable image, replace the versioned image above with the complete `ghcr.io/mayo-nas/dockspire@sha256:...` reference from the release notes. To publish only on loopback, replace `-p 8787:8787` with `-p 127.0.0.1:8787:8787`. For DNS access, append `--allow-http --setup --data-dir /appdata --external-url http://docker.example.test:8787` after the image name, using your exact origin. App arguments go after the image and replace its default command.

Optional startup diagnostics are available with `docker logs --tail 50 dockspire` and `docker exec dockspire /stacklane --healthcheck`.

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

The current template defaults to `ghcr.io/mayo-nas/dockspire:v0.2.0` when `DOCKSPIRE_IMAGE` is unset or empty; older saved templates may still require it. For a source build, keep the explicit local-image selection above so Compose uses your build. A shell export overrides `.env`. The HTTP template accepts IP addresses and localhost by default. DNS access requires an exact `http://...` external URL; forwarded headers are ignored. With `DOCKSPIRE_HTTP_PORT=8788`, use `http://VM-IP:8788`.

## Optional HTTPS

This section uses the downloaded `dockspire-compose.yaml`. For source-built or older installations using `compose.yaml`, substitute `-f compose.yaml` for the base-file argument below and preserve the existing configuration. For an existing `docker run` installation, retain its current settings and use a deliberate migration; do not start another manager against the same volume.

Native `--https` still defaults to 9443; the container can serve TLS on its existing 8787 mapping. Set `DOCKSPIRE_TLS_HOSTS` to your DNS/IP before first TLS startup and save `compose.override.yaml` beside `dockspire-compose.yaml`:

```yaml
services:
  app:
    command: ["--https", "--listen", ":8787", "--setup", "--data-dir", "/appdata", "--tls-hosts", "${DOCKSPIRE_TLS_HOSTS:-}", "--external-url", "${DOCKSPIRE_EXTERNAL_URL:-}"]
    healthcheck:
      test: ["CMD", "/stacklane", "--healthcheck", "--https", "--listen", "127.0.0.1:8787"]
```

Run `docker compose -f dockspire-compose.yaml -f compose.override.yaml up -d --no-deps app` and use `https://VM-IP:8787`. Any external URL must use `https://` in this mode. For trusted certificates, mount a private certificate directory read-only and add `--tls-cert /certs/cert.pem --tls-key /certs/key.pem` to the command; keep the listener/healthcheck settings above. Include matching certificate SANs and renew/restart externally. Explicit `-f` selection does not load overrides automatically; retain both file arguments for subsequent HTTPS operations.

Generated self-signed TLS identities persist; existing identities are not silently replaced to add SANs. Authenticate a self-signed certificate before entering credentials. From a trusted VM console/SSH session, `docker compose -f dockspire-compose.yaml -f compose.override.yaml exec -T app /stacklane --tls-fingerprint --data-dir /appdata` prints its public SHA-256 fingerprint for comparison with the browser certificate viewer. This is an optional HTTPS trust diagnostic, not a setup token. Custom certificates need `--tls-cert` pointing to the served public certificate for this check. Saved TLS keys remain private even when HTTP is used.

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

Download [deploy/compose.portainer.yaml](../deploy/compose.portainer.yaml) from the same source revision as the primary Compose file, saving it beside `dockspire-compose.yaml` as `compose.portainer.yaml`. The commands below use the downloaded base file; for source-built or older installations, substitute the actual base filename and retain existing overrides. Enable it only when authorized to read those stack definitions, which can contain secrets even without associated environment files. In your private local `.env`, set:

```dotenv
# Example only: use the actual authorized host compose subdirectory.
DOCKSPIRE_PORTAINER_COMPOSE_DIR=/srv/portainer/data/compose
# Optional, separate authorization to copy .env and stack.env values:
# DOCKSPIRE_IMPORT_ENVIRONMENT=true
```

Mount only Portainer's `compose` subdirectory, never its entire data volume. For a named Portainer data volume, an administrator must identify its actual host compose directory without copying database files or credentials into Dockspire. The override requires an explicit existing host path, mounts it read-only at `/portainer/compose`, and does not create a missing path. Environment-file copying defaults to false. Files referenced from Compose beyond `.env` and `stack.env`, and values held only in Portainer's database, are not copied.

Apply only the manager service:

```sh
docker compose -f dockspire-compose.yaml -f compose.portainer.yaml config --quiet
docker compose -f dockspire-compose.yaml -f compose.portainer.yaml up -d --no-deps app
```

Use the same two `-f` arguments for subsequent HTTP manager operations while this feature is enabled. For optional HTTPS, put the merged TLS/import command and the HTTPS healthcheck from above in `compose.override.yaml`, and explicitly select it last: `docker compose -f dockspire-compose.yaml -f compose.portainer.yaml -f compose.override.yaml up -d --no-deps app`. Explicit `-f` selection does not automatically load the default override. Preserve all TLS certificate/listener and import arguments; command lists replace rather than append. Preserve the existing project, image, port and data volume. This may restart Dockspire and end browser sessions; it does not restart managed workloads.

After signing in, choose **Stacks → Import stack → Portainer**. Select a discovered stack, review the masked Compose preview, warnings and environment-file inclusion status, then choose **Import definition · leave containers running**. Dockspire rechecks the source fingerprint before saving. Source access requires existing containers whose Compose source label identifies `/data/compose/<numeric-id>/docker-compose.yml` or `.yaml`. Swarm stacks, other source layouts, and stacks with no existing containers are not discovered by this import path. Uploaded ordinary Compose files remain available through the other import choices for creating independent new drafts with unused project names.

Only the supported Compose and explicitly authorized environment files are copied. Portainer database files, separate key files, arbitrary attachments and workload data are excluded. The copied Compose text and environment files can contain workload credentials or keys. Keep the library and exported archives private. Removing the override and restarting `app` with the base file disables further Portainer reads; previously saved copies remain in the library. Pending takeover recovery still needs the same authorized source bundle to verify its fingerprint; keep that read-only source available until the handoff finishes.
