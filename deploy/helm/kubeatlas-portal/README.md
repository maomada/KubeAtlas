# Deploy KubeAtlas Hub with Helm

Install Hub in a Kubernetes cluster with Helm 3, persistent storage, and either an HTTPS Ingress or a trusted reverse proxy. Choose a version that publishes the Hub OCI chart and same-version Linux `amd64` and `arm64` image with the built-in Agent catalog. [Deployment overview](../../../docs/deploy-with-ai.md)

## Install

1. Select the Kubernetes context where Hub should run. Create the `kubeatlas-portal` namespace if needed, then provision a Secret called `kubeatlas-portal-auth` with the keys `username` and `password`. Use an administrator password at least 16 bytes long. Provision your credentials through a secret manager or protected local files.

2. Configure a dedicated HTTPS address for Hub. With an existing Ingress controller, save a values file such as `hub-ingress.yaml` and reference the TLS Secret in the same namespace.

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: hub.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - hosts: [hub.example.com]
      secretName: kubeatlas-portal-tls
```

3. Install the selected Hub version. If you terminate TLS on a separate reverse proxy, omit `--values` and configure that proxy to reach the Hub Service instead.

```bash
VERSION='<version>'
helm upgrade --install kubeatlas-portal oci://ghcr.io/maomada/charts/kubeatlas-portal \
  --namespace kubeatlas-portal --version "$VERSION" \
  --set existingSecret=kubeatlas-portal-auth \
  --set-string hubOrigin=https://hub.example.com \
  --values hub-ingress.yaml --wait
```

4. Confirm `curl --fail https://hub.example.com/healthz` succeeds and sign in to Hub. Use the Hub-generated command to [connect a cluster](../../../docs/deploy-with-ai.md#2-connect-a-cluster).

The chart runs one Hub replica and persists its state in a PVC. The Service defaults to `ClusterIP`. The proxy or Ingress must serve Hub at the domain root, preserve the external `Host`, support WebSocket upgrades and streaming, and forward Registry and API requests unchanged. Target nodes need to trust the Hub HTTPS certificate. [Proxy and trust requirements](../../../docs/deploy-with-ai.md#1-choose-how-to-deploy-hub)

## Upgrade

Keep the existing namespace, release name, Secret, and Hub PVC. Back up state before upgrading. Repeat the install command with the new published version and the original configuration. Verify the Hub endpoint and the existing Agent connections afterward. [Hub state and recovery](../../../docs/hub-state.md)

When existing Agents still reference a previous image digest, retain their catalog during the upgrade. Point `hubArtifacts.previous.existingClaim` at the PVC containing the previous catalog and set `hubArtifacts.previous.version` to its exact version. [Retain a previous catalog](../../../docs/hub-agent-onboarding.md#retain-a-previous-catalog)

## Custom configuration

To supply a private catalog for the installed version, fill a PVC with the verified catalog, set `hubArtifacts.enabled=true`, and pass its name in `hubArtifacts.existingClaim`. Hub mounts it read-only. Use `image.repository` and `image.tag` for a compatible mirrored image. [Prepare a private catalog](../../../docs/hub-agent-onboarding.md#prepare-a-private-bundle)

The credentials Secret can also hold `mcp-allowed-hosts`, `hub-groups`, and OIDC settings. Set `mcp-allowed-hosts` to the external authority if you will use Hub MCP. For optional identity, proxy, and trusted LAN settings, see [Hub configuration](../../../docs/portal.md#deployment-and-security).
