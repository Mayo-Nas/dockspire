#!/bin/sh
# Explicit, one-time root installation. Does not install or upgrade Docker packages.
set -eu
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
cd "$directory"
if [ "$(id -u)" -ne 0 ]; then printf '%s\n' 'Run with sudo to install the approved host helper service.'; exit 1; fi
test -f ./dockspire && test -f ./dockspire-host.service
chmod 755 ./dockspire
./dockspire --host-check >/dev/null
if systemctl is-active --quiet dockspire-host.service; then
  printf '%s\n' 'Helper already running. Stop it only after checking that no host operation is active, then rerun this installer.'
  exit 1
fi
install -d -m 755 -o root -g root /usr/local/lib/dockspire-host
install -m 755 -o root -g root ./dockspire /usr/local/lib/dockspire-host/dockspire
install -m 644 -o root -g root ./dockspire-host.service /etc/systemd/system/dockspire-host.service
systemctl daemon-reload
systemctl enable --now dockspire-host.service
systemctl is-active --quiet dockspire-host.service
printf '%s\n' 'Host helper installed. No Docker packages were upgraded.'
