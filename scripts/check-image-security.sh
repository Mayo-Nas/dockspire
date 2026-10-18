#!/usr/bin/env bash
# Inspect the exact shipped Go executables without starting the image.
set -euo pipefail
image=${1:?Usage: bash scripts/check-image-security.sh IMAGE [GOVULNCHECK]}
scanner=${2:-govulncheck}
directory=$(mktemp -d "${TMPDIR:-/tmp}/dockspire-image-scan.XXXXXX")
container=''
cleanup() {
  if [[ -n "$container" ]]; then docker rm "$container" >/dev/null; fi
  rm -f -- "$directory/stacklane" "$directory/docker-compose"
  rmdir -- "$directory"
}
trap cleanup EXIT
container=$(docker create "$image")
docker cp "$container:/stacklane" "$directory/stacklane"
docker cp "$container:/docker-compose" "$directory/docker-compose"
"$scanner" -mode=binary "$directory/stacklane"
"$scanner" -mode=binary "$directory/docker-compose"
