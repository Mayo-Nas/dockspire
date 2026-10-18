# Dockspire operations manual

For first installation and certificate verification, start with [INSTALL.md](INSTALL.md). Source builds and fictional demos are described in [DEVELOPMENT.md](DEVELOPMENT.md). Examples below use generic paths and fictional stack names.

## Administrator access

The container installation starts HTTPS and browser administrator setup. Verify the certificate identity through an authenticated VM console/SSH session before entering the setup token or password. Setup expires after five minutes; restart `app` and retrieve its new one-time token if it expires. The fixed username is `admin`; passwords require at least 15 characters and at most 1024 UTF-8 bytes. Setup cannot replace an existing administrator.

Sign-out revokes the browser's session. Sessions expire after eight hours, and all sessions end on restart. Password verification is limited per actual connection source, with one expensive verification at a time. Forwarded address headers are ignored. Clients sharing a NAT or reverse proxy share that source allowance; restrict access to the management network. A valid one-time setup token can complete setup despite anonymous throttling from the same address. Restarting clears temporary throttling state without changing an existing password.

Native/legacy operation can initialize an administrator offline using piped private input, with the server stopped:

```sh
stacklane --data-dir ./data --init-admin < /path/to/private/password-file
```

Never put passwords in arguments, environment variables, shell history or chat. Remove temporary plaintext input files after use. Do not run more than one server or maintenance process against a library.

To reset a forgotten password in the container installation:

```sh
docker compose stop app
docker compose run --rm -T --no-deps app --data-dir /appdata --reset-admin < /path/to/private/password-file
docker compose start app
```

Reset requires a valid existing private administrator file. Do not delete that file or initialize a new volume as a reset shortcut. Keep whole-volume backups private: they include the password hash, TLS key and potentially secret stack configuration. Portable library archives exclude these authentication/TLS files.

For a TLS reverse proxy, configure an explicit HTTPS `--external-url`, preserve that Host header and restrict the backend HTTP listener to the proxy. Forwarded headers do not authorize alternate origins. The standard production template uses direct TLS and needs no reverse proxy. See the installation guide for exact settings.

## Container monitoring

Open a container's **Overview & health** for state, uptime, restart/exit information, ports and mounts. Environment values, commands, arbitrary labels and health-check output are omitted. Running state alone is not proof of application health.

**Resources** shows CPU, memory, network and disk I/O, and process/thread counts. Network/rate measurements need two samples; missing/reset counters are shown as unavailable. Only the selected view polls, and hiding/closing it pauses polling. Docker/cgroup support can limit available metrics.

**Logs** shows bounded timestamped stdout/stderr with search, pause/resume, refresh and time filters. Log output can itself contain secrets; Dockspire displays it as text and does not retain it. Shorten the range when a response is truncated. Monitoring does not adopt another manager's resources.

## Adding stacks

Choose **Stacks → Add stack**, enter an unused Compose project name and YAML, and optionally supply environment entries. **Validate Compose** checks the model without creating containers. **Save and review** saves a draft; deployment is a separate confirmed action.

Image-based services, named volumes, absolute Linux bind paths, and `.env`/`stack.env` are supported. Builds, includes, extends and arbitrary attachments are not supported by the editor. Treat command/comment text as sensitive too: automatic masking cannot identify every arbitrary secret.

**Import stack** accepts ordinary Compose/environment files from Compose-based tools through the draft editor. It does not automatically read another manager's files or adopt running workloads. Proprietary backup formats are unsupported. Portainer source-file discovery is a separate explicit opt-in requiring a reviewed read-only mount; leave it disabled unless authorized.

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
docker compose stop app
docker compose run --rm --no-deps -v "$PWD/backups:/backups" app --data-dir /appdata --backup /backups/library.zip
docker compose start app
```

A restored library needs its own administrator/TLS setup. Inspect configuration and recovery state before reviewing any deployment. Restoring records does not authorize adoption of external containers or restore workload data. Moving a library to a new path may affect ownership checks; plan migration explicitly.

## Host packages and compatibility

Docker Engine and the Compose plugin used to launch the application are host-managed. Dockspire does not install or upgrade host packages. Its runtime image bundles a separately pinned Compose executable for stack operations. The optional legacy host-helper scripts are not connected by the default installation.

Existing formats and native executable names retain `stacklane`, `STACKLANE_*`, the `stacklane-library` archive kind and related compatibility identifiers. The product/browser name is Dockspire. Preserve an installation's project name and data mount when upgrading; see [INSTALL.md](INSTALL.md) for backup and rollback.
