# KubeAtlas Self-Hosted Cloud Hub Helm Chart

This chart deploys the self-hosted Cloud Hub used by KubeAtlas Portal and
Agents. The `kubeatlas-portal` name is retained for the binary and chart; this
deployment has no centralized Fleet registry, snapshot ingestion, or token map.
For the full identity, API, and security model, see
[`docs/portal.md`](../../../docs/portal.md).

## Prerequisites

- Kubernetes and Helm 3
- A PersistentVolume provisioner
- An externally managed Secret containing `username` and `password`
- A version actually published through the manual Release GHCR workflow

Optional Secret keys configure `hub-groups`, `oidc-issuer`, `oidc-client-id`,
`oidc-redirect-url`, `oidc-client-secret`, and `mcp-allowed-hosts`. OIDC values
must be configured together. Use an independent self-hosted identity provider;
directory synchronization and backchannel logout are not provided.

Create and rotate credentials through your approved secret manager or GitOps
Secret mechanism. Do not put credentials in Helm values, chart files, commands
retained in shell history, or the Helm release state.

## Build an image

Build locally for testing:

```bash
docker build -f Dockerfile.portal --target full -t kubeatlas-portal:local .
```

The local test image is not a published release.

## Install

```bash
helm upgrade --install kubeatlas-portal oci://ghcr.io/maomada/charts/kubeatlas-portal \
  --namespace kubeatlas-portal --create-namespace \
  --version "$VERSION" \
  --set existingSecret=kubeatlas-portal-auth
```

The chart creates a single-replica `Recreate` Deployment and a PVC for the
required Hub state file at `/var/lib/kubeatlas-portal/hub/state.json`. The Pod
does not mount a Kubernetes ServiceAccount token and runs with a read-only root
filesystem. The Hub relies on connected Agents to proxy Kubernetes requests;
it does not read cluster credentials or call the Kubernetes API directly.

The Service is cluster-internal by default. If exposing it with an Ingress,
configure TLS termination at the ingress or another trusted reverse proxy.
The UI and API are root-hosted on a dedicated hostname; subpath hosting is not
supported. Do not expose login or bearer traffic over untrusted plaintext
networks.

Local owner login remains available. OIDC can be enabled with the optional
Secret keys above. Sessions use `HttpOnly`, `Secure`, `SameSite=Strict` cookies
and expire after eight hours or a Hub restart. OIDC directory synchronization
and backchannel logout are unavailable; disabling an IdP account alone does
not revoke an already-issued session.

Cloud Hub MCP is exposed at `/mcp` and requires an independently issued
bearer credential scoped to current active membership and clusters. Native
Agent MCP endpoints `/mcp` and `/mcp-readonly` are separate services and
remain unchanged. Preserve the PVC when upgrading: it contains the required
Hub identity and credential state. Back it up using the storage platform's
supported process.

## Hub-hosted Agent artifacts

See [Hub-hosted Agent onboarding](../../../docs/hub-agent-onboarding.md) for bundle preparation and trust requirements. The optional bundle is mounted from an operator-managed PVC and mounted read-only by the Portal:

```yaml
hubArtifacts:
  enabled: true
  existingClaim: kubeatlas-agent-artifacts
```

The PVC must contain the catalog produced by `cmd/prepare-hub-artifacts` and match the Portal build version. An absent bundle disables install artifacts without disabling Hub/MCP; a configured but invalid catalog prevents the Portal from starting. Artifact bytes are not fetched or published at runtime.
