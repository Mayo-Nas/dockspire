# Offline archive and legacy preview installation

The primary installation now uses a published container image and browser setup; see [Linux VM installation](INSTALL.md). This archive path remains available for offline/legacy use and retains its terminal password initialization and loopback HTTP defaults. Do not apply it as a migration over an existing installation.

Dockspire runs in one Docker container. Docker Engine and the modern `docker compose` plugin must already be installed and accessible to the installing operator. The application uses the host's Docker socket and Compose plugin. It needs no Go/Node installation, separate database, or Git working copy on the production VM.

The currently verified platform is **Linux amd64 on Ubuntu**. The default release builder produces that platform only. Use [Docker's distribution-specific installation instructions](https://docs.docker.com/engine/install/) and [Compose plugin instructions](https://docs.docker.com/compose/install/linux/). Host package updates remain an operator responsibility.

## Release availability

No downloadable archive release exists yet. The version below is an example for a future release; use an actually published version when available.

For a normal installation, choose a published version rather than following a moving `main` branch. GitHub releases provide the archive; `gh` supports authenticated asset downloads while the repository is private. See [GitHub CLI release downloads](https://cli.github.com/manual/gh_release_download).

## Download and verify

On a Linux workstation or VM with an authenticated GitHub CLI account that can read the repository:

```sh
version=v0.1.0  # Example: select an actually published version.
mkdir -p "$HOME/dockspire-downloads/$version"
cd "$HOME/dockspire-downloads/$version"
gh release download "$version" --repo Mayo-Nas/dockspire \
  --pattern "dockspire-$version-linux-amd64.tar.gz" \
  --pattern "dockspire-$version-linux-amd64.tar.gz.sha256"
sha256sum -c "dockspire-$version-linux-amd64.tar.gz.sha256"
```

Download both assets from the authenticated GitHub release. Checksums detect an incomplete or changed download; they are not an independent signature of publisher identity. Alternatively download on your workstation and copy both files to the VM; a GitHub login is then unnecessary on the production VM.

Create a new, dedicated installation directory, owned by the installing operator, and extract there:

```sh
sudo install -d -m 0750 -o "$(id -un)" -g "$(id -gn)" /opt/dockspire
tar -xzf "dockspire-$version-linux-amd64.tar.gz" -C /opt/dockspire
cd /opt/dockspire
bash install.sh --check-only
bash install.sh
```

For first installation, use a new directory rather than an existing application's directory. `--check-only` verifies bundled files and the host's Docker/Compose access without initializing an administrator or starting the app. Normal installation prompts twice for a password without echoing it, creates the fixed `admin` account only if missing, then builds the small local image from the bundled executable and starts only the manager service. It does not download a Go build image or install host packages.

If Compose is installed in an unusual location, set `STACKLANE_COMPOSE_BINARY` to its executable's absolute path before running the installer. The compatibility installer discovers the normal plugin path and mounts its directory read-only. The runtime image is built locally from `scratch`, so the VM does not need an authenticated container registry login.

For a headless first install, supply one password line on stdin:

```sh
# Read from an operator-managed private input source, never a command argument.
bash install.sh --init-admin-stdin < /path/to/private/password-file
```

Remove a temporary plaintext password file after use. This mode refuses to replace an existing administrator. Password reset remains an explicit offline operation described in the [operations manual](OPERATIONS.md#administrator-access).

## Open the application

The application binds the VM's **127.0.0.1:8787**. From your workstation:

```sh
ssh -N -L 8788:127.0.0.1:8787 USER@VM
```

Open `http://127.0.0.1:8788` and sign in as `admin`. Keep the tunnel running. LAN/TLS reverse-proxy access is optional and requires deliberate configuration; see [administrator access](OPERATIONS.md#administrator-access).

## What persists

```text
/opt/dockspire/
  stacklane                 Compiled Dockspire executable (compatibility filename)
  compose.yaml              Manager deployment
  Dockerfile                Copies the executable into its runtime image
  install.sh                Interactive first-install entry point
  install-preview.sh        Compatible dependency/startup implementation
  RELEASE.txt               Source commit, version and platform
  SHA256SUMS                Bundled-file checksums
  licenses/                 Dependency notices
  data/                     Private administrator and stack library; mounted at /appdata
```

Keep `data/` at the same path when upgrading. It contains credentials and may contain Compose environment secrets. Whole-directory backups need private storage; portable library exports deliberately omit credentials and do not back up workload volumes or bind-mounted data. Back up workload data separately.

The runtime executable/image/project/environment names currently retain `stacklane` / `stacklane-preview` / `STACKLANE_*` for compatibility. The browser/product name is Dockspire. Do not rename a running Compose project or change its data mount during an application upgrade.

## Upgrade and rollback

Upgrades are operator-controlled; the installer does not auto-update from GitHub.

1. Download the chosen release and verify its external checksum in a separate directory. Extract it into a separate staging directory and verify its internal `SHA256SUMS` before running its code.
2. Finish any active manager operation. Record the current `RELEASE.txt`, Compose settings and exact running image ID. Tag that image with a distinct rollback tag. Retain the previous release's runtime files.
3. Stop only the manager's `app` service and privately back up its data while stopped. Do not stop or recreate the workloads it manages. Include workload backups as appropriate.
4. Copy the new release's runtime files into the existing installation directory, preserving `data/`, any local environment/configuration and the reviewed Compose settings. Do not copy a new Compose template over customized settings blindly. Review release notes before any data-format change.
5. Run `bash install.sh --check-only`, then `bash install.sh`. An existing administrator is preserved. Check health, sign in, inspect real Docker inventory and saved stacks, and compare the pre-upgrade workload baseline. Sessions end on a manager restart.

Rollback uses the retained matching runtime files and image: restore the previous files/settings, tag the retained image as `stacklane-preview:local`, then run `docker compose up --no-build --pull never -d app`. If a future release changes the data format, follow that release's documented restore/rollback procedure; retagging an image alone is not a data migration rollback. This reorganization does not change the data format.

## Source builds

Cloning GitHub and building the Dockerfile remains a development/early-testing option, described in [DEVELOPMENT.md](DEVELOPMENT.md). The Docker build downloads the Go toolchain image and module dependency. That path is separate from the precompiled release installation above and is currently the only GitHub-based source path until the first authorized push/release exists.
