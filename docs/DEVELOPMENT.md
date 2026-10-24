# Development and checks

## Branch policy

`main` holds the complete source, regression tests, CI, documentation and release tools. Keep runtime images and release assets minimal through packaging, rather than maintaining different file sets on separate branches.

Use short-lived branches for focused changes to `main`. Complete independent review, the four ordinary PR checks, and the owner-dispatched full upgrade/rollback job on the exact feature HEAD before merging. Both instrumented and original-entrypoint matrices must pass; skipped jobs and stale results do not count. Follow the [exact-ref/SHA dispatch procedure](GITHUB.md#development-and-checks). Keep corrective testing off main until those gates pass.

Run from the repository root. Go 1.27.3 builds `cmd/dockspire`; backend code/tests are in `internal/app`. `web` embeds runtime HTML/CSS/JavaScript and assets, excluding Go source. Node 24 runs CI browser checks. Fast JavaScript regressions need no npm installation; the real-browser smoke suite uses a separate pinned test-only package and lockfile.

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
bash tests/test-upgrade-baseline-policy.sh
```

Use fictional data in a dedicated ignored directory:

```sh
./.local/bin/dockspire --demo --setup --listen 127.0.0.1:8787 --data-dir .local/demo-data
```

Open http://127.0.0.1:8787 and create the administrator within five minutes; no setup token is needed. Restarting reopens an unfinished setup window without replacing an existing administrator. Native HTTP remains loopback-only unless --allow-http or an explicit origin is supplied. Demo mode never connects to Docker or deploys workloads.

Offline `--init-admin`/`--reset-admin` use literal nonempty UTF-8 stdin and require a stopped server; trailing newlines are password characters and empty EOF fails. Explicit matching blank browser setup fields persist passwordless mode. Generic auth JSON bodies are capped at 1 MiB; see [operations](OPERATIONS.md) for exact input/recovery rules and [installation](INSTALL.md) for TLS trust, SANs and custom certificates. Native HTTP defaults to loopback.

## Real-browser smoke test

The fast `tests/test-*.mjs` regressions use a mocked DOM. The separate `tests/browser/` suite pins test-only Playwright 1.64.1 and drives a real browser through administrator setup, sign-in, draft creation/editing and deployment review. Its opt-in Go test fixture uses actual authentication/routes/library on a random loopback port, fictional inventory, and a runner that rejects container operations. It stops before a confirmed deployment; it never connects to Docker.

```sh
bash scripts/check-browser.sh
```

The helper uses empty project-local npm configs, the committed lockfile, a dependency audit, and ignored `.local/browser-smoke/` caches, browser profiles, fixture data and results. It does not introduce a frontend build or runtime dependency. Set `DOCKSPIRE_BROWSER_EXECUTABLE` to an existing Chrome/Chromium executable to avoid downloading a browser. The helper's optional `--with-deps` flag installs OS browser dependencies and is intended for a disposable Linux runner; do not use it on another host without approving that system change. CI uses it only on ephemeral hosted Linux runners.

## Container build and checks

The topbar shows the embedded build version. Unversioned source builds use `dev`; release publication passes its validated tag through Docker's `VERSION` build argument into the Go binary. For a labeled development build, use `docker build --build-arg VERSION=v0.1.2-dev -t dockspire:local .` and `bash tests/test-container.sh dockspire:local v0.1.2-dev`. A development label is not a published release.

```sh
docker build -t dockspire:local .
bash tests/test-container.sh dockspire:local
go install golang.org/x/vuln/cmd/govulncheck@v1.9.0
bash scripts/check-image-security.sh dockspire:local
```

The image builds the application and patched upstream Compose with digest-pinned Go builders. [The separate Compose manifest](../deploy/compose-runtime/README.md) records its dependency override and build procedure. The container tests create/remove their own container/volume on a random loopback port, checking real Docker/Compose, default HTTP and optional HTTPS, tokenless setup, cookie policy, login, TLS fingerprint and restart persistence. They never deploy a stack. Check host disk capacity before building; use a dedicated development host.

Production configuration is `deploy/compose.yaml`. To start a fresh installation from the local image on a Linux development host:

```sh
DOCKSPIRE_IMAGE=dockspire:local docker compose -f deploy/compose.yaml up -d --pull never
DOCKSPIRE_IMAGE=dockspire:local docker compose -f deploy/compose.yaml logs app
```

Follow [installation](INSTALL.md) for browser setup and optional HTTPS. The HTTP template supports v0.1.1/v0.1.2 release images (after publication) or a matching source build; v0.1.0 uses its tagged HTTPS template. This uses the same `dockspire` project and `dockspire_data` volume as the release template; use a dedicated development host, never a host with an existing Dockspire installation. It connects to the host Docker socket. For fictional inventory without Docker access, use the native demo above. `deploy/development/compose.demo.yaml` is also retained for existing demo workspaces and requires offline administrator initialization.

The old root Compose file, preview templates, archive packagers and archive installers have been retired. Existing deployments must retain their own configuration and data; see [upgrade and rollback](INSTALL.md#upgrade-and-rollback). Building locally does not publish a tag, release or image. Publication requires explicit authorization; see [GITHUB.md](GITHUB.md).

On a dedicated Linux amd64 development Docker host, opt in to the disposable takeover test using an already-built local image ID: `DOCKSPIRE_TAKEOVER_TEST=1 DOCKSPIRE_TEST_IMAGE="$(docker image inspect --format '{{.Id}}' dockspire:local)" go test ./internal/app -run '^TestTakeoverDockerIntegration$' -count=1 -timeout=5m`. It creates only new randomized synthetic projects, tests actual Compose handoff/redeploy and data markers, then removes its own containers, volumes and network. It never imports an existing stack. Ordinary Go tests skip this fixture. Container CI and image publication run it before executable scans/publication.

## Private/generated files

Use ignored `.local/` or an external workspace for caches, binaries, fixtures, reports and private operational notes. Never copy real installation history, environment values, addresses, usernames or private key paths into source documentation. Versioned regression tests belong in `tests/` or beside Go source; use documentation-reserved example addresses and fictional passwords.

## Application structure and state

Dockspire is one Go process serving embedded browser assets and a Docker/Compose API, with no frontend build step. The saved library stores complete configuration revisions. Durable deployment attempts distinguish saved, attempted, and observed state; interrupted work requires inspection before recovery. Portable archives exclude administrator credentials and TLS keys. Back up workload volumes and bind-mounted data separately.

Primary installations use `deploy/compose.yaml`, a versioned Linux amd64 image, a persistent data volume, HTTP on 8787 (optional HTTPS), and browser administrator setup. Changing existing project names or storage paths requires an explicit migration. The Docker socket grants host administration. Use a trusted management network or a verified SSH tunnel. Multi-host management is not implemented. v0.1.1 and later support an explicit, conservatively checked Portainer takeover workflow; it never runs automatically.

## Dependency attribution

`licenses/app-third-party.txt` and `licenses/compose-third-party.txt` retain a conservative superset of upstream licenses, copyright, patent and NOTICE files for the modules imported by each Linux amd64 target, plus the pinned Go standard library and bundled source notices. The container copies the entire `licenses/` directory.

After dependency or toolchain changes, use PowerShell 7 with the pinned Go toolchain and its checksum-verified public module cache:

```powershell
./scripts/generate-notices.ps1 -Go go
./scripts/generate-notices.ps1 -Go go -Check
```

Generated notices normalize line endings and trailing horizontal whitespace while preserving license wording. The generator verifies modules, fails on replacements, missing root licenses or unknown source versions, records module versions and relative notice paths, and rejects linked source entries. It does not infer legal compatibility or choose Dockspire’s own source license. Review any new or unfamiliar terms before distribution; regeneration and automated checks do not grant legal or trademark clearance. Keep dependency downloads/caches outside the checkout.

Compose validation limits captured expanded JSON to 8 MiB and cancels the producer on overflow; deployment output is discarded. This bounds the application’s retained subprocess output, but Compose can still allocate memory internally before producing output. Verify real Compose/container resource behavior on a disposable Linux installation before image distribution.
