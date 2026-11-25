# KubeAtlas Hub Linux packages

Complete `kubeatlas-portal` packages for Linux `amd64` and `arm64` include a systemd service and the same-version Agent catalog. The catalog contains the Agent installer, Helm chart, and images that Hub serves when you connect a cluster. New releases also bundle the [OpenCost offline chart and exporter](opencost-offline.md).

These steps apply to the complete packages built from this source. For the published v0.1.3 packages, use the [v0.1.3 installation guide](https://github.com/maomada/KubeAtlas/blob/v0.1.3/docs/deploy-with-ai.md#install-from-the-release-bundle).

## Install

Download the package that matches your distribution and architecture from [GitHub Releases](https://github.com/maomada/KubeAtlas/releases). Check its SHA-256 against the release's `checksums.txt`. Use the actual release version in the filename and substitute `arm64` for `amd64` on an ARM64 host.

On Debian or Ubuntu, install the DEB.

```bash
VERSION='<version>'
sudo apt install "./kubeatlas-portal_${VERSION}_linux_amd64.deb"
```

On an RPM-based distribution, install the RPM.

```bash
VERSION='<version>'
sudo dnf install "./kubeatlas-portal_${VERSION}_linux_amd64.rpm"
```

### Configure HTTPS

Configure your HTTPS reverse proxy to forward to `127.0.0.1:1333` on the Hub host. The external hostname must be reachable by the nodes you intend to connect. [Reverse proxy and Agent trust](hub-agent-onboarding.md#configure-lan-and-public-addresses)

Edit `/etc/kubeatlas-portal/hub.env` as root and set the external HTTPS address.

```dotenv
KUBEATLAS_PORTAL_HUB_ORIGIN=https://hub.example.com
```

To use a different local port, set `KUBEATLAS_PORTAL_OPTIONS=--port=2444` and update the proxy upstream. If the proxy runs on a separate host, bind Hub to a private address with `KUBEATLAS_PORTAL_OPTIONS=--listen=192.0.2.10:1333` and restrict access to the proxy.

Apply the configuration before creating the first administrator. The environment setting seeds the public address for fresh Hub state. For an initialized Hub, update the connection address in Organization settings.

```bash
sudo systemctl restart kubeatlas-portal
systemctl is-active kubeatlas-portal
curl --fail --silent --show-error https://hub.example.com/healthz
```

### Create the platform administrator

Read the one-time setup code on the Hub host.

```bash
sudo kubeatlas-portal --hub-state-file=/var/lib/kubeatlas-portal/state.json --show-setup-code
```

Open the HTTPS address in your browser and use the code to create the first administrator. Sign in and select **Clusters → Connect cluster**. [Agent onboarding](hub-agent-onboarding.md#bootstrap-a-new-cluster)

## Upgrade and migration

Back up the Hub state and service configuration before an upgrade. Keep the configured HTTPS address and listener. [State and recovery](hub-state.md)

**Package-managed Hub.** Use the same package manager to install a newer release. The upgrade retains one previous Agent catalog at `/var/lib/kubeatlas-portal-native/previous/catalog` so existing Agents can still pull their image. After every Agent using that digest has been upgraded, move the entire `/var/lib/kubeatlas-portal-native/previous` directory to a protected backup location and restart Hub. This frees the catalog slot for the next package upgrade. [Retaining a previous catalog](hub-agent-onboarding.md#retain-a-previous-catalog)

**Legacy or custom Hub.** A Hub deployed with `install-hub.py` uses that installer's managed upgrade procedure and the matching release files. For a custom service, plan its migration with the existing listener, catalog, and state path. The package manager checks for an existing local Hub unit before replacing the installed binary. [v0.1.3 managed upgrade](https://github.com/maomada/KubeAtlas/blob/v0.1.3/docs/deploy-with-ai.md#install-from-the-release-bundle) · [Custom service configuration](portal.md#maintain-a-custom-hub-service)

## Release verification

`binary-manifest.json` records each published binary package's component, platform, architecture, version, SHA-256, and source commit. The standalone Agent bundle is also used for private catalogs and preparing the Hub OCI image. [Release process](ci-cd.md)

[Helm installation](../deploy/helm/kubeatlas-portal/README.md) · [Docker Compose installation](../deploy/compose/README.md)
