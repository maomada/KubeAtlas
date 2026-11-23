# Deploy KubeAtlas Hub with Helm

Install Hub in a Kubernetes cluster with Helm 3, persistent storage, and either an HTTPS Ingress or a trusted reverse proxy. Choose a version that publishes the Hub OCI chart and same-version Linux `amd64` and `arm64` image with the built-in Agent catalog. [Deployment overview](../../../docs/deploy-with-ai.md)

## Install

1. Select the Kubernetes context where Hub should run. Create the `kubeatlas-portal` namespace if needed.

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
  --set-string hubOrigin=https://hub.example.com \
  --values hub-ingress.yaml --wait
```

4. Confirm `curl --fail https://hub.example.com/healthz` succeeds. Read the one-time code and open `https://hub.example.com` to create the platform administrator. Then use the Hub-generated command to [connect a cluster](../../../docs/deploy-with-ai.md#2-connect-a-cluster).

```bash
kubectl -n kubeatlas-portal exec deploy/kubeatlas-portal -- /kubeatlas-portal \
  --hub-state-file=/var/lib/kubeatlas-portal/hub/state.json --show-setup-code
```

The chart runs one Hub replica and persists its state in a PVC. The Hub container listens on port `1333`. The `ClusterIP` Service forwards to it on the port set by `service.port`, which also defaults to `1333`. The proxy or Ingress must serve Hub at the domain root, preserve the external `Host`, support WebSocket upgrades and streaming, and forward Registry and API requests unchanged. Target nodes need to trust the Hub HTTPS certificate. [Proxy and trust requirements](../../../docs/deploy-with-ai.md#1-choose-how-to-deploy-hub)

## Upgrade

Keep the existing namespace, release name, Hub PVC, and any configured Secret. Back up state before upgrading. Repeat the install command with the new published version and the original configuration. Use `--set service.port=9290` if the existing proxy still routes to that port. Verify the Hub endpoint and the existing Agent connections afterward. [Hub state and recovery](../../../docs/hub-state.md)

To roll back to v0.1.3 or earlier, stop Hub and restore the pre-upgrade PVC contents and administrator Secret before starting the old image.

When existing Agents still reference a previous image digest, retain their catalog during the upgrade. Point `hubArtifacts.previous.existingClaim` at the PVC containing the previous catalog and set `hubArtifacts.previous.version` to its exact version. [Retain a previous catalog](../../../docs/hub-agent-onboarding.md#retain-a-previous-catalog)

## Custom configuration

To supply a private catalog for the installed version, fill a PVC with the verified catalog, set `hubArtifacts.enabled=true`, and pass its name in `hubArtifacts.existingClaim`. Hub mounts it read-only. Use `image.repository` and `image.tag` for a compatible mirrored image. [Prepare a private catalog](../../../docs/hub-agent-onboarding.md#prepare-a-private-bundle)

Set `existingSecret` for unattended administrator setup or to preserve the credentials during a legacy upgrade. Supply the `username` and `password` keys for those cases. The same Secret can hold `mcp-allowed-hosts`, `hub-groups`, and OIDC settings. Set `mcp-allowed-hosts` to the external authority if you will use Hub MCP. [Hub configuration](../../../docs/portal.md#deployment-and-security)
