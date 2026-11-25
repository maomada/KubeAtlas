# KubeAtlas

**English** · [简体中文](README.zh-CN.md)

**A self-hosted operations console for Kubernetes.** Deploy KubeAtlas Hub and connect your clusters with KubeAtlas Agent to find failing workloads, trace dependencies, and see what changed.

[Releases](https://github.com/maomada/KubeAtlas/releases/latest) · [Deploy Hub](#deploy-hub) · [Connect a cluster](#connect-a-cluster) · [Deploy with AI](docs/deploy-with-ai.md)

```mermaid
flowchart TB
    Browser["Team browser"] --> Hub["KubeAtlas Hub"]
    MCP["MCP client"] --> Hub

    subgraph ClusterA["Kubernetes cluster A"]
        AgentA["KubeAtlas Agent"] --> APIA["Kubernetes API"]
    end

    subgraph ClusterB["Kubernetes cluster B"]
        AgentB["KubeAtlas Agent"] --> APIB["Kubernetes API"]
    end

    Hub <-->|"Agent tunnel"| AgentA
    Hub <-->|"Agent tunnel"| AgentB
```

**Hub** hosts the web console and manages organizations, cluster access, and MCP credentials. **Agent** runs in each cluster and opens an outbound connection to Hub. Cluster operations are scoped to the selected organization and enforced by Kubernetes RBAC.

## What you can do

- **Investigate issues** with workload health, logs, events, and a timeline of resource changes. [Cluster checks](docs/cluster-checks.md) · <a name="kubernetes-upgrade-impact"></a>[Upgrade impact checks](docs/upgrade-impact.md)
- **Explore applications** and their resource dependencies, inspect live traffic when a source is available, and review Helm releases and Argo CD or Flux sync status. [Integrations](docs/integrations.md)
- **Review capacity and cost** with scheduling requests, node capacity, and cost allocation. New releases bundle the offline files to install OpenCost from the Cost page on clusters with a compatible Prometheus. [Capacity](docs/capacity.md) · [Offline OpenCost](docs/opencost-offline.md)
- **Connect an AI client** over MCP using a credential scoped to selected clusters and your permissions. [Hub MCP](docs/hub-live-mcp.md)

## Deploy Hub

Starting with [v0.1.4](https://github.com/maomada/KubeAtlas/releases/tag/v0.1.4), each Linux package includes the systemd service and the matching offline Agent catalog. Download one package for your distribution and architecture.

| Linux distribution | Install command for `amd64` |
| --- | --- |
| Debian or Ubuntu | `sudo apt install ./kubeatlas-portal_0.1.4_linux_amd64.deb` |
| RPM-based | `sudo dnf install ./kubeatlas-portal_0.1.4_linux_amd64.rpm` |

The packaged service listens on `127.0.0.1:1333` by default. Configure an HTTPS address reachable by your clusters, then use the one-time setup code to create the first administrator in your browser. [Linux setup and upgrades](docs/packaging.md#configure-https)

[Helm](deploy/helm/kubeatlas-portal/README.md) · [Docker Compose](deploy/compose/README.md) · [v0.1.3 deployment guide](https://github.com/maomada/KubeAtlas/blob/v0.1.3/docs/deploy-with-ai.md)

## Connect a cluster

1. Sign in to Hub and select **Clusters → Connect cluster**.
2. Run the generated command in a shell with access to the intended Kubernetes context. Hub supplies the Agent installer, Helm chart, and image.
3. Approve the matching code in Hub and check that the cluster is **Connected**. [Agent onboarding](docs/hub-agent-onboarding.md#bootstrap-a-new-cluster)

To hand the setup to an AI agent, share the [deployment runbook](docs/deploy-with-ai.md) or its [raw Markdown](https://raw.githubusercontent.com/maomada/KubeAtlas/main/docs/deploy-with-ai.md).

## Contribute

[Development guide](DEVELOPMENT.md) · [Contributing](CONTRIBUTING.md) · [Discussions](https://github.com/maomada/KubeAtlas/discussions) · [Security reporting](SECURITY.md)

Licensed under [Apache 2.0](LICENSE). See [NOTICE](NOTICE) for attribution.
