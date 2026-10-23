#!/usr/bin/env bash
# Verify expectations only. This script never changes repository/package visibility.
set -euo pipefail
expected_repository=${1:?Expected repository visibility required}
expected_package=${2:?Expected package visibility required}
[[ "$expected_repository" == private || "$expected_repository" == public ]]
[[ "$expected_package" == private || "$expected_package" == public ]]
[[ "${GITHUB_REPOSITORY:-}" == Mayo-Nas/dockspire && "${GITHUB_REF:-}" == refs/heads/main ]]
repository_metadata=$(gh api repos/Mayo-Nas/dockspire)
jq -e --arg visibility "$expected_repository" '
 .full_name == "Mayo-Nas/dockspire" and .fork == false and .default_branch == "main" and
 .visibility == $visibility and .private == ($visibility == "private")
' <<< "$repository_metadata" >/dev/null
owner_type=$(jq -er '.owner.type' <<< "$repository_metadata")
case "$owner_type" in
 User) endpoint=users ;;
 Organization) endpoint=orgs ;;
 *) echo 'Unsupported repository owner type.' >&2; exit 1 ;;
esac
package_api="$endpoint/Mayo-Nas/packages/container/dockspire"
gh api "$package_api" | jq -e --arg visibility "$expected_package" '
 .visibility == $visibility and .package_type == "container" and .name == "dockspire" and
 (.repository == null or .repository.full_name == "Mayo-Nas/dockspire")
' >/dev/null
printf '%s\n' "$package_api"
