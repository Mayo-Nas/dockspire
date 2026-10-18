#!/bin/sh
# Explicit, one-time root installation. Does not install or upgrade Docker packages.
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$directory"
if [ "$(id -u)" -ne 0 ]; then printf '%s\n' 'Run with sudo to install the approved host helper service.'; exit 1; fi
test -f ./stacklane && test -f ./stacklane-host.service
chmod 755 ./stacklane
./stacklane --host-check >/dev/null
if systemctl is-active --quiet stacklane-host.service; then
  printf '%s\n' 'Helper already running. Stop it only after checking that no host operation is active, then rerun this installer.'
  exit 1
fi
install -d -m 755 -o root -g root /usr/local/lib/stacklane-host
install -m 755 -o root -g root ./stacklane /usr/local/lib/stacklane-host/stacklane
install -m 644 -o root -g root ./stacklane-host.service /etc/systemd/system/stacklane-host.service
systemctl daemon-reload
systemctl enable --now stacklane-host.service
systemctl is-active --quiet stacklane-host.service
printf '%s\n' 'Host helper installed. No Docker packages were upgraded.'
