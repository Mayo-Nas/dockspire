# Install Dockspire on a Linux VM

Dockspire runs as one container with a persistent Docker volume, a browser-based first administrator setup, and direct HTTPS. Docker Engine and the modern Compose plugin must already be installed; use [Docker's distribution-specific instructions](https://docs.docker.com/engine/install/). Go, Node, a source checkout, a reverse proxy and host Compose binary mounts are unnecessary. The image bundles its own versioned Compose executable; host package updates remain your responsibility.

**Publication is pending.** No installable release has been published yet. The version below is an example for a future release. Only Linux amd64 is currently supported for publication.

## Start the container

Download `deploy/compose.yaml` from the chosen release/commit into a dedicated directory such as `/opt/dockspire`, naming the local file `compose.yaml`. While the repository is private, use an authenticated GitHub download or copy the file from your workstation. A versioned release should link to its matching Compose file.

For a private repository, the authenticated GitHub CLI can download it from a published tag (example version):

```sh
sudo install -d -m 0750 -o "$(id -un)" -g "$(id -gn)" /opt/dockspire
cd /opt/dockspire
gh api -H 'Accept: application/vnd.github.raw+json' \
  'repos/Mayo-Nas/dockspire/contents/deploy/compose.yaml?ref=v0.1.0' > compose.yaml
```

The tag and image must exist before these commands work. You may instead download on your workstation and copy the file to avoid installing/authenticating `gh` on the VM.

Choose a published version (or exact image digest) in a local `.env` beside the file:

```dotenv
DOCKSPIRE_IMAGE=ghcr.io/mayo-nas/dockspire:v0.1.0
# Optional when Portainer already uses 9443:
# DOCKSPIRE_HTTPS_PORT=9444
# Optional: pin the exact browser origin. Include a non-default port.
# DOCKSPIRE_EXTERNAL_URL=https://docker.example.test:9443
# Optional DNS names/IPs to include when first generating a certificate:
# DOCKSPIRE_TLS_HOSTS=docker.example.test,203.0.113.20
```

The Compose file requires an explicit image selection. It does not silently follow `latest`. For a private GHCR image, authenticate Docker using an account granted package access and a read-packages token through `docker login ghcr.io --username YOUR_GITHUB_USER --password-stdin`. Keep tokens out of arguments and Compose files; see [GitHub's registry authentication instructions](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry). Making the repository/image public requires a separate decision; private source does not provide anonymous image downloads.

Then run:

```sh
docker compose pull
docker compose up -d
docker compose logs app
```

Dockspire creates and persists a self-signed certificate on first start. **Authenticate its identity before entering any setup token or password.** A browser warning alone does not establish that you reached the intended VM. Use a trusted certificate matching your DNS/IP, or verify the fingerprint below. No plaintext HTTP port is published. Keep access on your trusted management network; the Docker socket permits host administration even when mounted `:ro`.

From the VM console or an SSH connection whose host identity you have already verified, run:

```sh
docker compose exec -T app /stacklane --tls-fingerprint --data-dir /appdata
```

This reads only the public certificate and prints its SHA-256 fingerprint. Startup logs also print it. Open `https://VM-IP:9443` (or your chosen port), inspect the certificate through your browser's certificate viewer **before accepting an exception**, and compare all bytes of its SHA-256 fingerprint with the trusted console output. Stop if it differs. Do not use a fingerprint shown by the web page itself as your trusted reference. Repeat verification after any intentional certificate replacement.

For a custom certificate, add `--tls-cert /certs/cert.pem` to that command so it reads the certificate actually served by your override.

The default certificate identifies localhost/loopback. Set `DOCKSPIRE_TLS_HOSTS` before first startup to include your DNS/IP and avoid a name mismatch; these SANs do not make a self-signed certificate trusted. Existing identities are never silently replaced: adding an uncovered host later fails startup and requires supplying a matching custom certificate or an explicit, verified certificate replacement. Trusted custom certificates avoid the manual trust exception.

## Create the administrator in your browser

1. After certificate verification, copy the **one-time administrator setup token** from `docker compose logs app` into the setup page. Treat the token as private while setup is open.
2. Choose and confirm a password of at least 15 characters for the fixed `admin` account.
3. Create the administrator, then sign in on the next page.

Setup is available for five minutes after server startup. If it times out, run `docker compose restart app`, retrieve the new token from the latest log entry, and reopen the page. A restart rotates the token. Setup never replaces an existing administrator; a damaged or unsafe administrator file causes startup to fail. Keep the correct data volume mounted: starting with an empty volume creates a new installation.

The password is hashed before storage. Administrator credentials, the saved stack library, and the TLS certificate/key persist in the named **`dockspire_data`** volume mounted at `/appdata`. Sessions end on restart; the password and certificate remain. Library export ZIPs exclude the administrator and TLS keys, so they are not a full installation backup. Back up the volume privately with the app stopped and back up workload data separately. Never run `docker compose down -v` unless you intend to erase this installation's persistent data.

Anonymous attempts are limited per transport peer, with bounded expiring state and one expensive password check at a time. Caller-supplied forwarded addresses are ignored. Clients sharing a NAT/reverse proxy share an allowance; restrict access to trusted management clients. A valid setup token can complete first setup even if anonymous requests from that address exhausted the allowance. Restarting clears temporary limits without resetting an existing account. Network-level controls are still needed against sustained/distributed traffic.

If setup is already complete, startup presents normal sign-in and prints no new setup token. Password recovery stays an explicit offline operation: stop the app, run the same image with `--data-dir /appdata --reset-admin` against the same volume and piped private password input, then start it. See [administrator recovery](OPERATIONS.md#administrator-access) for the native command's input rules. Do not initialize a second account or delete `admin.json` as a recovery shortcut.

## Optional access settings

The default publishes HTTPS to the VM's interfaces at port 9443. `DOCKSPIRE_HTTPS_PORT=9444` avoids a conflict with an existing Portainer installation. Do not stop Portainer to free its port.

To restrict access to an SSH tunnel, set `DOCKSPIRE_BIND_ADDRESS=127.0.0.1` and forward your chosen host port:

```sh
ssh -N -L 9444:127.0.0.1:9443 USER@VM
```

Open `https://localhost:9444`. Leave `DOCKSPIRE_EXTERNAL_URL` unset for access through different local tunnel ports; if set, browser Host/Origin must match it exactly. Forwarded headers do not change the allowed origin.

To supply a trusted certificate, create a separate Compose override:

```yaml
services:
  app:
    command: ["--https", "--setup", "--data-dir", "/appdata", "--tls-cert", "/certs/cert.pem", "--tls-key", "/certs/key.pem", "--external-url", "https://docker.example.test:9443"]
    volumes:
      - ./certs:/certs:ro
```

Save it as `compose.override.yaml` beside `compose.yaml`; Compose loads both automatically. Supply a valid certificate/full chain and matching private key with SANs covering the hostname/IP you use. Restrict private key access. Renew custom certificates externally and restart only `app` to load them. The generated certificate is valid for two years and is not automatically renewed; replace it with a managed certificate before expiry. An incomplete or damaged saved certificate causes startup to fail instead of silently changing identity.

An existing TLS reverse proxy remains supported through the legacy HTTP listener plus an explicit `--external-url https://...`. Bind that backend to loopback or a private proxy network; the normal container template uses direct TLS and needs no proxy.

## Optional Portainer configuration import

Dockspire can preview and save configuration copies from supported Portainer Docker Compose stacks on the same Docker host. This optional read-only source import does not stop Portainer, adopt workloads, edit its source files, or deploy containers. Imported copies remain read-only in Dockspire. No Portainer API token, database access, or full Portainer data-volume mount is needed.

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

Use the same two `-f` arguments for subsequent manager operations while this feature is enabled. The override preserves the primary HTTPS, administrator setup, TLS-host and external-origin settings. If you already use a custom command override, merge its certificate/listener arguments with the import arguments explicitly: Compose replaces command lists rather than appending them. Preserve the existing project, image, port and data volume. This may restart Dockspire and end browser sessions; it does not restart managed workloads.

After signing in, choose **Stacks → Import stack → Portainer**. Select a discovered stack, review the masked Compose preview, warnings and environment-file inclusion status, then choose **Import definition · leave containers running**. Dockspire rechecks the source fingerprint before saving. Source access requires existing containers whose Compose source label identifies `/data/compose/<numeric-id>/docker-compose.yml` or `.yaml`. Swarm stacks, other source layouts, and stacks with no existing containers are not discovered by this import path. Uploaded ordinary Compose files remain available through the other import choices for creating independent new drafts with unused project names.

Only the supported Compose and explicitly authorized environment files are copied. Portainer database files, separate key files, arbitrary attachments and workload data are excluded. The copied Compose text and environment files can contain workload credentials or keys. Keep the library and exported archives private. Removing the override and restarting `app` with the base file disables further Portainer reads; previously saved copies remain in the library. Workload migration requires a separate supported procedure and is not performed here.

## Upgrade and rollback

Select a tested image version or digest in `.env`, read its release notes, record the previous reference/digest, and back up the data volume privately while `app` is stopped. Retain the old image locally. Then run:

```sh
docker compose pull app
docker compose up -d --no-deps app
docker compose ps
```

Sign in again and check inventory, saved stacks and installation status. Preserve the Compose project, volume name and mount. Upgrading the manager does not require restarting managed workloads. Rollback selects the retained previous image and runs `docker compose up -d --no-deps --pull never app`; follow the release's data-restore instructions if it changes the storage format. This change does not migrate the library format.

Legacy `stacklane-preview` installations use different project/data/access settings. Updating one needs a reviewed deployment preserving its current data mount; do not start this template against an empty volume and assume the old library was migrated. [Offline archives and legacy preview installation](INSTALL-ARCHIVE.md) remain supported as a separate path.
