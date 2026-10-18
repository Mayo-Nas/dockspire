#!/bin/sh
# Run on the Docker host before installing or starting Dockspire. No packages are changed.
set -eu
failed=0
if ! command -v docker >/dev/null 2>&1; then
  printf '%s\n' 'MISSING: Docker CLI. Install Docker Engine using https://docs.docker.com/engine/install/'
  failed=1
else
  printf 'Docker CLI: '
  docker --version
  if ! docker version --format 'Docker Engine: {{.Server.Version}} (API {{.Server.APIVersion}})'; then
    printf '%s\n' 'UNAVAILABLE: Docker daemon. Check installation, service status, and socket permissions.'
    failed=1
  fi
  if docker compose version --short >/dev/null 2>&1; then
    printf 'Docker Compose: '
    docker compose version --short
  else
    printf '%s\n' 'MISSING: modern Docker Compose plugin. See https://docs.docker.com/compose/install/linux/'
    failed=1
  fi
fi
if [ "$failed" -ne 0 ]; then
  printf '%s\n' 'Installation checks failed. No packages were installed and Dockspire was not started.'
  exit 1
fi
printf '%s\n' 'Docker and Compose installation checks passed.'
