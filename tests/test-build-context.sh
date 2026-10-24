#!/usr/bin/env bash
# Probe Docker's actual COPY-visible context using fictional files only.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
command -v docker >/dev/null
base=$(cd "${TMPDIR:-/tmp}" && pwd -P)
fixture=$(mktemp -d "$base/dockspire-build-context.XXXXXX")
cleanup() {
  [[ "$fixture" == "$base"/dockspire-build-context.* && -d "$fixture" && ! -L "$fixture" ]] || return 1
  rm -rf -- "$fixture"
}
trap cleanup EXIT
mkdir "$fixture/context"
included=(
  Dockerfile LICENSE NOTICE go.mod go.sum
  cmd/dockspire/main.go internal/app/app.go web/embed.go
  web/index.html web/style.css web/app.js web/brand/dockspire-icon-256.png web/brand/dockspire-horizontal-dark.png
  licenses/app-third-party.txt licenses/yaml-LICENSE licenses/compose-NOTICE
  deploy/compose-runtime/go.mod deploy/compose-runtime/go.sum
)
excluded=(
  web/favicon.ico web/brand/dockspire-icon-64.png web/brand/dockspire-icon-128.png
  .dockerignore .env .env.production .git/config .local/settings.json .vscode/settings.json
  tests/fixture.go README.md docs/INSTALL.md scripts/run.sh
  cmd/dockspire/main_test.go internal/app/app_test.go web/embed_test.go
  internal/app/tests/fixture.go internal/app/testdata/fixture.go
  internal/app/.git/config.go web/.vscode/settings.js internal/.idea/project.go
  internal/.vscode/settings.go
  internal/.local/private.go web/.codex/private.js internal/.agents/private.go
  cmd/.ssh/fixture.go internal/.aws/config.go internal/app/.env.go
  internal/app/private.pem internal/app/private.key web/fixture.pfx web/fixture.p12
  internal/app/admin.json internal/app/secrets/fixture.go
  internal/app/data/fixture.go internal/app/backups/fixture.go
  internal/config.json cmd/config.yaml web/settings.json licenses/local-config.json
  deploy/compose.yaml deploy/compose.portainer.yaml deploy/development/compose.demo.yaml
  deploy/compose-runtime/README.md deploy/compose-runtime/config.json
)
for name in "${included[@]}" "${excluded[@]}"; do
  mkdir -p -- "$fixture/context/$(dirname "$name")"
  printf '%s\n' fixture > "$fixture/context/$name"
done
cp "$root/.dockerignore" "$fixture/context/.dockerignore"
cat > "$fixture/context/Dockerfile" <<'DOCKERFILE'
FROM scratch
COPY . /context/
DOCKERFILE
if ! DOCKER_BUILDKIT=1 docker build --no-cache --network=none --progress=plain \
  --output "type=local,dest=$fixture/output" "$fixture/context" > "$fixture/build.log" 2>&1; then
  cat "$fixture/build.log" >&2
  exit 1
fi
for name in "${included[@]}"; do
  [[ -f "$fixture/output/context/$name" ]] || { printf 'Required build input missing: %s\n' "$name" >&2; exit 1; }
done
for name in "${excluded[@]}"; do
  [[ ! -e "$fixture/output/context/$name" ]] || { printf 'Excluded fixture entered build context: %s\n' "$name" >&2; exit 1; }
done
actual=$(find "$fixture/output/context" -type f | wc -l)
[[ "$actual" -eq "${#included[@]}" ]]
printf 'Docker build context: %s required inputs retained, %s excluded fixtures absent\n' "${#included[@]}" "${#excluded[@]}"
