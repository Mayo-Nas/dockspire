# Bundled Compose build

This isolated build manifest compiles upstream Docker Compose v5.6.0 with Go 1.27.3 and golang.org/x/net v0.60.0. It does not add these dependencies to the Dockspire application module. Module source and dependency checksums are recorded in go.sum; the Docker builder is pinned by digest and refuses an unexpected Go version.

The patched executable identifies itself as `v5.6.0-dockspire.1`. Upstream source is unchanged; the build uses the dependency overrides here to replace the older Go/networking components in the upstream precompiled artifact. Keep the symbol table (`-w`, without `-s`) so binary vulnerability scanning can distinguish linked packages from unused packages in dependency modules. Builds use `-trimpath` to omit workstation paths.

Reproduce from this directory:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -tags=e2e \
  -ldflags='-w -X github.com/docker/compose/v5/internal.Version=v5.6.0-dockspire.1' \
  -o ../../.local/bin/docker-compose github.com/docker/compose/v5/cmd
```

Place generated executables outside the checkout. Scan both final image executables, then run the container installation tests before publishing an image. A dependency module can contain unused vulnerable packages: a module-only advisory is not proof that those packages are linked or called. Do not suppress an advisory without checking the actual import graph and binary.

## Dependency updates

Update this standalone build manifest manually; it has no local Go package for dependency automation to inspect. Do not run `go mod tidy` here: it can remove the explicit upstream build requirements and their checksums.

1. Select and review exact upstream Compose and dependency versions, then update their explicit `require` entries. Preserve the pinned Go toolchain and builder digest unless their update is also reviewed. Review the upstream changes and any networking override for compatibility.
2. Resolve the selected public modules with the pinned toolchain and checksum verification; retain the complete `go.sum` needed by the build. Regenerate and verify both dependency notice inventories using the [notice procedure](../../docs/DEVELOPMENT.md#dependency-attribution). Review the module/checksum/notice diff; an empty requirement block or checksum file is not a successful update.
3. Run the `-mod=readonly -tags=e2e` build above, then build the complete image and run the [container installation, disposable takeover and exact executable vulnerability checks](../../docs/DEVELOPMENT.md#container-build-and-checks). Scan both the application and bundled Compose binaries; review findings rather than suppressing module-only advisories. Complete the release upgrade/rollback check before publishing.

Root application Go, browser-test npm, Actions and Dockerfile dependency updates remain automated; this exception applies only to the standalone Compose build manifest. No dependency update publishes an image or changes an installation automatically.

Upstream license: Apache-2.0; see the notices in [licenses](../../licenses/compose-NOTICE) and the [license text](../../licenses/Apache-2.0.txt).

The complete generated dependency notice inventory is [compose-third-party.txt](../../licenses/compose-third-party.txt), including Logrus and standard-library attribution. Regenerate and verify both target inventories with [the documented notice procedure](../../docs/DEVELOPMENT.md#dependency-attribution) when changing dependencies.
