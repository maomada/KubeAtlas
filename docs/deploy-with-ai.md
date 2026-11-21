# Deploy KubeAtlas with an AI agent

Deploy **KubeAtlas Hub** first. Use the Hub to install **KubeAtlas Agent** in each cluster you want to connect.

Share this [raw Markdown runbook](https://raw.githubusercontent.com/maomada/KubeAtlas/main/docs/deploy-with-ai.md) with your AI agent.

> Use this runbook to deploy KubeAtlas Hub with `<Helm, DEB/RPM, or Docker Compose>` on `<Hub host or Kubernetes context>`. Serve it at `<https://hub.example.com>` and connect `<target Kubernetes context>`. Tell me the installed version, HTTPS address, and what you verified. I will enter credentials and approve the pairing in Hub.

## 1. Choose how to deploy Hub

Use a Linux `amd64` or `arm64` host or node. Pick a version from [GitHub Releases](https://github.com/maomada/KubeAtlas/releases/latest) and confirm it provides the required artifacts for your method. Keep the Hub, chart, and Agent artifacts on the same release version.

| Deployment method | What to prepare |
| --- | --- |
| [Helm](../deploy/helm/kubeatlas-portal/README.md) | A Kubernetes context for Hub, persistent storage, an administrator Secret, and an HTTPS Ingress or reverse proxy. |
| [DEB or RPM](#install-from-the-release-bundle) | A Linux systemd host, the release files, a private credentials file, and an HTTPS reverse proxy. |
| [Docker Compose](../deploy/compose/README.md) | A Linux container host, a private environment file, and an HTTPS reverse proxy. |

Use a dedicated HTTPS root address, such as `https://hub.example.com`. The registration shell and every eligible node must be able to reach it and trust its certificate. Pass the whole origin to Hub with the original `Host` header, WebSocket upgrades on `/agent`, and streaming responses on `/mcp`. Serve `/api/`, `/downloads/agent/`, and `/v2/` without rewriting paths or redirecting. If a private CA is used, configure trust on the registration host and in each node's container runtime. [HTTPS and Agent trust](hub-agent-onboarding.md#configure-lan-and-public-addresses)

For an existing Hub, keep its credentials and state location, and take a verified backup before changing the release. Preserve the previous Agent image catalog while any Agent still uses it. [Hub state and recovery](hub-state.md) · [Retaining a previous catalog](hub-agent-onboarding.md#retain-a-previous-catalog)

<a name="3-run-a-shared-hub"></a>

### Install from the release bundle

Download the matching `kubeatlas-portal` DEB or RPM, `kubeatlas-hub-agent-bundle_<version>.tar.gz`, `install-hub.py`, and `hub-checksums.txt` into one directory. The bundle includes the Agent images, Helm chart, and install scripts.

The host needs Python 3, systemd, `useradd`, and `nologin`. DEB installation uses `dpkg` and `dpkg-deb`. RPM installation uses `rpm`, `rpm2cpio`, and `cpio`. Configure the HTTPS proxy to forward to `127.0.0.1:9290` first. With a separate proxy host, pass a private listener with `--listen` and restrict direct access to that proxy.

On the Hub host, place the administrator credentials in a root-owned JSON file with mode `0600` and the keys `username` and `password`. Use a password at least 16 bytes long. Provide the credentials through the host or a secret manager.

```bash
# Use the exact file names from the selected release.
VERSION='<version>'
PACKAGE="kubeatlas-portal_${VERSION}_linux_amd64.deb"
awk '$2 == "install-hub.py"' hub-checksums.txt | sha256sum --check - &&
  sudo python3 ./install-hub.py install --package "./$PACKAGE" \
    --checksums ./hub-checksums.txt --catalog-archive "./kubeatlas-hub-agent-bundle_$VERSION.tar.gz" \
    --origin https://hub.example.com --credentials /root/kubeatlas-hub-credentials.json
```

Use the arm64 or RPM filename if it matches the target. With a private CA, pass its PEM file using `--ca-file`. The installer imports the Agent bundle, configures systemd, and checks that Hub and its downloads are reachable at the HTTPS address.

For a Hub managed by the installer, use the `upgrade` command with the new files and the same package format. It backs up the state, retains the previous catalog, and rolls back on a failed cutover. Use `--retire-previous-catalog` only after migrating the Agents that still use the catalog it would replace. [Linux packages and upgrades](packaging.md)

<a name="5-connect-a-cluster"></a>

## 2. Connect a cluster

1. Open the Hub HTTPS address and sign in. Check that the connection address matches the intended endpoint under **Hub connection addresses**.
2. In the intended organization, select **Clusters → Connect cluster** (`集群 → 连接集群`). Check `kubectl config current-context` on the registration host and run the generated command. Confirm the displayed cluster identity, then approve its matching code in Hub.
3. Confirm the cluster is **Connected** in Hub and open nodes or namespaces to verify access. Report the Hub version, Agent version, and connected cluster ID. [Registration prerequisites and recovery](hub-agent-onboarding.md#bootstrap-a-new-cluster)

<a name="manual-hub-setup"></a>
<a name="4-prepare-agent-artifacts-manually"></a>

For deployments using custom service configuration or a private mirror, see [Hub configuration](portal.md#deployment-and-security) and [private Agent catalogs](hub-agent-onboarding.md#prepare-a-private-bundle). To add one-click OpenCost installation for supported clusters, see [OpenCost offline preparation](opencost-offline.md).
