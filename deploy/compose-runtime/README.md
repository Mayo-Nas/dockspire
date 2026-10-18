# Bundled Compose build

This isolated build manifest compiles upstream Docker Compose v5.6.0 with Go 1.27.2 and golang.org/x/net v0.60.0. It does not add these dependencies to the Dockspire application module. Module source and dependency checksums are recorded in go.sum; the Docker builder is pinned by digest and refuses an unexpected Go version.

The patched executable identifies itself as `v5.6.0-dockspire.1`. Upstream source is unchanged; the build uses the dependency overrides here to replace the older Go/networking components in the upstream precompiled artifact. Keep the symbol table (`-w`, without `-s`) so binary vulnerability scanning can distinguish linked packages from unused packages in dependency modules. Builds use `-trimpath` to omit workstation paths.

Reproduce from this directory:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -mod=readonly -trimpath -tags=e2e \
  -ldflags='-w -X github.com/docker/compose/v5/internal.Version=v5.6.0-dockspire.1' \
  -o ../../.local/bin/docker-compose github.com/docker/compose/v5/cmd
```

Place generated executables outside the checkout. Scan both final image executables, then run the container installation tests before publishing an image. A dependency module can contain unused vulnerable packages: a module-only advisory is not proof that those packages are linked or called. Do not suppress an advisory without checking the actual import graph and binary.

Upstream license: Apache-2.0; see the notices in [licenses](../../licenses/compose-NOTICE) and the [license text](../../licenses/Apache-2.0.txt).

The complete generated dependency notice inventory is [compose-third-party.txt](../../licenses/compose-third-party.txt), including Logrus and standard-library attribution. Regenerate and verify both target inventories with [the documented notice procedure](../../docs/DEVELOPMENT.md#dependency-attribution) when changing dependencies.
