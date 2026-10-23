#!/usr/bin/env bash
# Synthetic data only, no Docker socket. Old -> candidate -> restored-old -> preserved-candidate.
set -Eeuo pipefail
# Emit fixture coordinates only: commands, payloads and authentication stay private.
trap 'printf "Upgrade fixture failed: line=%s scenario=%s phase=%s\n" "$LINENO" "${scenario:-preflight}" "${phase:-preflight}" >&2' ERR
old_image=${1:?Usage: test-upgrade.sh OLD_IMAGE_ID NEW_IMAGE_ID NEW_VERSION}
new_image=${2:?Missing candidate image ID}
new_version=${3:?Missing candidate version}
[[ "$old_image" =~ ^sha256:[0-9a-f]{64}$ && "$new_image" =~ ^sha256:[0-9a-f]{64}$ ]]
[[ "$new_version" == dev || "$new_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]
for image in "$old_image" "$new_image"; do
 [[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image") == linux/amd64 ]]
done
owner="dockspire-upgrade-$(date +%s)-$$-$RANDOM"
directory=$(mktemp -d "${TMPDIR:-/tmp}/dockspire-upgrade.XXXXXX")
container_created=false
volume_created=false
rollback_created=false
remove_container() {
 if $container_created; then
  [[ $(docker inspect --format '{{index .Config.Labels "io.dockspire.upgrade-test"}}' "$name") == "$owner" ]] || return 1
  [[ $(docker inspect --format '{{.Image}}' "$name") == "$running_image" ]] || return 1
  docker rm -f "$name" >/dev/null || return 1
  container_created=false
 fi
}
cleanup_resources() {
 remove_container || return 1
 if $rollback_created; then
  [[ $(docker volume inspect --format '{{index .Labels "io.dockspire.upgrade-test"}}' "$rollback_volume") == "$owner" ]] || return 1
  docker volume rm "$rollback_volume" >/dev/null || return 1
  rollback_created=false
 fi
 if $volume_created; then
  [[ $(docker volume inspect --format '{{index .Labels "io.dockspire.upgrade-test"}}' "$volume") == "$owner" ]] || return 1
  docker volume rm "$volume" >/dev/null || return 1
  volume_created=false
 fi
}
cleanup() {
 cleanup_resources || return
 [[ "$directory" == "${TMPDIR:-/tmp}"/dockspire-upgrade.* && -d "$directory" && ! -L "$directory" ]] || return 1
 rm -rf -- "$directory"
}
trap cleanup EXIT
curl() { command curl --connect-timeout 3 --max-time 25 "$@"; }
# Fixture contents are ASCII and both environment files exist. Encode exactly
# the new Go JSON/base64 identity, using the stopped pre-upgrade snapshot bytes.
expected_exact_token() {
 local stack=$1 selected path material
 path="$checkpoint/stacks/$stack"
 selected=$(jq -r '.configRevision // ""' "$path/record.json")
 if [[ -n "$selected" ]]; then
  [[ "$selected" =~ ^[0-9a-f]{64}$ ]]
  path="$path/revisions/$selected"
 fi
 material=$(jq -nc --rawfile compose "$path/compose.yaml" --rawfile env "$path/.env" --rawfile stackenv "$path/stack.env" '
  ["dockspire-files-v2",["compose.yaml",true,($compose|@base64)],[".env",true,($env|@base64)],["stack.env",true,($stackenv|@base64)]]')
 printf '%s' "$material" | sha256sum | awk '{print $1}'
}
for scenario in http https http-passwordless https-passwordless; do
 name="$owner-$scenario"
 volume="$name-data"
 rollback_volume="$name-rollback-data"
 checkpoint="$directory/$scenario-checkpoint"
 mkdir "$checkpoint"
 scheme=${scenario%-passwordless}
 passwordless=false
 password='fictional-upgrade-password'
 if [[ "$scenario" == *-passwordless ]]; then passwordless=true; password=''; fi
 port_in=8787
 transport=(--allow-http)
 curl_args=()
 if [[ "$scheme" == https ]]; then port_in=9443; transport=(--https --tls-hosts 127.0.0.1); curl_args=(-k); fi
 [[ -z $(docker ps -aq --filter "name=^/$name$") ]]
 ! docker volume inspect "$volume" >/dev/null 2>&1
 ! docker volume inspect "$rollback_volume" >/dev/null 2>&1
 docker volume create --label "io.dockspire.upgrade-test=$owner" "$volume" >/dev/null
 volume_created=true
 [[ $(docker volume inspect --format '{{index .Labels "io.dockspire.upgrade-test"}}' "$volume") == "$owner" ]]
 stack_names=()
 baseline_records=()
 baseline_histories=()
 candidate_records=()
 candidate_histories=()
 fingerprint=''
 for phase in old candidate rollback candidate-preserved; do
  printf 'Upgrade fixture: scenario=%s phase=%s\n' "$scenario" "$phase"
  running_image=$old_image
  expected_version=v0.1.1
  active_volume=$volume
  if [[ "$phase" == candidate || "$phase" == candidate-preserved ]]; then running_image=$new_image; expected_version=$new_version; fi
  if [[ "$phase" == rollback ]]; then
   docker volume create --label "io.dockspire.upgrade-test=$owner" "$rollback_volume" >/dev/null
   rollback_created=true
   [[ $(docker volume inspect --format '{{index .Labels "io.dockspire.upgrade-test"}}' "$rollback_volume") == "$owner" ]]
   active_volume=$rollback_volume
  fi
  [[ -z $(docker ps -aq --filter "name=^/$name$") ]]
  docker create --pull never --name "$name" --label "io.dockspire.upgrade-test=$owner" \
   --read-only --cap-drop ALL --security-opt no-new-privileges:true \
   --memory 256m --cpus 0.5 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=64m \
   -p "127.0.0.1::$port_in" -v "$active_volume:/appdata" "$running_image" \
   --demo --setup --listen ":$port_in" --data-dir /appdata "${transport[@]}" >/dev/null
  container_created=true
  if [[ "$phase" == rollback ]]; then docker cp "$checkpoint/." "$name:/appdata"; fi
  docker start "$name" >/dev/null
  port=$(docker port "$name" "$port_in/tcp" | sed -n 's/^127\.0\.0\.1://p')
  [[ "$port" =~ ^[0-9]+$ ]]
  base="$scheme://127.0.0.1:$port"
  for attempt in $(seq 1 45); do
   if curl "${curl_args[@]}" -fsS "$base/api/health" >/dev/null 2>&1; then break; fi
   sleep 1
  done
  curl "${curl_args[@]}" -fsS "$base/api/health" >/dev/null
  if [[ "$scheme" == https ]]; then
   current_fingerprint=$(docker exec "$name" /stacklane --tls-fingerprint --data-dir /appdata)
   [[ "$current_fingerprint" == 'SHA256 Fingerprint='* ]]
   if [[ -z "$fingerprint" ]]; then fingerprint=$current_fingerprint; else [[ "$current_fingerprint" == "$fingerprint" ]]; fi
  fi
  payload=$(jq -nc --arg password "$password" '{password:$password,confirmation:$password}')
  if [[ "$phase" == old ]]; then
   [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 201 ]]
  fi
  curl "${curl_args[@]}" -fsS "$base/api/auth/setup" | jq -e '.required == false' >/dev/null
  [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 409 ]]
  if $passwordless; then
   headers=$(curl "${curl_args[@]}" -fsS -D - "$base/api/session")
  else
   [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' "$base/api/session") == 401 ]]
   login=$(jq -nc --arg password "$password" '{username:"admin",password:$password}')
   headers=$(curl "${curl_args[@]}" -fsS -D - -o /dev/null -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$login" "$base/api/auth/login")
  fi
  cookie=$(printf '%s\n' "$headers" | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\);.*/\1/p' | tr -d '\r')
  [[ -n "$cookie" ]]
  session=$(curl "${curl_args[@]}" -fsS -H "Cookie: $cookie" "$base/api/session")
  jq -e --arg version "$expected_version" --argjson passwordless "$passwordless" '.version == $version and .passwordless == $passwordless and .deployEnabled == false' <<< "$session" >/dev/null
  csrf=$(jq -er '.csrfToken | select(length > 0)' <<< "$session")
  api() { curl "${curl_args[@]}" -fsS -H "Cookie: $cookie" -H "Origin: $base" -H "X-Stacklane-CSRF: $csrf" -H 'Content-Type: application/json' "$@"; }
  if [[ "$phase" == candidate || "$phase" == rollback ]]; then
   for index in "${!baseline_records[@]}"; do
    record=$(api "$base/api/stacks/${stack_names[$index]}" | jq -Sc .)
    if [[ "$phase" == candidate ]]; then
     # Only the optimistic editor token changes. All stored metadata and IDs remain exact.
     [[ $(jq -Sc 'del(.fingerprint)' <<< "$record") == "$(jq -Sc 'del(.fingerprint)' <<< "${baseline_records[$index]}")" ]]
     [[ $(jq -r '.fingerprint' <<< "$record") == $(expected_exact_token "${stack_names[$index]}") ]]
    else
     [[ "$record" == "${baseline_records[$index]}" ]]
    fi
    [[ $(api "$base/api/stacks/${stack_names[$index]}/revisions" | jq -Sc .) == "${baseline_histories[$index]}" ]]
   done
   if [[ "$phase" == rollback ]]; then
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" "$base/api/stacks/upgrade-candidate") == 400 ]]
    missing=$(curl "${curl_args[@]}" -sS -H "Cookie: $cookie" "$base/api/stacks/upgrade-candidate")
    jq -e '.error == "Saved stack not found"' <<< "$missing" >/dev/null
   fi
  elif [[ "$phase" == candidate-preserved ]]; then
   for index in "${!candidate_records[@]}"; do
    [[ $(api "$base/api/stacks/${stack_names[$index]}" | jq -Sc .) == "${candidate_records[$index]}" ]]
    [[ $(api "$base/api/stacks/${stack_names[$index]}/revisions" | jq -Sc .) == "${candidate_histories[$index]}" ]]
   done
  fi
  if [[ "$phase" == old || "$phase" == candidate ]]; then
   stack="upgrade-$phase"
   compose=$(printf 'services:\n  fixture:\n    image: busybox:1.37\n    labels:\n      io.dockspire.fixture: %s\n' "$phase")
   draft=$(jq -nc --arg name "$stack" --arg compose "$compose" '{name:$name,compose:$compose}')
   api --data-binary "$draft" "$base/api/stacks" >/dev/null
   record=$(api "$base/api/stacks/$stack" | jq -Sc .)
   revision=$(jq -er '.fingerprint' <<< "$record")
   edited="$compose"$'\n# preserved revision'
   draft=$(jq -nc --arg compose "$edited" --arg revision "$revision" '{compose:$compose,revision:$revision}')
   api --data-binary "$draft" "$base/api/stacks/$stack/edit" >/dev/null
   record=$(api "$base/api/stacks/$stack" | jq -Sc .)
   jq -e --arg compose "$edited" '.compose == $compose and .origin == "new"' <<< "$record" >/dev/null
   history=$(api "$base/api/stacks/$stack/revisions" | jq -Sc .)
   jq -e '.revisions | length == 2' <<< "$history" >/dev/null
   stack_names+=("$stack")
   if [[ "$phase" == old ]]; then baseline_records+=("$record"); baseline_histories+=("$history"); fi
  fi
  if [[ "$phase" == candidate ]]; then
   for stack in "${stack_names[@]}"; do
    candidate_records+=("$(api "$base/api/stacks/$stack" | jq -Sc .)")
    candidate_histories+=("$(api "$base/api/stacks/$stack/revisions" | jq -Sc .)")
   done
  fi
  if [[ "$phase" == old ]]; then
   docker stop --time 10 "$name" >/dev/null
   docker cp "$name:/appdata/." "$checkpoint"
  fi
  remove_container
 done
 cleanup_resources
 echo "Upgrade/backup-restore rollback $scenario: legacy metadata/history, modern writes, auth/TLS and preserved candidate data passed"
done
