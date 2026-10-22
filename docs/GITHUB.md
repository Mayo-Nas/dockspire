# Repository and releases

The canonical repository is [Mayo-Nas/dockspire](https://github.com/Mayo-Nas/dockspire). The repository and GHCR image are currently private; see [installation](INSTALL.md) for current access requirements.

## Development

`main` contains the complete maintained source, tests, CI, release tooling and documentation. Use focused branches and pull requests to `main`; outside contributors use forks. See [CONTRIBUTING.md](../CONTRIBUTING.md) and [development checks](DEVELOPMENT.md).

Branch protection availability depends on GitHub plan and repository visibility.

CI checks Go on Linux and Windows, browser tests, installation fixtures, disposable Linux container installation and takeover, and the shipped executables for vulnerabilities. Trusted-main CI also checks release upgrade and rollback. Ordinary CI does not publish images or deploy applications.

## Releases

1. Select one new version tag, such as `v0.2.0`, for a reviewed and tested commit on `main`. Keep existing tags unchanged.
2. Run **Publish container image** manually from `main`, supplying that existing tag. The workflow verifies the tag belongs to `main`, tests the selected source, checks disposable installation, takeover, upgrade and rollback, and scans the shipped executables before publishing the Linux amd64 image to GHCR.
3. Record the immutable image digest and validation results. Publish release notes for the same tag with installation, upgrade and rollback instructions and known limitations. The image workflow does not create a GitHub Release automatically.

Use versioned images or immutable digest references in installation instructions. Do not replace a published version with different content. Ordinary pushes and tag pushes do not publish an image or update running installations. Tests and contributor tools stay in source; runtime images contain the application, bundled Compose and required notices.

Dockspire is licensed under [Apache 2.0](../LICENSE), with attribution in [NOTICE](../NOTICE). The Dockerfile includes both under `/licenses` alongside third-party notices; branding retains its supplied [asset notes](branding/ASSET_LICENSE_NOTES.md).
