# Development and checks

Run from the repository root. Go 1.27.2 builds `cmd/dockspire`; backend code/tests are in `internal/app`. `web` embeds runtime HTML/CSS/JavaScript and assets, excluding Go source. Node 24 runs UI regression tests; no npm installation/build is required.

## Checks and native demo

```sh
go test ./...
go vet ./...
go build -o .local/bin/dockspire ./cmd/dockspire
node tests/test-auth.mjs
node tests/test-setup.mjs
node tests/test-navigation.mjs
node tests/test-stacks.mjs
node tests/test-revisions.mjs
sh tests/test-installation.sh
```

Use fictional data in a dedicated ignored directory:

```sh
./.local/bin/dockspire --demo --https --setup --listen 127.0.0.1:9443 --data-dir .local/demo-data
```

Verify the certificate fingerprint printed in this trusted console before entering the setup token/password in the browser. The offline `--tls-fingerprint --data-dir .local/demo-data` command reads only the public saved certificate. A restart rotates an unused setup token and opens a new five-minute window. Existing administrators are preserved. Demo mode never connects to Docker or deploys workloads.

Offline `--init-admin`/`--reset-admin` use piped private password input and require a stopped server. Legacy native HTTP defaults to loopback. See [operations](OPERATIONS.md) for input/recovery rules and [installation](INSTALL.md) for TLS trust, SANs and custom certificates.

## Container build and checks

```sh
docker build -t dockspire:local .
bash tests/test-container.sh dockspire:local
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
bash scripts/check-image-security.sh dockspire:local
```

The image builds the application and patched upstream Compose with digest-pinned Go builders. [The separate Compose manifest](../deploy/compose-runtime/README.md) records its dependency override and build procedure. The container tests create/remove their own container/volume on a random loopback port, checking real Docker/Compose, HTTPS, setup, login, certificate fingerprint and restart persistence. They never deploy a stack. Check host disk capacity before building; use a dedicated development host.

Production configuration is `deploy/compose.yaml`. To start a fresh installation from the local image on a Linux development host:

```sh
DOCKSPIRE_IMAGE=dockspire:local docker compose -f deploy/compose.yaml up -d --pull never
DOCKSPIRE_IMAGE=dockspire:local docker compose -f deploy/compose.yaml logs app
```

Follow [installation](INSTALL.md) for certificate verification and browser setup. This uses the same `dockspire` project and `dockspire_data` volume as the release template; use a dedicated development host, never a host with an existing Dockspire installation. It connects to the host Docker socket. For fictional inventory without Docker access, use the native demo above. `deploy/development/compose.demo.yaml` is also retained for existing demo workspaces and requires offline administrator initialization.

The old root Compose file, preview templates, archive packagers and archive installers have been retired. Existing deployments must retain their own configuration and data; see [upgrade and rollback](INSTALL.md#upgrade-and-rollback). Building locally does not publish a tag, release or image. Publication requires explicit authorization; see [GITHUB.md](GITHUB.md).

## Private/generated files

Use ignored `.local/` or an external workspace for caches, binaries, fixtures, reports and private operational notes. Never copy real installation history, environment values, addresses, usernames or private key paths into source documentation. Versioned regression tests belong in `tests/` or beside Go source; use documentation-reserved example addresses and fictional passwords.

## Application structure and state

Dockspire is one Go process serving embedded browser assets and a Docker/Compose API, with no frontend build step. The saved library stores complete configuration revisions. Durable deployment attempts distinguish saved, attempted, and observed state; interrupted work requires inspection before recovery. Portable archives exclude administrator credentials and TLS keys. Back up workload volumes and bind-mounted data separately.

Primary installations use `deploy/compose.yaml`, a versioned Linux amd64 image, a persistent data volume, direct HTTPS, and browser administrator setup. Changing existing project names or storage paths requires an explicit migration. The Docker socket grants host administration. Use a trusted management network or a verified SSH tunnel. Multi-host management and external-manager takeover are not implemented.

## Dependency attribution

`licenses/app-third-party.txt` and `licenses/compose-third-party.txt` retain a conservative superset of upstream licenses, copyright, patent and NOTICE files for the modules imported by each Linux amd64 target, plus the pinned Go standard library and bundled source notices. The container copies the entire `licenses/` directory.

After dependency or toolchain changes, use PowerShell 7 with the pinned Go toolchain and its checksum-verified public module cache:

```powershell
./scripts/generate-notices.ps1 -Go go
./scripts/generate-notices.ps1 -Go go -Check
```

Generated notices normalize line endings and trailing horizontal whitespace while preserving license wording. The generator verifies modules, fails on replacements, missing root licenses or unknown source versions, records module versions and relative notice paths, and rejects linked source entries. It does not infer legal compatibility or choose Dockspire’s own source license. Review any new or unfamiliar terms before distribution; regeneration and automated checks do not grant legal or trademark clearance. Keep dependency downloads/caches outside the checkout.

Compose validation limits captured expanded JSON to 8 MiB and cancels the producer on overflow; deployment output is discarded. This bounds the application’s retained subprocess output, but Compose can still allocate memory internally before producing output. Verify real Compose/container resource behavior on a disposable Linux installation before image distribution.
