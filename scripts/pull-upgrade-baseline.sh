#!/usr/bin/env bash
# Canonical trusted-main or explicitly reviewed owner-dispatched feature only.
set -euo pipefail
baseline_authorized() {
 local reviewed_ref=${DOCKSPIRE_REVIEWED_REF:-} reviewed_head=${DOCKSPIRE_REVIEWED_COMMIT:-}
 [[ "${GITHUB_REPOSITORY:-}" == Mayo-Nas/dockspire ]] || return 1
 case "${GITHUB_EVENT_NAME:-}" in pull_request|pull_request_target) return 1 ;; esac
 if [[ "${GITHUB_REF:-}" == refs/heads/main ]]; then
  case "${GITHUB_EVENT_NAME:-}" in
   push|schedule) return 0 ;;
   workflow_dispatch) [[ "${GITHUB_ACTOR:-}" == Mayo-Nas && "${GITHUB_TRIGGERING_ACTOR:-}" == Mayo-Nas ]]; return ;;
   *) return 1 ;;
  esac
 fi
 [[ "${GITHUB_EVENT_NAME:-}" == workflow_dispatch && "${GITHUB_ACTOR:-}" == Mayo-Nas && "${GITHUB_TRIGGERING_ACTOR:-}" == Mayo-Nas && "${GITHUB_REF_TYPE:-}" == branch ]] || return 1
 [[ "$reviewed_ref" == refs/heads/* && "$reviewed_ref" != refs/heads/main && "$reviewed_ref" == "${GITHUB_REF:-}" ]] || return 1
 [[ "$reviewed_head" =~ ^[0-9a-f]{40}$ && "$reviewed_head" == "${GITHUB_SHA:-}" ]] || return 1
 [[ $(git rev-parse HEAD) == "$reviewed_head" ]] || return 1
}
baseline_authorized || exit 1
if [[ $# == 1 && "$1" == --check-trust ]]; then exit 0; fi
[[ $# == 0 ]]
: "${GH_TOKEN:?Hosted package-read token required}"
: "${GITHUB_ACTOR:?Hosted actor required}"
: "${RUNNER_TEMP:?Hosted temporary directory required}"
expected_visibility=${DOCKSPIRE_BASELINE_PACKAGE_VISIBILITY:-private}
[[ "$expected_visibility" == private || "$expected_visibility" == public ]]
previous_digest=sha256:4fd7ba7c829c48087d6f76883d7c553c33a4e46737d44583580d5716b2b4303c
package=users/Mayo-Nas/packages/container/dockspire
gh api "$package" | jq -e --arg visibility "$expected_visibility" '.visibility == $visibility and .package_type == "container" and .name == "dockspire" and (.repository == null or .repository.full_name == "Mayo-Nas/dockspire")' >/dev/null
gh api --paginate --slurp "$package/versions?per_page=100" | jq -e --arg digest "$previous_digest" '[.[][] | select(.metadata.container.tags | index("v0.1.1")) | .name] == [$digest]' >/dev/null
registry_config=$(mktemp -d "$RUNNER_TEMP/dockspire-upgrade-registry.XXXXXX")
export DOCKER_CONFIG="$registry_config"
cleanup() {
  docker logout ghcr.io >/dev/null 2>&1 || true
  [[ "$registry_config" == "$RUNNER_TEMP"/dockspire-upgrade-registry.* && -d "$registry_config" && ! -L "$registry_config" ]] || return 1
  rm -rf -- "$registry_config"
}
trap cleanup EXIT
printf '%s' "$GH_TOKEN" | docker login ghcr.io --username "$GITHUB_ACTOR" --password-stdin >/dev/null
docker pull --platform linux/amd64 "ghcr.io/mayo-nas/dockspire@$previous_digest"
docker image inspect --format '{{json .RepoDigests}}' "ghcr.io/mayo-nas/dockspire@$previous_digest" | jq -e --arg reference "ghcr.io/mayo-nas/dockspire@$previous_digest" 'index($reference) != null' >/dev/null
