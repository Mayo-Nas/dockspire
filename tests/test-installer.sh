#!/bin/bash
# Test release setup with fictional files and executables; never contacts Docker.
set -euo pipefail
source_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
temp_parent=$(CDPATH= cd -- "${TMPDIR:-/tmp}" && pwd -P)
fixture=$(mktemp -d "$temp_parent/dockspire-installer.XXXXXX")
cleanup() {
  case "$fixture" in "$temp_parent"/dockspire-installer.*) rm -rf -- "$fixture" ;; *) printf '%s\n' 'Refusing unexpected cleanup path' >&2 ;; esac
}
trap cleanup EXIT HUP INT TERM

prepare() {
  directory="$fixture/$1"
  mkdir -p "$directory"
  cp "$source_root/scripts/install.sh" "$directory/install.sh"
  cat > "$directory/install-preview.sh" <<'GATE'
#!/bin/sh
if [ "${1:-}" = --check-only ]; then
  printf 'check\n' >> trace
  exit "${FIXTURE_GATE_STATUS:-0}"
fi
test -f data/admin.json || exit 23
printf 'start\n' >> trace
GATE
  cat > "$directory/stacklane" <<'APP'
#!/bin/bash
set -eu
test "$*" = '--data-dir ./data --init-admin'
IFS= read -r password
test "${#password}" -ge 15
test ! -e data/admin.json
mkdir -p data
printf 'fictional-credential-record\n' > data/admin.json
printf 'init\n' >> trace
APP
  chmod 755 "$directory/stacklane"
  (cd "$directory" && sha256sum install.sh install-preview.sh stacklane > SHA256SUMS)
}

prepare check
bash "$directory/install.sh" --check-only > "$directory/output"
test "$(cat "$directory/trace")" = check
test ! -d "$directory/data"

prepare corrupt
printf '\n# changed after packaging\n' >> "$directory/stacklane"
if bash "$directory/install.sh" --check-only > "$directory/output" 2>&1; then printf '%s\n' 'Corrupt bundle was accepted'; exit 1; fi
test ! -f "$directory/trace"

prepare noninteractive
if bash "$directory/install.sh" < /dev/null > "$directory/output" 2>&1; then printf '%s\n' 'Missing administrator was silently accepted'; exit 1; fi
test ! -d "$directory/data"
test "$(cat "$directory/trace")" = check

prepare first
fictional='fictional installer password only'
printf '%s\n' "$fictional" | bash "$directory/install.sh" --init-admin-stdin > "$directory/output"
test "$(cat "$directory/trace")" = $'check\ninit\nstart'
test "$(cat "$directory/data/admin.json")" = fictional-credential-record
case "$(cat "$directory/output")" in *"$fictional"*) printf '%s\n' 'Password was printed'; exit 1 ;; esac

prepare existing
mkdir "$directory/data"
printf 'retained-record\n' > "$directory/data/admin.json"
bash "$directory/install.sh" < /dev/null > "$directory/output"
test "$(cat "$directory/trace")" = $'check\nstart'
test "$(cat "$directory/data/admin.json")" = retained-record
if printf '%s\n' "$fictional" | bash "$directory/install.sh" --init-admin-stdin > "$directory/output" 2>&1; then printf '%s\n' 'Existing administrator replacement was accepted'; exit 1; fi
test "$(cat "$directory/data/admin.json")" = retained-record

prepare failed-gate
if printf '%s\n' "$fictional" | FIXTURE_GATE_STATUS=17 bash "$directory/install.sh" --init-admin-stdin > "$directory/output" 2>&1; then printf '%s\n' 'Dependency failure was ignored'; exit 1; fi
test ! -d "$directory/data"
test "$(cat "$directory/trace")" = check

prepare invalid-args
if bash "$directory/install.sh" --unknown > "$directory/output" 2>&1; then exit 1; fi
if bash "$directory/install.sh" --check-only extra > "$directory/output" 2>&1; then exit 1; fi
test ! -f "$directory/trace"
printf '%s\n' 'Installer: checksums, check-only, first initialization, existing-account preservation, headless input, failed gates and invalid arguments passed'
