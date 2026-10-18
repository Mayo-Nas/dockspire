# Source and image publication

The canonical source/module is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). Source push, container publication and application deployment are separate steps requiring explicit authorization. Never upload private development history or operational notes with the source. Default new repositories/packages to private unless public visibility is explicitly approved.

## Before the first push

Review the exact branch and every ancestor it will publish, including deleted files, author/committer metadata, binary metadata and untracked files that might be staged. Use the sanitized publication history; do not merge a retained private development repository into it. A current `.gitignore` does not remove historical content. Never rewrite history or force-push without explicit authorization.

Run the documented tests, a redacted secret scan of all reachable publication history/current files, and a manual privacy review. Use only intentional public contributor identities/noreply addresses. Keep real deployment addresses, home paths, credentials, diagnostics and environment files outside Git. No detector guarantees absence of arbitrary private information.

After explicit upload authorization, push only the reviewed branch:

```sh
git status --short --branch
git log --oneline
git push -u origin main
```

Do not use `--all` or `--mirror` for a reviewed single-branch first push. Confirm repository visibility and hosted CI results afterward. Configure required branch checks only after the first successful hosted run establishes their names.

## Continuous integration

CI checks Go on Linux/Windows, browser JavaScript, Docker/Compose prerequisite fixtures and a disposable Linux container installation. It has read-only repository permissions and pinned action commits. Checkout does not persist credentials. The container job scans the exact two shipped Go executables using pinned govulncheck before accepting the image. Normal source pushes and pull requests do not publish images or deploy applications.

## Image release

The manual `publish-image.yml` workflow validates an existing version tag, checks it out, runs tests, builds the Linux amd64 image, checks a disposable installation and scans its exact executables before publishing that version to GHCR. Its publishing job alone has package-write permission; registry authentication uses stdin and an ephemeral Docker config. Treat the selected tag's scripts as trusted release code. Dispatch only after explicit publication authorization, and never reuse an existing version for different content.

Review dependency build metadata and vulnerability results for both Dockspire and bundled Compose; the latter has a separate pinned build manifest. Keep required notices with redistributed dependencies. Runtime vulnerability databases can change, so repeat scans before release. Record the resulting image digest and verify it in a fresh installation before recommending the release.

Source license selection remains a separate project decision. Notices under `licenses/` cover third-party components and do not grant a license to Dockspire itself. Branding retains its supplied provenance and asset notes.
