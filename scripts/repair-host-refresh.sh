#!/bin/sh
# Test APT under the corrected restrictions before changing the installed service.
# Refreshes indexes only; never installs, removes, or upgrades packages.
set -eu
if [ "$(id -u)" -ne 0 ]; then printf '%s\n' 'Run this repair with sudo.'; exit 1; fi
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
python3 - "$directory" <<'PY'
import http.client, json, os, pathlib, socket, subprocess, sys, tempfile, time

os.umask(0o077)
directory = pathlib.Path(sys.argv[1])
fd, report_path = tempfile.mkstemp(prefix='host-refresh-repair.', dir=directory)
report = {'success': False, 'packagesChanged': False}

def status():
    connection = http.client.HTTPConnection('localhost', timeout=15)
    connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.sock.settimeout(15)
    connection.sock.connect('/run/dockspire-host/control.sock')
    try:
        connection.request('GET', '/status')
        response = connection.getresponse()
        if response.status != 200:
            raise RuntimeError('Cannot check whether the host helper is idle')
        return json.loads(response.read(1048576))
    finally:
        connection.close()

dropin = pathlib.Path('/etc/systemd/system/dockspire-host.service.d/apt-identity.conf')
previous = None
changed = False
try:
    if status().get('active'):
        raise RuntimeError('A host operation is active; no repair attempted')
    properties = [
        'User=root', 'Group=root', 'UMask=0077', 'NoNewPrivileges=true',
        'AmbientCapabilities=CAP_SETUID CAP_SETGID',
        'ProtectHome=true', 'PrivateTmp=true', 'ProtectKernelTunables=true',
        'ProtectKernelModules=true', 'ProtectControlGroups=true',
        'RestrictAddressFamilies=AF_UNIX AF_INET AF_INET6 AF_NETLINK',
        'ReadOnlyPaths=/usr/local/lib/dockspire-host',
        'RuntimeMaxSec=300',
    ]
    command = ['systemd-run', '--quiet', '--wait', '--pipe', '--collect',
               '--unit=dockspire-apt-repair-test-' + str(time.time_ns())]
    command += ['--property=' + value for value in properties]
    command += ['/usr/bin/env', '-i', 'PATH=/usr/sbin:/usr/bin:/sbin:/bin',
                'HOME=/root', 'LANG=C', 'LC_ALL=C', 'DEBIAN_FRONTEND=noninteractive',
                'NEEDRESTART_MODE=l', '/usr/bin/apt-get', 'update',
                '-o', 'APT::Update::Error-Mode=any']
    test = subprocess.run(command, stdout=subprocess.DEVNULL, stderr=subprocess.PIPE,
                          timeout=330)
    report['testExitCode'] = test.returncode
    if test.returncode:
        # Return fixed diagnostic categories, never raw repository or credential text.
        errors = test.stderr.decode(errors='replace').lower()
        report['identitySwitchDenied'] = any(word in errors for word in
            ('seteuid', 'setresuid', 'failed to set new user ids'))
        raise RuntimeError('APT self-test failed; installed service was left unchanged')
    if status().get('active'):
        raise RuntimeError('A host operation started during the self-test; service left unchanged')
    previous = dropin.read_bytes() if dropin.exists() else None
    dropin.parent.mkdir(mode=0o755, parents=True, exist_ok=True)
    if previous is not None:
        backup = dropin.with_name('apt-identity.conf.backup-' + str(time.time_ns()))
        backup.write_bytes(previous)
    dropin.write_text('[Service]\nAmbientCapabilities=CAP_SETUID CAP_SETGID\n')
    dropin.chmod(0o644)
    changed = True
    subprocess.run(['systemctl', 'daemon-reload'], check=True)
    subprocess.run(['systemctl', 'restart', 'dockspire-host.service'], check=True)
    for attempt in range(20):
        try:
            current = status()
            if current.get('connected'):
                break
        except (OSError, RuntimeError):
            pass
        time.sleep(0.5)
    else:
        raise RuntimeError('Helper did not become available after restart')
    report['success'] = True
    report['message'] = 'APT self-test passed; helper identity capabilities repaired'
except Exception as error:
    report['message'] = str(error) if isinstance(error, RuntimeError) else 'Repair could not complete'
    if changed:
        if previous is None:
            dropin.unlink()
        else:
            dropin.write_bytes(previous)
            dropin.chmod(0o644)
        subprocess.run(['systemctl', 'daemon-reload'], check=False)
        subprocess.run(['systemctl', 'restart', 'dockspire-host.service'], check=False)
        report['serviceChangeReverted'] = True
finally:
    with os.fdopen(fd, 'w') as stream:
        json.dump(report, stream, indent=2)
        stream.write('\n')
    owner = os.environ.get('SUDO_UID', '0')
    os.chown(report_path, int(owner) if owner.isdecimal() else 0, -1)
    print(report['message'])
    print('Report: ' + report_path)
sys.exit(0 if report['success'] else 1)
PY
