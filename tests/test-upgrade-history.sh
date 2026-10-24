#!/usr/bin/env bash
# Synthetic extracted-archive metadata only; no Docker, app or credentials.
set -Eeuo pipefail
parent=$(realpath "${TMPDIR:-/tmp}")
scratch=$(mktemp -d "$parent/dockspire-upgrade-history.XXXXXX")
cleanup() {
 local resolved
 resolved=$(realpath "$scratch") || return 1
 [[ "$resolved" == "$parent"/dockspire-upgrade-history.* && -d "$resolved" && ! -L "$scratch" ]] || return 1
 rm -rf -- "$resolved"
}
trap cleanup EXIT
trap 'printf "Upgrade history fixture failed: line=%s\n" "$LINENO" >&2' ERR
source <(awk '$0=="rollback_expected_history() {"{copy=1} copy{print} $0=="# End rollback history expectation."{exit}' tests/test-upgrade.sh)
assertion=$(grep -F '    [[ "$current_history" == "$expected_history" ]]' tests/test-upgrade.sh)
[[ -n "$assertion" && "$assertion" != *$'\n'* ]]
strict_match() {
 local current_history=$1 expected_history=$2
 source /dev/stdin <<< "$assertion"
}
initial=$(printf '%064d' 1)
first=$(printf '%064d' 2)
second=$(printf '%064d' 3)
extra=$(printf '%064d' 4)
checkpoint="$scratch/checkpoint"
base="$checkpoint/stacks/upgrade-old/revisions"
mkdir -p "$base/$first" "$base/$second"
touch -d '2026-10-23 12:34:56 UTC' "$base/$first" "$base/$second"
baseline=$(jq -nSc --arg i "$initial" --arg a "$first" --arg b "$second" '{
 nextCursor:$a,schemaCanary:"fixture",revisions:[
 {revision:$a,storedAt:"2026-10-23T12:34:56.9Z",selected:true,custom:"kept"},
 {revision:$b,storedAt:"2026-10-23T12:34:56.8Z",applied:true},
 {revision:$i,storedAt:"2026-10-23T12:34:56.5Z",initial:true}]}')
baseline_copy=$baseline
expected=$(jq -nSc --arg i "$initial" --arg a "$first" --arg b "$second" '{
 nextCursor:$a,schemaCanary:"fixture",revisions:[
 {revision:$i,storedAt:"2026-10-23T12:34:56.5Z",initial:true},
 {revision:$b,storedAt:"2026-10-23T12:34:56Z",applied:true},
 {revision:$a,storedAt:"2026-10-23T12:34:56Z",selected:true,custom:"kept"}]}')
derived=$(rollback_expected_history upgrade-old "$baseline")
strict_match "$derived" "$expected"
[[ "$baseline" == "$baseline_copy" ]]
# The source/candidate view is still exact, not transport-adjusted.
! strict_match "$baseline" "$expected"
for change in \
 '.revisions[2].revision="ffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffffff"' \
 '.revisions[2].selected=false' \
 '.revisions[1].applied=false' \
 '.revisions[2].recovery=true' \
 '.revisions[0].initial=false' \
 '.nextCursor="changed"' \
 'del(.schemaCanary)' \
 '.revisions[0].storedAt="2026-10-23T12:34:56.6Z"' \
 '.revisions[1].storedAt="2026-10-23T12:34:57Z"' \
 '.revisions|=reverse'; do
 actual=$(jq -Sc "$change" <<< "$expected")
 ! strict_match "$actual" "$derived"
done
expect_derivation_failure() {
 if rollback_expected_history upgrade-old "$1" >/dev/null 2>&1; then
  printf '%s\n' 'Invalid archive/history relationship accepted' >&2
  exit 1
 fi
}
touch -d '2026-10-23 12:34:57 UTC' "$base/$first"
expect_derivation_failure "$baseline"
touch -d '2026-10-23 12:34:56.1 UTC' "$base/$first"
expect_derivation_failure "$baseline"
touch -d '2026-10-23 12:34:56 UTC' "$base/$first"
rmdir "$base/$second"
expect_derivation_failure "$baseline"
mkdir "$base/$second"
touch -d '2026-10-23 12:34:56 UTC' "$base/$second"
mkdir "$base/$extra"
expect_derivation_failure "$baseline"
rmdir "$base/$extra"
changed=$(jq -Sc '.revisions[0].storedAt="2026-10-23T12:34:58Z"' <<< "$baseline")
expect_derivation_failure "$changed"
changed=$(jq -Sc '.revisions[0].revision=.revisions[1].revision' <<< "$baseline")
expect_derivation_failure "$changed"
printf '%s\n' 'Upgrade history transport: exact metadata/order preservation and corruption guards passed'
