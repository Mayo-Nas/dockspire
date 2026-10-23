#!/usr/bin/env bash
# Test-only tooling/profiles stay in ignored project storage; never connects to Docker.
set -euo pipefail
[[ $# -le 1 ]]
if [[ $# == 1 ]]; then [[ "$1" == --with-deps ]]; fi
root=$(cd "$(dirname "$0")/.." && pwd)
directory="$root/.local/browser-smoke"
mkdir -p "$directory/tmp"
export TEMP="$directory/tmp" TMP="$directory/tmp" TMPDIR="$directory/tmp"
export PLAYWRIGHT_BROWSERS_PATH="$directory/browsers"
export NPM_CONFIG_USERCONFIG="$directory/npm-user.rc" NPM_CONFIG_GLOBALCONFIG="$directory/npm-global.rc"
export NPM_CONFIG_CACHE="$directory/npm-cache" NPM_CONFIG_REGISTRY=https://registry.npmjs.org
for config in "$NPM_CONFIG_USERCONFIG" "$NPM_CONFIG_GLOBALCONFIG"; do
 [[ ! -L "$config" && ! -s "$config" ]]
 : > "$config"
done
for config in "$root/.npmrc" "$root/tests/.npmrc" "$root/tests/browser/.npmrc"; do [[ ! -e "$config" && ! -L "$config" ]]; done
npm ci --include=dev --ignore-scripts --no-audit --no-fund --prefix "$root/tests/browser" --fetch-timeout=30000 --fetch-retries=0
npm audit --include=dev --audit-level=low --prefix "$root/tests/browser" --fetch-timeout=30000 --fetch-retries=0
if [[ -z "${DOCKSPIRE_BROWSER_EXECUTABLE:-}" ]]; then
 install_args=(chromium)
 if [[ "${1:-}" == --with-deps ]]; then install_args=(--with-deps chromium); fi
 node "$root/tests/browser/node_modules/playwright/cli.js" install "${install_args[@]}"
fi
cd "$root"
export GOTOOLCHAIN=local GOTELEMETRY=off GOENV=off
export DOCKSPIRE_BROWSER_FIXTURE_BINARY="$directory/fixture$(go env GOEXE)"
go test -mod=readonly -c -o "$DOCKSPIRE_BROWSER_FIXTURE_BINARY" ./internal/app
node "$root/tests/browser/node_modules/@playwright/test/cli.js" test --config "$root/tests/browser/playwright.config.mjs"
