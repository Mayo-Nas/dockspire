# Dockspire operations manual

For first installation and certificate verification, start with [INSTALL.md](INSTALL.md). Source builds and fictional demos are described in [DEVELOPMENT.md](DEVELOPMENT.md). Examples below use generic paths and fictional stack names.

## Administrator access

Current source containers start HTTP on 8787 and browser administrator setup without a token. Use a trusted management network: HTTP has no transport encryption and the first reachable client can claim the administrator during the setup window. Optional HTTPS is available. Setup expires after five minutes; restart app if unfinished. The fixed username is admin. Passwords have no length/composition policy; explicit matching blank browser fields persist passwordless access and take every visitor directly to the dashboard. Anyone who can reach that instance can control its Docker host. Setup cannot replace an existing administrator, and existing password accounts remain protected. Published v0.1.0 retains its tagged HTTPS/token instructions; see INSTALL.md before upgrading.

For password accounts, sign-out revokes the browser session; sessions expire after eight hours and end on restart. Passwordless mode hides sign-out and automatically creates a new browser session without sign-in; per-browser CSRF and exact Host/Origin checks still protect mutations. Password verification is limited per actual connection source, with one expensive verification at a time. Forwarded address headers are ignored. Clients sharing a NAT or reverse proxy share that source allowance; restrict access to the management network. Setup attempts share that allowance. Restarting clears temporary throttling state without changing stored access mode.

Native/legacy operation can initialize an administrator offline using piped private input, with the server stopped:

```sh
stacklane --data-dir ./data --init-admin < /path/to/private/password-file
```

Never put passwords in arguments, environment variables, shell history or chat. Nonempty offline stdin is literal UTF-8: trailing newlines, spaces and control characters are part of the password, with no trimming. Keep a password file's exact intended contents; empty EOF fails safely and cannot select passwordless mode. Offline input is bounded to 1 MiB and must also fit a 1 MiB encoded JSON login request; browser JSON bodies share that generic transport limit, including escaping and both setup fields. Oversize input is rejected, never truncated. Remove temporary plaintext input files after use. Do not run more than one server or maintenance process against a library.

To reset a forgotten password in the container installation:

```sh
docker compose stop app
docker compose run --rm -T --no-deps app --data-dir /appdata --reset-admin < /path/to/private/password-file
docker compose start app
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

Current source offers **Review takeover** on an imported Portainer copy. Nothing changes until you review the exact targets, type the project name and explicitly acknowledge the handoff. Back up mounted workload data and disable Portainer GitOps, webhooks and all other writers first. Portainer retains its visibility and manual controls; Dockspire cannot revoke its access or enforce a cross-manager lock. Do not delete the Portainer stack to transfer it: deletion may remove the same running workload. Future foreign-manager replacements cause Dockspire operations to be blocked.

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
docker compose stop app
docker compose run --rm --no-deps -v "$PWD/backups:/backups" app --data-dir /appdata --backup /backups/library.zip
docker compose start app
```

A restored library needs its own administrator/TLS setup. Inspect configuration and recovery state before reviewing any deployment. Restoring records does not authorize adoption of external containers or restore workload data. Moving a library to a new path may affect ownership checks; plan migration explicitly.

## Host packages and compatibility

Docker Engine and the Compose plugin used to launch the application are host-managed. Dockspire does not install or upgrade host packages. Its runtime image bundles a separately pinned Compose executable for stack operations. The optional host helper is not connected by the default installation; its runtime support and maintenance scripts remain available for existing installations.

The unchanged service template is now `deploy/host-helper/stacklane-host.service`. Existing helper maintainers can stage that file, a reviewed Linux `stacklane` executable built from `cmd/dockspire`, and the needed `scripts/*host*.sh` maintenance scripts together in a private directory. The scripts expect these files beside them; review each script before explicitly running it with root privileges on the intended Linux host. Keep existing helper service/socket paths and container overrides. The retired preview Compose override used old HTTP/host-Compose settings and must not be applied to the current container template.
For `restrict-host-helper.sh`, stage a `SHA256SUMS` file from the verified build that includes the `stacklane` executable; the replacement script verifies it before installation.

Existing formats and native executable names retain `stacklane`, `STACKLANE_*`, the `stacklane-library` archive kind and related compatibility identifiers. The product/browser name is Dockspire. Preserve an installation's project name and data mount when upgrading; see [INSTALL.md](INSTALL.md) for backup and rollback.
