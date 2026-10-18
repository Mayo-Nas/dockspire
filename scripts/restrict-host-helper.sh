#!/bin/sh
# Replace an idle legacy updater with the version/index-only helper.
# Does not install packages, restart Docker, or touch other containers.
set -eu
if [ "$(id -u)" -ne 0 ]; then printf '%s\n' 'Run this helper replacement with sudo.'; exit 1; fi
directory=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
python3 - "$directory" <<'PY'
import hashlib, http.client, json, os, pathlib, socket, subprocess, sys, time

directory = pathlib.Path(sys.argv[1])
service = 'stacklane-host.service'
changed = False

def request(method, path, body=None):
    connection = http.client.HTTPConnection('localhost', timeout=30)
    connection.sock = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
    connection.sock.settimeout(30)
    connection.sock.connect('/run/stacklane-host/control.sock')
    try:
        connection.request(method, path, body, {'Content-Type': 'application/json'})
        response = connection.getresponse()
        return response.status, json.loads(response.read(1048576))
    finally:
        connection.close()

try:
    expected = next(line.split()[0] for line in (directory / 'SHA256SUMS').read_text().splitlines()
                    if line.split()[1] == 'stacklane')
    if hashlib.sha256((directory / 'stacklane').read_bytes()).hexdigest() != expected:
        raise RuntimeError('Release binary checksum failed; helper left unchanged')
    code, status = request('GET', '/status')
    if code != 200 or not status.get('connected') or status.get('active') is not None:
        raise RuntimeError('Cannot confirm the helper is idle; helper left unchanged')
    subprocess.run([str(directory / 'stacklane'), '--host-check'], check=True,
                   stdout=subprocess.DEVNULL)
    destination = pathlib.Path('/usr/local/lib/stacklane-host/stacklane')
    staged = destination.with_name('stacklane.restricted-next')
    subprocess.run(['install', '-m', '755', '-o', 'root', '-g', 'root',
                    str(directory / 'stacklane'), str(staged)], check=True)
    # No old binary is restored automatically: that would re-enable installation.
    subprocess.run(['systemctl', 'stop', service], check=True)
    changed = True
    os.replace(staged, destination)
    subprocess.run(['systemctl', 'start', service], check=True)
    for attempt in range(30):
        try:
            code, current = request('GET', '/status')
            if code == 200 and current.get('connected') and current.get('updatesEnabled') is False:
                break
        except (OSError, ValueError, http.client.HTTPException):
            pass
        time.sleep(0.5)
    else:
        raise RuntimeError('Replacement helper did not report installation disabled')
    for path in ('/review', '/update'):
        code, problem = request('POST', path, '{"confirmation":"UPDATE DOCKER","acknowledge":true}')
        if code != 403 or 'disabled' not in problem.get('error', ''):
            raise RuntimeError('Replacement helper did not reject package installation')
    print('Helper replaced and checked: package installation is disabled; versions, index refreshes, and history remain available.')
    print('Reconnect the preview socket mount by recreating only its app container.')
except Exception as error:
    if changed:
        subprocess.run(['systemctl', 'stop', service], check=False)
    print(str(error) if isinstance(error, RuntimeError) else 'Helper replacement could not complete; inspect its service on the host.')
    if changed:
        print('Helper left stopped; the legacy updater was not restarted. Docker packages and workloads were not changed.')
    sys.exit(1)
PY
