# KubeAtlas Hub Linux packages

KubeAtlas Hub is packaged as `kubeatlas-portal` for Linux `amd64` and `arm64`. The [release page](https://github.com/maomada/KubeAtlas/releases/latest) supplies DEB and RPM packages, `install-hub.py`, `hub-checksums.txt`, and the matching `kubeatlas-hub-agent-bundle_<version>.tar.gz`.

The [Hub installer](deploy-with-ai.md#install-from-the-release-bundle) checks the package and bundle, configures a systemd service, and serves the bundled Agent artifacts from Hub. The [Hub registration command](hub-agent-onboarding.md#bootstrap-a-new-cluster) installs Agent in the selected Kubernetes cluster.

## Install and upgrade

Follow the [systemd installation steps](deploy-with-ai.md#install-from-the-release-bundle). Confirm the installed version and service health.

```bash
kubeatlas-portal --version
systemctl is-active kubeatlas-portal
```

For a managed Hub, select the same DEB or RPM format for upgrades and use the new release's installer and artifacts. The installer backs up state and retains the previous Agent catalog. Keep older catalogs available until the Agents using their image digests have been migrated. [Hub state and recovery](hub-state.md)

For an existing custom service, keep the current state path and credentials, and use the [manual Hub settings](portal.md#deployment-and-security). The DEB and RPM alone install the binary and documentation. The managed systemd deployment is configured by the installer.

## Release verification

Use `hub-checksums.txt` to verify the installer, Hub package and Agent bundle before installation. `binary-manifest.json` records each published binary package's component, platform, architecture, version, SHA-256, and source commit. Release publication verifies the package contents, native Linux runtime tests, and offline approval. [Release process](ci-cd.md)

[Helm installation](../deploy/helm/kubeatlas-portal/README.md) · [Docker Compose installation](../deploy/compose/README.md)
