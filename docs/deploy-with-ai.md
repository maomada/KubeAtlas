# Deploy KubeAtlas with an AI agent

Deploy **KubeAtlas Hub** first. Use the Hub to install **KubeAtlas Agent** in each cluster you want to connect.

Share this [raw Markdown runbook](https://raw.githubusercontent.com/maomada/KubeAtlas/main/docs/deploy-with-ai.md) with your AI agent.

> Use this runbook to deploy KubeAtlas Hub with `<Helm, DEB/RPM, or Docker Compose>` on `<Hub host or Kubernetes context>`. Serve it at `<https://hub.example.com>` and connect `<target Kubernetes context>`. Tell me the installed version, HTTPS address, and what you verified. I will create the first administrator in my browser and approve the pairing in Hub.

## 1. Choose how to deploy Hub

Use a Linux `amd64` or `arm64` host or node. Pick a version from [GitHub Releases](https://github.com/maomada/KubeAtlas/releases/latest) and confirm it provides the required artifacts for your method. Keep the Hub, chart, and Agent artifacts on the same release version.

| Deployment method | What to prepare |
| --- | --- |
| [Helm](../deploy/helm/kubeatlas-portal/README.md) | A Kubernetes context for Hub, persistent storage, and an HTTPS Ingress or reverse proxy. |
| [DEB or RPM](#install-a-deb-or-rpm) | A Linux systemd host, the package for its architecture, and an HTTPS reverse proxy. |
| [Docker Compose](../deploy/compose/README.md) | A Linux container host and an HTTPS reverse proxy. |

Use a dedicated HTTPS root address, such as `https://hub.example.com`. The registration shell and every eligible node must be able to reach it and trust its certificate. Pass the whole origin to Hub with the original `Host` header, WebSocket upgrades on `/agent`, and streaming responses on `/mcp`. Serve `/api/`, `/downloads/agent/`, and `/v2/` without rewriting paths or redirecting. If a private CA is used, configure trust on the registration host and in each node's container runtime. [HTTPS and Agent trust](hub-agent-onboarding.md#configure-lan-and-public-addresses)

After a fresh Hub starts, open its HTTPS address and create the platform administrator using the one-time setup code. Each deployment guide shows how to obtain the code on the host or from the Hub Pod. The operator should enter the code and password directly in the browser. Agent onboarding is available once setup completes.

For an existing Hub, keep its listener, state location, and credentials, and take a verified backup before changing the release. Preserve the previous Agent image catalog while any Agent still uses it. [Hub state and recovery](hub-state.md) · [Retaining a previous catalog](hub-agent-onboarding.md#retain-a-previous-catalog)

<a name="3-run-a-shared-hub"></a>
<a name="install-from-the-release-bundle"></a>

### Install a DEB or RPM

Use the package for your Linux architecture. With the complete package, `apt` or `dnf` installs the Hub binary, systemd service, and matching Agent catalog in one operation. Follow the [package installation guide](packaging.md#install) to set the HTTPS origin, read the one-time setup code, and confirm that the service is ready.

For v0.1.3, follow its [versioned deployment guide](https://github.com/maomada/KubeAtlas/blob/v0.1.3/docs/deploy-with-ai.md#install-from-the-release-bundle). Before replacing an existing service, check the [package upgrade and migration guide](packaging.md#upgrade-and-migration).

<a name="5-connect-a-cluster"></a>

## 2. Connect a cluster

1. Open the Hub HTTPS address and sign in. Check that the connection address matches the intended endpoint under **Hub connection addresses**.
2. In the intended organization, select **Clusters → Connect cluster** (`集群 → 连接集群`). Check `kubectl config current-context` on the registration host and run the generated command. Confirm the displayed cluster identity, then approve its matching code in Hub.
3. Confirm the cluster is **Connected** in Hub and open nodes or namespaces to verify access. Report the Hub version, Agent version, and connected cluster ID. [Registration prerequisites and recovery](hub-agent-onboarding.md#bootstrap-a-new-cluster)

<a name="manual-hub-setup"></a>
<a name="4-prepare-agent-artifacts-manually"></a>

For deployments using custom service configuration or a private mirror, see [Hub configuration](portal.md#deployment-and-security) and [private Agent catalogs](hub-agent-onboarding.md#prepare-a-private-bundle). To add one-click OpenCost installation for supported clusters, see [OpenCost offline preparation](opencost-offline.md).
