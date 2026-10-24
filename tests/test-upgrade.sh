#!/usr/bin/env bash
# Synthetic data only, no Docker socket. Old -> candidate -> restored-old -> preserved-candidate.
set -Eeuo pipefail
# Emit fixture coordinates only: commands, payloads and authentication stay private.
emit_probe_lines() {
 local line metadata_pattern credentials_pattern security_pattern exec_pattern
 metadata_pattern='^probe label=(old|candidate|rollback) target=(data|admin) type=(regular|directory|symlink|other|unknown) uid=-?[0-9]+ gid=-?[0-9]+ mode=[0-7]{4} stat_errno=-?[0-9]+$'
 credentials_pattern='^probe label=(old|candidate|rollback) uid_real=-?[0-9]+ uid_effective=-?[0-9]+ uid_saved=-?[0-9]+ uid_fs=-?[0-9]+ gid_real=-?[0-9]+ gid_effective=-?[0-9]+ gid_saved=-?[0-9]+ gid_fs=-?[0-9]+ groups=(none|unknown|[0-9]+(,[0-9]+){0,127}) groups_count=-?[0-9]+ groups_complete=(0|1) groups_truncated=(0|1) parent_group_match=(-1|0|1) admin_group_match=(-1|0|1)$'
 security_pattern='^probe label=(old|candidate|rollback) cap_eff=([0-9a-f]{16}|unknown) cap_bnd=([0-9a-f]{16}|unknown) no_new_privs=(-1|0|1) status_complete=(0|1) open_errno=-?[0-9]+$'
 exec_pattern='^probe exec_errno=-?[0-9]+$'
 while IFS= read -r line; do
  if [[ "$line" =~ $metadata_pattern || "$line" =~ $credentials_pattern || "$line" =~ $security_pattern || "$line" =~ $exec_pattern ]]; then printf '%s\n' "$line"; fi
 done <<< "$1"
}
fixture_failure() {
 local line=$1 status=$2 identity='' state=unknown code=unknown oom=false logs='' category=unclassified
 trap - ERR
 printf 'Upgrade fixture failed: line=%s scenario=%s phase=%s\n' "$line" "${scenario:-preflight}" "${phase:-preflight}" >&2
 if [[ "${container_created:-false}" == true && -n "${name:-}" && -n "${running_image:-}" ]]; then
  identity=$(timeout --kill-after=1s 3s docker inspect --format '{{index .Config.Labels "io.dockspire.upgrade-test"}} {{.Image}}' "$name" 2>/dev/null) || identity=''
  if [[ "$identity" == "$owner $running_image" ]]; then
   identity=$(timeout --kill-after=1s 3s docker inspect --format '{{.State.Status}} {{.State.ExitCode}} {{.State.OOMKilled}}' "$name" 2>/dev/null) || identity=''
   read -r state code oom <<< "$identity" || true
   case "$state" in created|running|paused|restarting|removing|exited|dead) ;; *) state=unknown ;; esac
   [[ "$code" =~ ^[0-9]+$ ]] || code=unknown
   # Logs remain in bounded memory. Never print raw startup text or values.
   logs=$(timeout --kill-after=1s 3s docker logs --tail 20 "$name" 2>&1 | head -c 65536) || true
   if [[ -n "${probe:-}" ]]; then emit_probe_lines "$logs"; fi
   if [[ "$oom" == true ]]; then category=memory-limit; else
    case "$logs" in
     *'Administrator file missing or unsafe;'*) category=admin-file-safety ;;
     *'Invalid administrator file; refusing to start'*) category=admin-format ;;
     *'Choose a real private data directory'*) category=data-directory ;;
     *'Cannot open administrator file'*) category=admin-open ;;
     *'permission denied'*) category=filesystem-permission ;;
     *'read-only file system'*) category=filesystem-readonly ;;
     *'address already in use'*) category=listen-address ;;
     *TLS*|*tls*) category=tls-startup ;;
    esac
   fi
   printf 'Upgrade fixture container: state=%s exit=%s category=%s\n' "$state" "$code" "$category" >&2
  fi
 fi
 exit "$status"
}
trap 'fixture_failure "$LINENO" "$?"' ERR
old_image=${1:?Usage: test-upgrade.sh OLD_IMAGE_ID NEW_IMAGE_ID NEW_VERSION}
new_image=${2:?Missing candidate image ID}
new_version=${3:?Missing candidate version}
[[ "$old_image" =~ ^sha256:[0-9a-f]{64}$ && "$new_image" =~ ^sha256:[0-9a-f]{64}$ ]]
[[ "$new_version" == dev || "$new_version" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]
for image in "$old_image" "$new_image"; do
 [[ $(docker image inspect --format '{{.Os}}/{{.Architecture}}' "$image") == linux/amd64 ]]
done
probe=${DOCKSPIRE_UPGRADE_PROBE:-}
probe_mount=()
if [[ -n "$probe" ]]; then
 [[ "$probe" == /* && "$probe" != *,* && -f "$probe" && -x "$probe" && ! -L "$probe" && $(realpath "$probe") == "$probe" ]]
 probe_mount=(--mount "type=bind,src=$probe,dst=/upgrade-probe,readonly")
fi
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
probe_context() {
 local context
 [[ $(timeout --kill-after=1s 3s docker inspect --format '{{index .Config.Labels "io.dockspire.upgrade-test"}}' "$name" 2>/dev/null | head -c 512) == "$owner" ]]
 [[ $(timeout --kill-after=1s 3s docker inspect --format '{{.Image}}' "$name" 2>/dev/null | head -c 512) == "$running_image" ]]
 context=$(timeout --kill-after=1s 3s docker inspect --format '{"user":{{json .Config.User}},"drop":{{json .HostConfig.CapDrop}},"opts":{{json .HostConfig.SecurityOpt}},"profile":{{json .AppArmorProfile}}}' "$name" 2>/dev/null | head -c 65536)
 jq -er --arg phase "$phase" '
  (if .user == "" then "default-root" elif (.user | test("^[0-9]+(:[0-9]+)?$")) then .user elif .user == "root" or .user == "root:root" then "root" else "other" end) as $user |
  (if ((.drop // []) | index("ALL")) != null then "all" else "other" end) as $drop |
  ((.opts // []) | any(. == "no-new-privileges:true" or . == "no-new-privileges")) as $nnp |
  (if .profile == "" then "none" elif .profile == "docker-default" then "docker-default" else "other" end) as $profile |
  (if ((.opts // []) | index("seccomp=unconfined")) != null then "unconfined" elif ((.opts // []) | any(startswith("seccomp="))) then "other" else "default" end) as $seccomp |
  "Upgrade probe context: phase=\($phase) user=\($user) cap_drop=\($drop) no_new_privs=\($nnp) apparmor=\($profile) seccomp=\($seccomp)"
 ' <<< "$context" 2>/dev/null
}
checkpoint_metadata() {
 local target fields uid gid mode rawmode
 for target in data admin; do
  local path="$checkpoint"
  if [[ "$target" == admin ]]; then path="$checkpoint/admin.json"; fi
  fields=$(stat -c '%u %g %a %f' "$path" 2>/dev/null) || fields=''
  read -r uid gid mode rawmode <<< "$fields" || true
  if [[ "$uid" =~ ^[0-9]+$ && "$gid" =~ ^[0-9]+$ && "$mode" =~ ^[0-7]{3,4}$ && "$rawmode" =~ ^[0-9a-f]+$ ]]; then
   printf 'Upgrade probe checkpoint: target=%s uid=%s gid=%s mode=%s mode_hex=%s\n' "$target" "$uid" "$gid" "$mode" "$rawmode"
  else printf 'Upgrade probe checkpoint: target=%s metadata=unavailable\n' "$target"; fi
 done
}
# Report only fixed schema names/counts; raw metadata values stay private.
history_metadata_difference() {
 if [[ ${#1} -gt 65536 || ${#2} -gt 65536 ]]; then
  printf '%s\n' 'Upgrade history comparison: metadata_over_limit=true'
  return
 fi
 jq -nr --argjson expected "$1" --argjson actual "$2" '
  ($expected.revisions | sort_by(.revision)) as $before |
  ($actual.revisions | sort_by(.revision)) as $after |
  (($before | map(.revision)) == ($after | map(.revision))) as $ids |
  "Upgrade history comparison: count_equal=\(($before|length)==($after|length)) revision_ids_equal=\($ids) order_equal=\(($expected.revisions|map(.revision))==($actual.revisions|map(.revision))) next_cursor_equal=\($expected.nextCursor==$actual.nextCursor) schema_equal=\(($expected|keys)==($actual|keys) and ($before|map(keys))==($after|map(keys)))",
  (if $ids then
   ["storedAt","initial","selected","applied","recovery"][] as $field |
   [range(0; $before|length) | select($before[.][$field] != $after[.][$field])] as $changed |
   select(($changed|length)>0) |
   "Upgrade history field: field=\($field) changed=\($changed|length)" +
   (if $field=="storedAt" then
    " whole_seconds_equal=\(all($changed[]; . as $i |
      ($before[$i].storedAt|type)=="string" and ($after[$i].storedAt|type)=="string" and
      ($before[$i].storedAt|sub("\\.[0-9]+Z$";"Z"))==($after[$i].storedAt|sub("\\.[0-9]+Z$";"Z"))))"
    else "" end)
  else empty end)
 ' 2>/dev/null || printf '%s\n' 'Upgrade history comparison: diagnostic_unavailable=true'
}
# End history comparison diagnostic.
# Docker exports directory mtimes at whole-second precision. Derive rollback
# expectations from the extracted original archive, never from the actual API.
rollback_expected_history() {
 local stack=$1 baseline=$2 ids actual_ids='' path revision stamp times='{}'
 [[ "$stack" =~ ^[a-z0-9][a-z0-9_-]{0,62}$ && ${#baseline} -le 65536 ]] || return 1
 ids=$(jq -er '
  .revisions as $rows |
  if ($rows|type)!="array" or ($rows|length)<2 or
     ([$rows[]|select(.initial==true)]|length)!=1 or
     ([$rows[].revision]|unique|length)!=($rows|length) or
     any($rows[]; (.revision|test("^[0-9a-f]{64}$"))|not)
  then error("Invalid fixture history") else
   $rows[]|select(.initial!=true)|.revision
  end' <<< "$baseline" 2>/dev/null) || return 1
 [[ -d "$checkpoint/stacks/$stack/revisions" && ! -L "$checkpoint/stacks/$stack/revisions" ]] || return 1
 for path in "$checkpoint/stacks/$stack/revisions"/*; do
  [[ -e "$path" ]] || continue
  revision=${path##*/}
  [[ -d "$path" && ! -L "$path" && "$revision" =~ ^[0-9a-f]{64}$ ]] || return 1
  actual_ids+="$revision"$'\n'
 done
 [[ $(printf '%s\n' "$ids" | LC_ALL=C sort) == "$(printf '%s' "$actual_ids" | LC_ALL=C sort)" ]] || return 1
 while IFS= read -r revision; do
  stamp=$(date -u --reference="$checkpoint/stacks/$stack/revisions/$revision" +'%Y-%m-%dT%H:%M:%S.%NZ') || return 1
  [[ "$stamp" == *.000000000Z ]] || return 1
  stamp="${stamp%.000000000Z}Z"
  times=$(jq -nc --argjson times "$times" --arg revision "$revision" --arg stamp "$stamp" '$times+{($revision):$stamp}') || return 1
 done <<< "$ids"
 jq -Sc --argjson times "$times" '
  def timekey: .storedAt |
   capture("^(?<whole>[0-9]{4}-[0-9]{2}-[0-9]{2}T[0-9]{2}:[0-9]{2}:[0-9]{2})(?:\\.(?<fraction>[0-9]{1,9}))?Z$") |
   .whole+"."+(((.fraction//"")+"000000000")[0:9]);
  .revisions |= (map(
   if .initial==true then . else
    if $times[.revision] != (.storedAt|sub("\\.[0-9]+Z$";"Z")) then error("Archive time differs beyond transport precision")
    else .storedAt=$times[.revision] end
   end) | sort_by([timekey,.revision]) | reverse)
 ' <<< "$baseline" 2>/dev/null
}
# End rollback history expectation.
# Preserve the stopped source archive; the host view is never the restore source.
checkpoint_container_owned() {
 [[ "$container_created" == true ]] || return 1
 [[ $(docker inspect --format '{{index .Config.Labels "io.dockspire.upgrade-test"}}' "$name") == "$owner" ]] || return 1
 [[ $(docker inspect --format '{{.Image}}' "$name") == "$running_image" ]] || return 1
}
validate_checkpoint_archive() {
 local archive=$1 entries types entry relative has_admin=false
 entries=$(tar --list --absolute-names --quoting-style=escape --file "$archive" 2>/dev/null) || return 1
 types=$(tar --list --verbose --numeric-owner --absolute-names --quoting-style=escape --file "$archive" 2>/dev/null) || return 1
 while IFS= read -r entry; do
  [[ "${entry:0:1}" == - || "${entry:0:1}" == d ]] || return 1
 done <<< "$types"
 while IFS= read -r entry; do
  if [[ "$entry" == . || "$entry" == ./ ]]; then continue; fi
  relative=${entry#./}
  relative=${relative%/}
  [[ "$relative" =~ ^[A-Za-z0-9_.-]+(/[A-Za-z0-9_.-]+)*$ && ${#relative} -le 512 ]] || return 1
  case "/$relative/" in *"/../"*|*"/./"*|*"//"*) return 1 ;; esac
  if [[ "$relative" == admin.json ]]; then has_admin=true; fi
 done <<< "$entries"
 "$has_admin" || return 1
}
save_checkpoint_archive() {
 checkpoint_container_owned || return 1
 (umask 077; docker cp "$name:/appdata/." - 2>/dev/null | head -c 33554433 > "$checkpoint_archive") || return 1
 [[ $(stat -c '%s' "$checkpoint_archive") -le 33554432 ]] || return 1
 validate_checkpoint_archive "$checkpoint_archive" || return 1
 (umask 077; tar --extract --file "$checkpoint_archive" --directory "$checkpoint" --no-same-owner --no-same-permissions 2>/dev/null) || return 1
}
restore_checkpoint_archive() {
 checkpoint_container_owned || return 1
 # stdin is the original relative-content tar with its original numeric IDs/modes.
 # Do not re-tar the host view or use -a (container-user ownership override).
 docker cp - "$name:/appdata" < "$checkpoint_archive" >/dev/null || return 1
}
# End checkpoint archive helpers.
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
 checkpoint_archive="$directory/$scenario-checkpoint.tar"
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
 rollback_histories=()
 candidate_records=()
 candidate_histories=()
 fingerprint=''
 for phase in old candidate rollback candidate-preserved; do
  printf 'Upgrade fixture: scenario=%s phase=%s\n' "$scenario" "$phase"
  running_image=$old_image
  expected_version=v0.2.0
  active_volume=$volume
  if [[ "$phase" == candidate || "$phase" == candidate-preserved ]]; then running_image=$new_image; expected_version=$new_version; fi
  if [[ "$phase" == rollback ]]; then
   docker volume create --label "io.dockspire.upgrade-test=$owner" "$rollback_volume" >/dev/null
   rollback_created=true
   [[ $(docker volume inspect --format '{{index .Labels "io.dockspire.upgrade-test"}}' "$rollback_volume") == "$owner" ]]
   active_volume=$rollback_volume
  fi
  [[ -z $(docker ps -aq --filter "name=^/$name$") ]]
  entrypoint=()
  command=(--demo --setup --listen ":$port_in" --data-dir /appdata "${transport[@]}")
  if [[ -n "$probe" && "$phase" == rollback ]]; then
   entrypoint=(--entrypoint /upgrade-probe)
   command=(--exec rollback -- "${command[@]}")
  fi
  docker create --pull never --name "$name" --label "io.dockspire.upgrade-test=$owner" \
   --read-only --cap-drop ALL --security-opt no-new-privileges:true \
   --memory 256m --cpus 0.5 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=64m \
   -p "127.0.0.1::$port_in" -v "$active_volume:/appdata" "${probe_mount[@]}" "${entrypoint[@]}" "$running_image" \
   "${command[@]}" >/dev/null
  container_created=true
  if [[ -n "$probe" ]]; then probe_context; fi
  if [[ "$phase" == rollback ]]; then restore_checkpoint_archive; fi
  docker start "$name" >/dev/null
  port=$(docker port "$name" "$port_in/tcp" | sed -n 's/^127\.0\.0\.1://p')
  [[ "$port" =~ ^[0-9]+$ ]]
  base="$scheme://127.0.0.1:$port"
  for attempt in $(seq 1 45); do
   if curl "${curl_args[@]}" -fsS "$base/api/health" >/dev/null 2>&1; then break; fi
   sleep 1
  done
  curl "${curl_args[@]}" -fsS "$base/api/health" >/dev/null
  if [[ -n "$probe" && "$phase" == rollback ]]; then
   probe_logs=$(timeout --kill-after=1s 3s docker logs --tail 20 "$name" 2>&1 | head -c 65536) || true
   emit_probe_lines "$probe_logs"
  fi
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
    # The released baseline already uses presence-aware identity: retain every field.
    [[ "$record" == "${baseline_records[$index]}" ]]
    if [[ "$phase" == candidate ]]; then
     [[ $(jq -r '.fingerprint' <<< "$record") == $(expected_exact_token "${stack_names[$index]}") ]]
    fi
    expected_history=${baseline_histories[$index]}
    if [[ "$phase" == rollback ]]; then expected_history=${rollback_histories[$index]}; fi
    current_history=$(api "$base/api/stacks/${stack_names[$index]}/revisions" | jq -Sc .)
    if [[ "$current_history" != "$expected_history" ]]; then
     history_metadata_difference "$expected_history" "$current_history"
    fi
    [[ "$current_history" == "$expected_history" ]]
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
  if [[ -n "$probe" && ( "$phase" == old || "$phase" == candidate ) ]]; then
   timeout --kill-after=1s 5s docker exec "$name" /upgrade-probe --inspect-only "$phase" 2>/dev/null
  fi
  if [[ "$phase" == old ]]; then
   docker stop --time 10 "$name" >/dev/null
   save_checkpoint_archive
   for index in "${!baseline_histories[@]}"; do
    rollback_histories+=("$(rollback_expected_history "${stack_names[$index]}" "${baseline_histories[$index]}")")
   done
   if [[ -n "$probe" ]]; then checkpoint_metadata; fi
  fi
  remove_container
 done
 cleanup_resources
 echo "Upgrade/backup-restore rollback $scenario: saved metadata/history, new writes, auth/TLS and preserved candidate data passed"
done
