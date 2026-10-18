#!/bin/sh
# Installer for the precompiled Linux release directory. Never installs host packages.
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$directory"
check_only=${1:-}
case "$check_only" in ''|--check-only) ;; *) printf '%s\n' 'Usage: sh install-preview.sh [--check-only]'; exit 2 ;; esac
sh ./check-installation.sh
compose_binary=${STACKLANE_COMPOSE_BINARY:-$(docker info --format '{{range .ClientInfo.Plugins}}{{if eq .Name "compose"}}{{.Path}}{{end}}{{end}}')}
if [ ! -f "$compose_binary" ] || [ ! -x "$compose_binary" ]; then
  printf '%s\n' 'Cannot locate the executable Compose plugin. Set STACKLANE_COMPOSE_BINARY to its absolute path.'
  exit 1
fi
if [ ! -f ./stacklane ]; then printf '%s\n' 'Run this installer from the extracted Linux release package.'; exit 1; fi
chmod 755 ./stacklane
./stacklane --compose-binary "$compose_binary" --check-installation
export STACKLANE_COMPOSE_PLUGIN_DIR="$(dirname -- "$compose_binary")"
set -- -f compose.yaml
docker compose "$@" config -q
if [ "$check_only" = --check-only ]; then exit 0; fi
if [ ! -f ./data/admin.json ]; then
  printf '%s\n' 'Initialize the administrator before starting Dockspire:' '  ./stacklane --data-dir ./data --init-admin < /path/to/private/password-file' 'Supply a single password line (at least 15 characters); then remove the password file.'
  exit 1
fi
docker compose "$@" up --build -d app
printf '%s\n' 'Dockspire started on host loopback port 8787. Use localhost or an SSH tunnel to view it.'
