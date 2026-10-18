#!/bin/sh
# Tests the installation gate using fixtures, without connecting to Docker.
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")/../scripts" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT HUP INT TERM
if PATH="$fixture" /bin/sh "$directory/check-installation.sh" >/dev/null 2>&1; then
  printf '%s\n' 'Missing Docker incorrectly passed'; exit 1
fi
cat > "$fixture/docker" <<'FIXTURE'
#!/bin/sh
case "$1" in
  --version) printf '%s\n' 'Docker fixture' ;;
  version) [ "${FIXTURE_DAEMON:-ready}" = ready ] || exit 1; printf '%s\n' 'Engine fixture' ;;
  compose) [ "${FIXTURE_COMPOSE:-ready}" = ready ] || exit 1; printf '%s\n' '5.5.0' ;;
  *) exit 1 ;;
esac
FIXTURE
chmod 755 "$fixture/docker"
if FIXTURE_DAEMON=stopped PATH="$fixture" /bin/sh "$directory/check-installation.sh" >/dev/null 2>&1; then
  printf '%s\n' 'Stopped daemon incorrectly passed'; exit 1
fi
if FIXTURE_COMPOSE=missing PATH="$fixture" /bin/sh "$directory/check-installation.sh" >/dev/null 2>&1; then
  printf '%s\n' 'Missing Compose incorrectly passed'; exit 1
fi
PATH="$fixture" /bin/sh "$directory/check-installation.sh" >/dev/null
printf '%s\n' 'Installation gate: missing Docker, stopped daemon, missing Compose, and ready fixtures passed'
