#!/usr/bin/env bash
# Disposable integration checks: only the named test container/volume are touched.
set -euo pipefail
trap 'printf "Container installation check failed at line %s\n" "$LINENO" >&2' ERR
image=${1:?Usage: bash tests/test-container.sh IMAGE}
curl() { command curl --connect-timeout 3 --max-time 25 "$@"; }
name="dockspire-install-test-$$-$RANDOM"
volume="$name-data"
created_volume=false
created_container=false
cleanup() {
  if $created_container; then docker rm -f "$name" >/dev/null; fi
  if $created_volume; then docker volume rm "$volume" >/dev/null; fi
}
trap cleanup EXIT
docker volume create "$volume" >/dev/null
created_volume=true
docker run -d --name "$name" --read-only --cap-drop ALL --security-opt no-new-privileges:true \
  --memory 256m --cpus 0.5 --pids-limit 64 --tmpfs /tmp:rw,noexec,nosuid,size=64m \
  -p 127.0.0.1::9443 -v "$volume:/appdata" -v /var/run/docker.sock:/var/run/docker.sock:ro \
  "$image" >/dev/null
created_container=true
set_base() {
  port=$(docker port "$name" 9443/tcp | sed -n 's/^127\.0\.0\.1://p')
  [[ "$port" =~ ^[0-9]+$ ]]
  base="https://127.0.0.1:$port"
}
set_base
for attempt in $(seq 1 45); do
  if curl -ksSf "$base/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -ksSf "$base/api/health" >/dev/null
docker exec "$name" /stacklane --healthcheck --https
docker exec "$name" /stacklane --check-installation --data-dir /appdata
fingerprint_before=$(docker exec "$name" /stacklane --tls-fingerprint --data-dir /appdata)
[[ "$fingerprint_before" == 'SHA256 Fingerprint='* ]]
logged_fingerprint=$(docker logs "$name" 2>&1 | sed -n 's/^.*TLS certificate SHA-256 fingerprint: //p' | tail -n 1)
[[ "$fingerprint_before" == "SHA256 Fingerprint=$logged_fingerprint" ]]
[[ $(curl -ksS -o /dev/null -w '%{http_code}' "$base/api/inventory") == 401 ]]
token=$(docker logs "$name" 2>&1 | sed -n 's/^.*One-time administrator setup token: //p' | tail -n 1)
[[ -n "$token" ]]
# Fictional input only. Never point this test at a real installation.
password='fictional container test password only'
payload=$(printf '{"token":"%s","password":"%s","confirmation":"%s"}' "$token" "$password" "$password")
[[ $(curl -ksS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 201 ]]
[[ $(curl -ksS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$payload" "$base/api/auth/setup") == 409 ]]
login=$(printf '{"username":"admin","password":"%s"}' "$password")
headers=$(curl -ksS -D - -o /dev/null -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$login" "$base/api/auth/login")
cookie=$(printf '%s\n' "$headers" | sed -n 's/^[Ss]et-[Cc]ookie: \([^;]*\);.*/\1/p' | tr -d '\r')
[[ "$cookie" == __Host-stacklane_session=* ]]
printf '%s\n' "$headers" | grep -qi 'Secure'
curl -ksSf -H "Cookie: $cookie" "$base/api/inventory" >/dev/null
cert_before=$(curl -ksS -o /dev/null -w '%{certs}' "$base/api/health")
docker restart "$name" >/dev/null
# Docker can assign another random published port when the container restarts.
set_base
for attempt in $(seq 1 45); do
  if curl -ksSf "$base/api/health" >/dev/null 2>&1; then break; fi
  sleep 1
done
curl -ksSf "$base/api/auth/setup" | grep -q '"required":false'
[[ $(docker exec "$name" /stacklane --tls-fingerprint --data-dir /appdata) == "$fingerprint_before" ]]
cert_after=$(curl -ksS -o /dev/null -w '%{certs}' "$base/api/health")
[[ -n "$cert_before" && "$cert_before" == "$cert_after" ]]
[[ $(curl -ksS -o /dev/null -w '%{http_code}' -H "Cookie: $cookie" "$base/api/session") == 401 ]]
[[ $(curl -ksS -o /dev/null -w '%{http_code}' -H "Origin: $base" -H 'Content-Type: application/json' --data-binary "$login" "$base/api/auth/login") == 200 ]]
echo 'Container HTTPS, setup, login, restart, persistence and Docker/Compose checks passed'
