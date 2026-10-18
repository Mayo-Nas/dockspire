#!/bin/sh
# Reproduce only the package-index refresh under the helper's service restrictions.
# Does not install, remove, or upgrade any packages. Report omits repository URLs.
set -eu
if [ "$(id -u)" -ne 0 ]; then printf '%s\n' 'Run this diagnostic with sudo.'; exit 1; fi
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
umask 077
mkdir -p "$directory/.local/diagnostics"
report=$(mktemp "$directory/.local/diagnostics/host-refresh-diagnostic.XXXXXX")
raw=$(mktemp)
trap 'rm -f -- "$raw"' EXIT HUP INT TERM
result=0
systemd-run --quiet --wait --pipe --collect --unit="stacklane-apt-diagnostic-$(date +%s)" \
  --property=User=root --property=Group=root --property=UMask=0077 \
  --property=NoNewPrivileges=true --property=ProtectHome=true --property=PrivateTmp=true \
  --property=ProtectKernelTunables=true --property=ProtectKernelModules=true \
  --property=ProtectControlGroups=true \
  '--property=RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK' \
  --property=ReadOnlyPaths=/usr/local/lib/stacklane-host \
  /usr/bin/env -i PATH=/usr/sbin:/usr/bin:/sbin:/bin HOME=/root LANG=C LC_ALL=C \
  DEBIAN_FRONTEND=noninteractive NEEDRESTART_MODE=l \
  /usr/bin/apt-get update -o APT::Update::Error-Mode=any >"$raw" 2>&1 || result=$?
python3 - "$raw" "$result" >"$report" <<'PY'
import sys,re,json
text=open(sys.argv[1],errors='replace').read()
lines=[]
for line in text.splitlines():
    if re.search(r'^(?:E:|W:|Err:|Failed)|permission denied|not permitted|not supported|error|denied|segmentation|signal',line,re.I):
        line=re.sub(r'\b(?:https?|ftp|file|socks[45]?)://\S+','[repository URL omitted]',line)
        line=re.sub(r'(?i)(authorization|password|token|secret)\s*[:=]\s*\S+',r'\1=[omitted]',line)
        lines.append(line[:1000])
print(json.dumps({'exitCode':int(sys.argv[2]),'errors':lines[-30:]},indent=2))
PY
owner=${SUDO_UID:-0}
case "$owner" in *[!0-9]*|'') owner=0 ;; esac
chown "$owner" "$report"
chmod 600 "$report"
printf 'Diagnostic complete. Report: %s\n' "$report"
