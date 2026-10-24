#!/usr/bin/env bash
# Package/image identity and credential-cleanup fixtures; never contacts Docker/GitHub.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
head=$(git rev-parse HEAD)
parent=$(realpath "${TMPDIR:-/tmp}")
scratch=$(mktemp -d "$parent/dockspire-baseline-metadata.XXXXXX")
cleanup() {
  [[ "$scratch" == "$parent"/dockspire-baseline-metadata.* && -d "$scratch" && ! -L "$scratch" ]] || return 1
  rm -rf -- "$scratch"
}
trap cleanup EXIT
mkdir "$scratch/bin"
export DOCKSPIRE_BASELINE_FIXTURE="$scratch" PATH="$scratch/bin:$PATH"
reference=ghcr.io/mayo-nas/dockspire@sha256:265b222a8993679dc6920e5e06b7184b57159571f68a6cd1de66047e05c0f639
cat > "$scratch/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
[[ ! -e "$DOCKSPIRE_BASELINE_FIXTURE/api-fail" ]]
[[ "$1" == api ]]
if [[ $# == 2 && "$2" == users/Mayo-Nas/packages/container/dockspire ]]; then
  cat "$DOCKSPIRE_BASELINE_FIXTURE/package.json"
elif [[ $# == 4 && "$2" == --paginate && "$3" == --slurp && "$4" == 'users/Mayo-Nas/packages/container/dockspire/versions?per_page=100' ]]; then
  cat "$DOCKSPIRE_BASELINE_FIXTURE/versions.json"
else exit 91; fi
GH
cat > "$scratch/bin/docker" <<'DOCKER'
#!/usr/bin/env bash
set -euo pipefail
reference=ghcr.io/mayo-nas/dockspire@sha256:265b222a8993679dc6920e5e06b7184b57159571f68a6cd1de66047e05c0f639
printf '%s\n' "$1" >> "$DOCKSPIRE_BASELINE_FIXTURE/calls"
[[ "$DOCKER_CONFIG" == "$RUNNER_TEMP"/dockspire-upgrade-registry.* && -d "$DOCKER_CONFIG" && ! -L "$DOCKER_CONFIG" ]]
case "$1" in
  login)
    [[ $# == 5 && "$2" == ghcr.io && "$3" == --username && "$4" == Mayo-Nas && "$5" == --password-stdin ]]
    [[ $(cat) == fictional-policy-token ]]
    printf '%s' "$DOCKER_CONFIG" > "$DOCKSPIRE_BASELINE_FIXTURE/config-path"
    printf '{"auths":{}}\n' > "$DOCKER_CONFIG/config.json"
    ;;
  pull) [[ $# == 4 && "$2" == --platform && "$3" == linux/amd64 && "$4" == "$reference" ]] ;;
  image)
    [[ $# == 5 && "$2" == inspect && "$3" == --format && "$5" == "$reference" ]]
    if [[ "$4" == '{{json .RepoDigests}}' ]]; then cat "$DOCKSPIRE_BASELINE_FIXTURE/digests.json"
    else cat "$DOCKSPIRE_BASELINE_FIXTURE/image.json"; fi
    ;;
  logout) [[ $# == 2 && "$2" == ghcr.io ]] ;;
  *) exit 92 ;;
esac
DOCKER
chmod 700 "$scratch/bin/gh" "$scratch/bin/docker"
reset_fixture() {
  rm -f -- "$scratch/calls" "$scratch/config-path" "$scratch/api-fail"
  jq -nc --arg visibility "$1" '{name:"dockspire",package_type:"container",visibility:$visibility,repository:{full_name:"Mayo-Nas/dockspire"}}' > "$scratch/package.json"
  jq -nc '[ [{name:"sha256:265b222a8993679dc6920e5e06b7184b57159571f68a6cd1de66047e05c0f639",metadata:{container:{tags:["v0.2.0"]}}}] ]' > "$scratch/versions.json"
  jq -nc --arg reference "$reference" '[$reference]' > "$scratch/digests.json"
  jq -nc '{os:"linux",arch:"amd64",source:"https://github.com/Mayo-Nas/dockspire",version:"v0.2.0",revision:"16fe9995ed5f7b7896dec78cf8de90c314ab9147"}' > "$scratch/image.json"
}
change_fixture() {
  jq "$2" "$scratch/$1.json" > "$scratch/next.json"
  mv -- "$scratch/next.json" "$scratch/$1.json"
}
exercise() {
  local expected=$1 stage=$2 visibility=${3:-private} result=0 auth_dir
  env GITHUB_REPOSITORY=Mayo-Nas/dockspire GITHUB_EVENT_NAME=workflow_dispatch \
    GITHUB_ACTOR=Mayo-Nas GITHUB_TRIGGERING_ACTOR=Mayo-Nas GITHUB_REF_TYPE=branch \
    GITHUB_REF=refs/heads/reviewed DOCKSPIRE_REVIEWED_REF=refs/heads/reviewed \
    GITHUB_SHA="$head" DOCKSPIRE_REVIEWED_COMMIT="$head" \
    GH_TOKEN=fictional-policy-token RUNNER_TEMP="$scratch" \
    DOCKSPIRE_BASELINE_PACKAGE_VISIBILITY="$visibility" BASH_ENV= ENV= \
    bash "$root/scripts/pull-upgrade-baseline.sh" >/dev/null 2>&1 || result=$?
  if [[ "$expected" == allow ]]; then [[ "$result" == 0 ]]; else [[ "$result" != 0 ]]; fi
  if [[ "$stage" == before ]]; then
    [[ ! -e "$scratch/calls" && ! -e "$scratch/config-path" ]]
  else
    [[ -f "$scratch/config-path" && $(tail -n 1 "$scratch/calls") == logout ]]
    auth_dir=$(cat "$scratch/config-path")
    [[ "$auth_dir" == "$scratch"/dockspire-upgrade-registry.* && ! -e "$auth_dir" ]]
  fi
}
for visibility in private public; do reset_fixture "$visibility"; exercise allow after "$visibility"; done
reset_fixture private; change_fixture package '.repository=null'; exercise allow after
for expression in '.visibility="public"' '.package_type="npm"' '.name="other"' '.repository.full_name="other/repository"'; do
  reset_fixture private; change_fixture package "$expression"; exercise deny before
done
for expression in '.[0][0].name="sha256:0000000000000000000000000000000000000000000000000000000000000000"' '.[0][0].metadata.container.tags=["v0.1.1"]' '. as $all | [$all[0]+[$all[0][0]]]'; do
  reset_fixture private; change_fixture versions "$expression"; exercise deny before
done
reset_fixture private; touch "$scratch/api-fail"; exercise deny before
reset_fixture private; exercise deny before internal
reset_fixture private; change_fixture digests '[]'; exercise deny after
for expression in '.os="windows"' '.arch="arm64"' '.source="https://example.test/other"' '.version="v0.1.1"' '.revision="0000000000000000000000000000000000000000"' 'del(.source)' 'del(.version)' 'del(.revision)'; do
  reset_fixture private; change_fixture image "$expression"; exercise deny after
done
printf '%s\n' 'Upgrade baseline metadata: private/public identity, pre-login rejection and credential cleanup fixtures passed'
