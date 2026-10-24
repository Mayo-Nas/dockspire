#!/usr/bin/env bash
# Offline authorization tests: no package API, registry, or Docker daemon access.
set -euo pipefail
root=$(pwd)
head=$(git rev-parse HEAD)
scratch=$(mktemp -d "${TMPDIR:-/tmp}/dockspire-baseline-policy.XXXXXX")
cleanup() {
 [[ "$scratch" == "${TMPDIR:-/tmp}"/dockspire-baseline-policy.* && -d "$scratch" && ! -L "$scratch" ]] || return 1
 rm -rf -- "$scratch"
}
trap cleanup EXIT
mkdir "$scratch/bin"
printf '#!/usr/bin/env bash\nprintf invoked >> "$POLICY_MARKER"\nexit 91\n' > "$scratch/bin/gh"
cp "$scratch/bin/gh" "$scratch/bin/docker"
chmod 700 "$scratch/bin/gh" "$scratch/bin/docker"
export PATH="$scratch/bin:$PATH" POLICY_MARKER="$scratch/side-effect"
check() {
 local expected=$1; shift
 local result=0
 env GITHUB_REPOSITORY=Mayo-Nas/dockspire GITHUB_EVENT_NAME=workflow_dispatch \
  GITHUB_ACTOR=Mayo-Nas GITHUB_TRIGGERING_ACTOR=Mayo-Nas GITHUB_REF_TYPE=branch \
  GITHUB_REF=refs/heads/reviewed DOCKSPIRE_REVIEWED_REF=refs/heads/reviewed \
  GITHUB_SHA="$head" DOCKSPIRE_REVIEWED_COMMIT="$head" \
  GH_TOKEN=fictional-policy-token RUNNER_TEMP="$scratch" BASH_ENV= ENV= \
  "$@" bash "$root/scripts/pull-upgrade-baseline.sh" --check-trust >/dev/null 2>&1 || result=$?
 if [[ "$expected" == allow ]]; then [[ "$result" == 0 ]]; else [[ "$result" != 0 ]]; fi
 [[ ! -e "$POLICY_MARKER" ]]
}
check allow
check allow GITHUB_REF=refs/heads/main GITHUB_EVENT_NAME=push
check allow GITHUB_REF=refs/heads/main GITHUB_EVENT_NAME=schedule
# Publisher dispatches main then checks out a reviewed tag: do not force workflow SHA.
check allow GITHUB_REF=refs/heads/main GITHUB_SHA=0000000000000000000000000000000000000000 DOCKSPIRE_REVIEWED_COMMIT=
check deny GITHUB_REPOSITORY=other/dockspire
check deny GITHUB_EVENT_NAME=pull_request
check deny GITHUB_EVENT_NAME=pull_request_target
check deny GITHUB_REF=refs/heads/main GITHUB_EVENT_NAME=pull_request
check deny GITHUB_REF=refs/heads/main GITHUB_EVENT_NAME=pull_request_target
check deny GITHUB_REF=refs/heads/main GITHUB_ACTOR=other
check deny GITHUB_REF=refs/heads/main GITHUB_TRIGGERING_ACTOR=other
check deny GITHUB_ACTOR=other
check deny GITHUB_TRIGGERING_ACTOR=other
check deny GITHUB_REF_TYPE=tag
check deny GITHUB_REF=refs/tags/reviewed DOCKSPIRE_REVIEWED_REF=refs/tags/reviewed
check deny DOCKSPIRE_REVIEWED_REF=refs/heads/other
check deny DOCKSPIRE_REVIEWED_REF=
check deny DOCKSPIRE_REVIEWED_COMMIT=
check deny DOCKSPIRE_REVIEWED_COMMIT=bad
check deny DOCKSPIRE_REVIEWED_COMMIT=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA GITHUB_SHA=AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA
check deny GITHUB_SHA=0000000000000000000000000000000000000000
check deny DOCKSPIRE_REVIEWED_COMMIT=0000000000000000000000000000000000000000 GITHUB_SHA=0000000000000000000000000000000000000000
check deny GITHUB_EVENT_NAME=push
# Denied full invocation must also stop before token/API/Docker use.
if env GITHUB_REPOSITORY=other/dockspire GITHUB_REF=refs/heads/main \
 GH_TOKEN=fictional-policy-token RUNNER_TEMP="$scratch" BASH_ENV= ENV= \
 bash "$root/scripts/pull-upgrade-baseline.sh" >/dev/null 2>&1; then exit 1; fi
[[ ! -e "$POLICY_MARKER" ]]
printf '%s\n' 'Upgrade baseline trust-policy fixtures passed'
