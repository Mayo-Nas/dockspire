#!/usr/bin/env bash
# No GitHub/Docker access: fake gh plus real jq test all destination combinations.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
directory=$(mktemp -d "${TMPDIR:-/tmp}/dockspire-release-policy.XXXXXX")
cleanup() {
 [[ "$directory" == "${TMPDIR:-/tmp}"/dockspire-release-policy.* && -d "$directory" && ! -L "$directory" ]] || return 1
 rm -rf -- "$directory"
}
trap cleanup EXIT
mkdir "$directory/bin"
cat > "$directory/bin/gh" <<'GH'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == api ]]
[[ "${DOCKSPIRE_METADATA_FAIL:-0}" != 1 ]]
case "$2" in
 repos/Mayo-Nas/dockspire) cat "$DOCKSPIRE_RELEASE_FIXTURE/repository.json" ;;
 users/Mayo-Nas/packages/container/dockspire|orgs/Mayo-Nas/packages/container/dockspire) cat "$DOCKSPIRE_RELEASE_FIXTURE/package.json" ;;
 *) exit 1 ;;
esac
GH
chmod 700 "$directory/bin/gh"
export DOCKSPIRE_RELEASE_FIXTURE="$directory" PATH="$directory/bin:$PATH"
export GITHUB_REPOSITORY=Mayo-Nas/dockspire GITHUB_REF=refs/heads/main
metadata() {
 jq -nc --arg visibility "$1" --argjson fork "$3" '{full_name:"Mayo-Nas/dockspire",fork:$fork,default_branch:"main",visibility:$visibility,private:($visibility=="private"),owner:{type:"User"}}' > "$directory/repository.json"
 jq -nc --arg visibility "$2" '{name:"dockspire",package_type:"container",visibility:$visibility,repository:{full_name:"Mayo-Nas/dockspire"}}' > "$directory/package.json"
}
for repository in private public; do
 for package in private public; do
  metadata "$repository" "$package" false
  [[ $(bash "$root/scripts/check-release-destination.sh" "$repository" "$package") == users/Mayo-Nas/packages/container/dockspire ]]
  other=public; if [[ "$package" == public ]]; then other=private; fi
  if bash "$root/scripts/check-release-destination.sh" "$repository" "$other" >/dev/null 2>&1; then echo 'Package visibility mismatch accepted' >&2; exit 1; fi
  other=public; if [[ "$repository" == public ]]; then other=private; fi
  if bash "$root/scripts/check-release-destination.sh" "$other" "$package" >/dev/null 2>&1; then echo 'Repository visibility mismatch accepted' >&2; exit 1; fi
 done
done
metadata private private true
if bash "$root/scripts/check-release-destination.sh" private private >/dev/null 2>&1; then echo 'Fork accepted' >&2; exit 1; fi
metadata private private false
for repository in fork/dockspire Mayo-Nas/other; do
 if GITHUB_REPOSITORY="$repository" bash "$root/scripts/check-release-destination.sh" private private >/dev/null 2>&1; then echo 'Noncanonical repository accepted' >&2; exit 1; fi
done
if GITHUB_REF=refs/heads/feature bash "$root/scripts/check-release-destination.sh" private private >/dev/null 2>&1; then echo 'Non-main publication accepted' >&2; exit 1; fi
if bash "$root/scripts/check-release-destination.sh" internal private >/dev/null 2>&1; then echo 'Unsupported visibility accepted' >&2; exit 1; fi
metadata private private false
if DOCKSPIRE_METADATA_FAIL=1 bash "$root/scripts/check-release-destination.sh" private private >/dev/null 2>&1; then echo 'Metadata access failure accepted' >&2; exit 1; fi
jq '.repository.full_name="other/dockspire"' "$directory/package.json" > "$directory/wrong.json"
mv "$directory/wrong.json" "$directory/package.json"
if bash "$root/scripts/check-release-destination.sh" private private >/dev/null 2>&1; then echo 'Wrong linked repository accepted' >&2; exit 1; fi
metadata public public false
jq '.owner.type="Organization"' "$directory/repository.json" > "$directory/org.json"
mv "$directory/org.json" "$directory/repository.json"
[[ $(bash "$root/scripts/check-release-destination.sh" public public) == orgs/Mayo-Nas/packages/container/dockspire ]]
echo 'Release destination visibility/fork/main/metadata fixtures passed'
