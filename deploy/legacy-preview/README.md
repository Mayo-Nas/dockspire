# Dockspire Linux release

Requires Linux amd64, Docker Engine and the modern Docker Compose plugin. Go and Node are not needed on this VM.

From this extracted directory:

```sh
bash install.sh --check-only
bash install.sh
```

The installer verifies bundled checksums, checks Docker/Compose and asks privately for the initial `admin` password if no administrator exists. Existing administrators are preserved. It builds a small image from the bundled executable and starts only the manager container. It never installs host packages or imports other managers' stacks.

For scripted first initialization, pipe one password line using `bash install.sh --init-admin-stdin`. Do not put the password in command arguments or an environment variable.

The application listens on the VM's 127.0.0.1:8787. From your workstation, run `ssh -N -L 8788:127.0.0.1:8787 USER@VM`, then open http://127.0.0.1:8788 and sign in as `admin`.

Keep this installation directory and its private `data/` folder in place for upgrades. The executable (`stacklane`), Docker image/project (`stacklane-preview`) and `STACKLANE_*` settings retain their compatibility names. Do not rename an existing project or change its data mount during an upgrade. Retain the previous release/image and back up private app data and workload data separately before upgrading. See the installation and operations guides in the source repository for upgrade, rollback, backup and password-reset procedures.

Project and documentation: https://github.com/Mayo-Nas/dockspire

`RELEASE.txt` identifies the source commit, version and target. `SHA256SUMS` covers the runtime files. The download also has a separate archive checksum; verify that before extracting or executing files. Dependency notices are under `licenses/`.
