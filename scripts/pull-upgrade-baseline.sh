#!/usr/bin/env bash
# Trusted hosted job only. Pull an immutable private baseline, never a mutable tag.
set -euo pipefail
: "${GH_TOKEN:?Hosted package-read token required}"
: "${GITHUB_ACTOR:?Hosted actor required}"
: "${RUNNER_TEMP:?Hosted temporary directory required}"
[[ "${GITHUB_REPOSITORY:-}" == Mayo-Nas/dockspire ]]
previous_digest=sha256:4fd7ba7c829c48087d6f76883d7c553c33a4e46737d44583580d5716b2b4303c
package=users/Mayo-Nas/packages/container/dockspire
gh api "$package" | jq -e '.visibility == "private" and .package_type == "container" and .name == "dockspire" and (.repository == null or .repository.full_name == "Mayo-Nas/dockspire")' >/dev/null
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
