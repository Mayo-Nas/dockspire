#!/usr/bin/env bash
# Disposable integration checks: only newly created test containers/volumes.
set -euo pipefail
trap 'printf "Container installation check failed at line %s\n" "$LINENO" >&2' ERR
image=${1:?Usage: bash tests/test-container.sh IMAGE}
expected_version=${2:-dev}
compose_file="$(cd -- "$(dirname "$0")/../deploy" && pwd)/compose.yaml"
(
  unset DOCKSPIRE_IMAGE DOCKSPIRE_EXTERNAL_URL DOCKSPIRE_BIND_ADDRESS DOCKSPIRE_HTTP_PORT
  [[ $(docker compose --env-file /dev/null -f "$compose_file" config --images) == ghcr.io/mayo-nas/dockspire:v0.2.0 ]]
  [[ $(DOCKSPIRE_IMAGE= docker compose --env-file /dev/null -f "$compose_file" config --images) == ghcr.io/mayo-nas/dockspire:v0.2.0 ]]
  [[ $(DOCKSPIRE_IMAGE="$image" docker compose --env-file /dev/null -f "$compose_file" config --images) == "$image" ]]
)
printf '%s\n' 'Compose template image selection: default, empty and explicit override passed'
bash "$(dirname "$0")/test-build-context.sh"
curl() { command curl --connect-timeout 3 --max-time 25 "$@"; }
created_volume=false
created_container=false
cleanup() {
  if $created_container; then docker rm -f "$name" >/dev/null; created_container=false; fi
  if $created_volume; then docker volume rm "$volume" >/dev/null; created_volume=false; fi
}
trap cleanup EXIT
for scenario in http https http-passwordless https-passwordless; do
  scheme=${scenario%-passwordless}
  passwordless=false
  if [[ "$scenario" == *-passwordless ]]; then passwordless=true; fi
  name="dockspire-install-test-$scenario-$$-$RANDOM"
  volume="$name-data"
  port_in=8787
  command_args=()
  curl_args=()
  health_args=()
  cookie_name=stacklane_session
  if [[ "$scheme" == https ]]; then
    port_in=9443
    command_args=(--https --setup --data-dir /appdata)
    curl_args=(-k)
    health_args=(--https)
    cookie_name=__Host-stacklane_session
  fi
  [[ -z $(docker ps -aq --filter "name=^/$name$") ]]
  ! docker volume inspect --format '{{.Name}}' "$volume" >/dev/null 2>&1
  docker volume create "$volume" >/dev/null
  created_volume=true
  docker run -d --name "$name" --read-only --cap-drop ALL --security-opt no-new-privileges:true \
    --memory 256m --cpus 0.5 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=64m \
    -p "127.0.0.1::$port_in" -v "$volume:/appdata" -v /var/run/docker.sock:/var/run/docker.sock:ro \
    "$image" "${command_args[@]}" >/dev/null
  created_container=true
  set_base() {
    port=$(docker port "$name" "$port_in/tcp" | sed -n 's/^127\.0\.0\.1://p')
    [[ "$port" =~ ^[0-9]+$ ]]
    base="$scheme://127.0.0.1:$port"
  }
  ready() {
    for attempt in $(seq 1 45); do
      if curl "${curl_args[@]}" -sSf "$base/api/health" >/dev/null 2>&1; then break; fi
      sleep 1
    done
    curl "${curl_args[@]}" -sSf "$base/api/health" >/dev/null
  }
  set_base
  ready
  docker exec "$name" /stacklane --healthcheck "${health_args[@]}"
  docker exec "$name" /stacklane --check-installation --data-dir /appdata
  [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' "$base/api/inventory") == 401 ]]
  if [[ "$scheme" == https ]]; then
    fingerprint_before=$(docker exec "$name" /stacklane --tls-fingerprint --data-dir /appdata)
    [[ "$fingerprint_before" == 'SHA256 Fingerprint='* ]]
    cert_before=$(curl -ksS -o /dev/null -w '%{certs}' "$base/api/health")
  fi
  # Fictional input only. Never point this test at a real installation.
  password='x'
  if $passwordless; then password=''; fi
  payload=$(printf '{"password":"%s","confirmation":"%s"}' "$password" "$password")
  [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 201 ]]
  [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 409 ]]
  login=$(printf '{"username":"admin","password":"%s"}' "$password")
  if $passwordless; then
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' "$base/") == 200 ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' "$base/login") == 303 ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary '{}' "$base/api/auth/logout") == 401 ]]
    headers=$(curl "${curl_args[@]}" -sS -D - "$base/api/session")
    printf '%s\n' "$headers" | grep -q '"passwordless":true'
  else
    headers=$(curl "${curl_args[@]}" -sS -D - -o /dev/null -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$login" "$base/api/auth/login")
  fi
  cookie=$(printf '%s\n' "$headers" | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\);.*/\1/p' | tr -d '\r')
  [[ "$cookie" == "$cookie_name="* ]]
  if $passwordless; then
    csrf=$(printf '%s\n' "$headers" | sed -n 's/.*"csrfToken":"\([^"]*\)".*/\1/p')
    [[ -n "$csrf" ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" -H "Origin: $base" -H 'Content-Type: application/json' --data-binary '{}' "$base/api/auth/logout") == 403 ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" -H 'Origin: https://evil.example.test' -H "X-Stacklane-CSRF: $csrf" -H 'Content-Type: application/json' --data-binary '{}' "$base/api/auth/logout") == 403 ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" -H "Origin: $base" -H "X-Stacklane-CSRF: $csrf" -H 'Content-Type: application/json' --data-binary '{}' "$base/api/auth/logout") == 200 ]]
  fi
  if [[ "$scheme" == https ]]; then
    printf '%s\n' "$headers" | grep -qi 'Secure'
  else
    ! printf '%s\n' "$headers" | grep -qi 'Secure'
  fi
  curl "${curl_args[@]}" -sSf -H "Cookie: $cookie" "$base/api/inventory" >/dev/null
  curl "${curl_args[@]}" -sSf -H "Cookie: $cookie" "$base/api/session" | grep -Fq "\"version\":\"$expected_version\""
  docker restart "$name" >/dev/null
  set_base
  ready
  curl "${curl_args[@]}" -sSf "$base/api/auth/setup" | grep -q '"required":false'
  if [[ "$scheme" == https ]]; then
    [[ $(docker exec "$name" /stacklane --tls-fingerprint --data-dir /appdata) == "$fingerprint_before" ]]
    cert_after=$(curl -ksS -o /dev/null -w '%{certs}' "$base/api/health")
    [[ -n "$cert_before" && "$cert_before" == "$cert_after" ]]
  fi
  if $passwordless; then
    curl "${curl_args[@]}" -sSf -H "Cookie: $cookie" "$base/api/session" | grep -q '"passwordless":true'
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' "$base/login") == 303 ]]
    curl "${curl_args[@]}" -sSf "$base/api/inventory" >/dev/null
  else
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" "$base/api/session") == 401 ]]
    [[ $(curl "${curl_args[@]}" -sS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$login" "$base/api/auth/login") == 200 ]]
  fi
  cleanup
  echo "Container $scenario setup, session policy, restart and persistence checks passed"
done
