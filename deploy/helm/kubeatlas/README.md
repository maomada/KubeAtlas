# KubeAtlas Helm Chart

Images and charts for this repository are published only by the manual
workflow documented in [`docs/ci-cd.md`](../../../docs/ci-cd.md); this source
checkout does not imply any version has been published. CLI installers and
direct update checks use this repository's GitHub releases. An unreadable
release lookup reports an error; the installer fails if its CLI archive or
checksum is missing. Package-manager
identifiers remain compatibility names, not a claim that those feeds provide
this project's releases.

Deploy KubeAtlas to your Kubernetes cluster for web-based cluster visualization and management.

> Upstream Cloud is unavailable: `cloud.enabled: true` fails chart rendering.
> Cloud-specific RBAC sections below describe retained compatibility contracts,
> not a supported product mode. Do not use these legacy values to enable Cloud
> features; no Cloud service or Cloud-triggered upgrade is available in KubeAtlas.

> **Maintainers**: this directory is the canonical source. The owned OCI publishing flow and its prerequisites are in the CI/CD guide.

> **See also:** [In-Cluster Deployment Guide](../../../docs/in-cluster.md) for ingress and DNS setup.

## Prerequisites

- Kubernetes 1.21+
- Helm 3.0+

## Installation

### Quick Start

```bash
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas \
  --version "$VERSION" -n kubeatlas --create-namespace
```

Access via port-forward:
```bash
kubectl port-forward svc/kubeatlas 9280:9280 -n kubeatlas
open http://localhost:9280
```

Each install or upgrade prints how to open KubeAtlas for the values you set. To see
it again, run `helm get notes kubeatlas -n kubeatlas`.

### With Ingress

```bash
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas \
  --version "$VERSION" -n kubeatlas --create-namespace \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=kubeatlas.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix
```

### With TLS

```bash
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas \
  --version "$VERSION" -n kubeatlas --create-namespace \
  --set ingress.enabled=true \
  --set ingress.className=nginx \
  --set ingress.hosts[0].host=kubeatlas.example.com \
  --set ingress.hosts[0].paths[0].path=/ \
  --set ingress.hosts[0].paths[0].pathType=Prefix \
  --set ingress.tls[0].secretName=kubeatlas-tls \
  --set ingress.tls[0].hosts[0]=kubeatlas.example.com
```

### With Gateway API HTTPRoute

HTTPRoute is disabled by default. Empty `httpRoute.rules` creates a `/`
`PathPrefix` route to KubeAtlas's Service. The generated rule has no timeout by
default, so the Gateway deployment's default timeout applies. Set
`httpRoute.defaultTimeout` to add an explicit request timeout, or set it to
`0s` to explicitly disable timeout. Set `rules` for multiple prefixes,
timeouts, filters, or custom backends; supplied rules replace that default and
pass through unchanged.

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: public-gateway
  hostnames: []
  # Optional timeout for chart-generated default rule.
  # defaultTimeout: 30s
  rules:
    - matches:
        - path:
            type: PathPrefix
            value: /api
      timeouts:
        request: 30s
        backendRequest: 20s
      backendRefs:
        - name: kubeatlas
          port: 9280
    - matches:
        - path:
            type: PathPrefix
            value: /admin
      timeouts:
        request: 2m
      backendRefs:
        - name: kubeatlas
          port: 9280
```

Override `httpRoute.apiVersion` when cluster Gateway API support requires a
different version, for example `gateway.networking.k8s.io/v1beta1`. Custom
rules can define their own `timeouts` independently.

`httpRoute` and `ingress` are mutually exclusive - enabling both fails the
render. `httpRoute.enabled` requires at least one `parentRefs` entry (the
Gateway to attach to), otherwise the render fails rather than emit a route
bound to no Gateway.

### Cloud (unsupported)

Legacy hosted KubeAtlas Cloud connection and installation are unsupported in
KubeAtlas. The self-hosted Cloud Hub is a separate service; see the
[Cloud Hub guide](../../../docs/portal.md).

### Legacy KubeAtlas Hub enrollment (unavailable)

KubeAtlas does not support KubeAtlas Hub enrollment. The legacy Cloud chart
configuration is not a supported deployment path; do not use historical Cloud
values or instructions from older KubeAtlas documentation.

### Legacy Cloud integration-read RBAC (unsupported)

upstream Cloud's default viewer/member/owner bindings receive explicit `get/list/watch`
grants for KubeAtlas's curated integrations, in addition to their existing roles.
These are full-object Kubernetes permissions: inline configuration in specs and
status is visible, not just KubeAtlas's summaries. The baseline adds no writes,
Secret grants, RBAC-object grants, or wildcard resources/groups.

```yaml
cloud:
  defaultRbac:
    integrationRead:
      viewer: true
      member: true
      owner: true
    clusterScopedRead:
      viewer: false  # retains namespaced integration reads, not cluster kinds
```

Each integration follows its `rbac.crdGroups` collection flag; `all=true` enables
only the reviewed finite baseline, not arbitrary caller access. Custom base
roles (`viewerClusterRole`, etc.) do **not** disable these separate bindings.
`integrationRead.<tier>=false` removes only the new add-on; existing cluster-read,
base roles, vendor aggregation and customer bindings can still grant access.
For entirely customer-managed user RBAC, set `cloud.defaultRbac.create=false`
and supply your own bindings. Kubernetes permissions are additive, not denies.

The [permission table](files/integration-read-baseline.yaml) records exact tuples
and intentional exceptions. See [the policy and upgrade guide](../../../docs/cloud-rbac-baseline.md)
for sensitive-resource exclusions, enterprise group bindings and rollout details.
An **installed chart upgrade** is needed to apply these grants; updating only the
KubeAtlas binary does not update RBAC. Missing new keys on an older
`--reuse-values` installation default to enabled; set explicit false before
upgrading if the added visibility is unwanted. Manage KubeAtlas upgrades through
Helm or GitOps.

### Legacy Cloud background-service RBAC (unsupported)

upstream Cloud's alerts worker and timeline puller read as `kubeatlas:system`.
`cloud.systemRbac` (default `true`) creates the chart's default read grant:
a chart-owned role aggregated to match `view`, the cluster-read and
integration-read add-ons, and cluster-wide `get/list/watch` on Secrets.
Helm release alerts and Secret changes in the timeline need Secret read;
Helm stores releases as Secrets. The grant includes no writes.

### Legacy Cloud Diagnose RBAC (unsupported)

Manual Diagnose turns read as the person who started them. upstream Cloud forwards
that person's identity and groups; `kubeatlas:ai:reader` is not involved. MCP clients
also read with the user's own permissions.

Automatic (alert-triggered) Diagnose runs use username `kubeatlas:ai:<org>` and
group `kubeatlas:ai:reader` only, a permanent read-only background diagnostic reader.
Never grant this group write permissions; a future write-capable AI identity
needs a separate opt-in group (e.g. `kubeatlas:ai:operator`).
`cloud.aiRbac` (default `true`) creates the chart's default grant: a chart-owned
role aggregated to match `view` plus the cluster-read and integration-read
add-ons. With the standard `view` role this grant includes no Kubernetes Secret
permission; it also inherits anything your cluster adds to `view` (ClusterRoles
labelled `rbac.authorization.k8s.io/aggregate-to-view`). It does include pod
logs and ConfigMaps, which may contain sensitive data.

Both values are independent of `cloud.defaultRbac`. They control only the
legacy chart grants and do not enable Cloud or Diagnose features in KubeAtlas.
False or absent creates no default grant and does not remove customer-created
bindings. To narrow the default grants, set the matching value
to false and supply your own read-only bindings for that group. With no grant,
the group has no cluster access; automatic Diagnose has no viewer fallback.

A plain `--reuse-values` upgrade from a chart predating these keys leaves them
absent and creates neither default grant. Set `cloud.aiRbac=true` and/or
`cloud.systemRbac=true` explicitly to create them. Changes made directly to
chart-managed bindings are restored on the next Helm upgrade.

Creating or updating these aggregated roles requires cluster-admin-equivalent
authority or `escalate` on ClusterRoles, in addition to normal chart installation
permissions. Restricted installers can set `cloud.aiRbac=false` and
`cloud.systemRbac=false` and supply their own read-only group bindings.

### Connecting to Argo CD (GitOps deep diff)

KubeAtlas's GitOps pages show a Git-rendered desired-vs-live diff when connected to
Argo CD's API. A self-hosted / in-cluster KubeAtlas can be given the token
declaratively so it survives pod restarts and needs no interactive Settings paste:

```bash
# A read-only Argo CD account token is enough:
#   argocd account generate-token --account kubeatlas
kubectl create secret generic kubeatlas-argocd-token -n kubeatlas \
  --from-literal=token=$ARGOCD_TOKEN \
  --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas --version "$VERSION" -n kubeatlas \
  --set argocd.existingSecret=kubeatlas-argocd-token
```

Leave `argocd.url` unset to auto-discover the in-cluster `argocd-server`, or set
it to pin an explicit endpoint. Providing a token makes the integration
**environment-managed**: read-only in the Settings UI, never written to disk.
Prefer `argocd.existingSecret` over the inline `argocd.token` so the token never
lands in the Helm release state. Rotation requires a pod restart. See
[docs/gitops.md](../../../docs/gitops.md#provisioning-the-token-per-deployment-shape).

### Connecting to Kubecost 3

Auto mode uses working OpenCost-compatible Prometheus metrics first, then a
Kubecost 3 Aggregator in the connected cluster. KubeAtlas tries the Aggregator's
named `tcp-api` port 9004 first. Without an API key, an authentication rejection
can fall back to the same Service's `tcp-api-rbac` port 9008 for SAML/OIDC-enabled
Kubecost. Configuring a key disables that bypass. A federated agent-only cluster
has no local Aggregator, so configure its central endpoint and cluster ID:

```bash
kubectl create secret generic kubeatlas-kubecost -n kubeatlas \
  --from-literal=api-key="$KUBECOST_API_KEY" \
  --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas --version "$VERSION" -n kubeatlas \
  --set cost.source=kubecost \
  --set cost.kubecost.url=https://kubecost.example.com/model \
  --set cost.kubecost.clusterId=production-a \
  --set cost.kubecost.existingSecret=kubeatlas-kubecost
```

The API key is optional; omit the Secret for an endpoint that intentionally
allows unauthenticated allocation reads. These values are environment-managed
and read-only in Settings. KubeAtlas reads current allocation and node costs plus
the retained cluster allocation trend from Kubecost. Workload and application
trend charts remain unavailable for Kubecost.

## Configuration

| Parameter | Description | Default |
|-----------|-------------|---------|
| `replicaCount` | Number of replicas | `1` |
| `image.repository` | Image repository | `ghcr.io/maomada/kubeatlas` |
| `image.tag` | Image tag | Chart appVersion |
| `service.type` | Service type | `ClusterIP` |
| `service.port` | Service port | `9280` |
| `basePath` | URL prefix KubeAtlas serves under, e.g. `/kubeatlas` for no-strip-prefix subpath ingress | `""` |
| `allowedHosts` | Hostnames KubeAtlas answers when `auth.mode` is `none`, besides `localhost`, IP addresses and Kubernetes Service names. `<service>.<namespace>`, `ingress.hosts`, `httpRoute.hostnames` and the hosts of `trustedOrigins` are added automatically; list others, such as a custom cluster domain. `*` turns the check off | `[]` |
| `trustedOrigins` | Origins besides KubeAtlas's own address that may use KubeAtlas's API, including changes and pod terminals, e.g. `http://kubeatlas.internal`. Set only if changes fail with "KubeAtlas refused this request" because a proxy rewrites `Host` | `[]` |
| `debug.image` | Image for ephemeral debug containers and node debug pods. In built-in restricted PodSecurity namespaces, pod debug containers may retry as the target/pod non-root UID, or UID `65532` by default; point at a compatible mirror for air-gapped / private-registry clusters. | `""` (busybox:latest) |
| `listPageSize` | Paginate the initial LIST of high-cardinality kinds (Pods, ReplicaSets) on very large clusters; `0` = off, try `2000`. Only used when the apiserver lacks WatchList streaming. | `0` |
| `ingress.enabled` | Enable ingress | `false` |
| `ingress.className` | Ingress class name | `""` |
| `httpRoute.enabled` | Enable Gateway API HTTPRoute | `false` |
| `httpRoute.apiVersion` | HTTPRoute API version override | `gateway.networking.k8s.io/v1` |
| `httpRoute.hostnames` | HTTPRoute hostnames | `[]` |
| `httpRoute.defaultTimeout` | Optional request timeout for generated default rule; empty uses Gateway deployment default | `""` |
| `httpRoute.rules` | HTTPRoute rules, passed through unchanged | `[]` |
| `timeline.storage` | Timeline storage (memory/sqlite/postgres) | `memory` |
| `timeline.retention` | Retention (Go duration; `0` disables). Applies to sqlite and postgres. | `168h` |
| `timeline.maxSize` | SQLite max DB + WAL size before oldest events are pruned (`0` disables). Not used for postgres. | `800Mi` |
| `timeline.postgres.existingSecret` | Name of a Secret holding the PostgreSQL DSN (required when `storage=postgres`) | `""` |
| `timeline.postgres.secretKey` | Key within the Secret holding the DSN | `dsn` |
| `persistence.enabled` | Enable PVC for SQLite | `false` |
| `cost.source` | Cost source: `auto`, `prometheus`, or `kubecost`; controls stay editable only when this and the Kubecost URL, cluster ID, and Secret are empty | `""` |
| `cost.currency` | Optional ISO 4217 override for OpenCost/Kubecost values; empty auto-detects, then uses USD | `""` |
| `cost.kubecost.url` | Kubecost 3 Aggregator URL; blank discovers local `tcp-api:9004` and may fall back to `tcp-api-rbac:9008` without a key; agent-only clusters need their central URL | `""` |
| `cost.kubecost.clusterId` | Cluster ID filter; blank detects literal `CLUSTER_ID` from the local FinOps Agent/Aggregator | `""` |
| `cost.kubecost.existingSecret` | Secret holding an optional Kubecost service-account API key; setting it disables automatic port-9008 auth bypass | `""` |
| `cost.kubecost.existingSecretKey` | Key within `cost.kubecost.existingSecret`; sent as `X-API-KEY` | `api-key` |
| `traffic.prometheusUrl` | Manual Prometheus/VictoriaMetrics URL (skips auto-discovery) | `""` |
| `traffic.prometheusHeaders` | HTTP headers sent with every Prometheus request (auth-protected backends). Requires `traffic.prometheusUrl` — credentials are never sent to auto-discovered endpoints | `{}` |
| `traffic.prometheusHeadersFromEnv` | Prometheus headers sourced from environment variables, for secret-backed auth headers. Requires `traffic.prometheusUrl` | `{}` |
| `traffic.prometheusSingleCluster` | Assert that the backend is dedicated to this cluster for workload metrics and PVC usage. Replaces automatic matching. [Scope limits](../../../docs/workload-metrics.md#optional-operator-override) | `false` |
| `traffic.prometheusClusterLabels` | Exact cluster label/value constraints for workload metrics and PVC usage, combined with AND. Labels must exist on each affected metric source. Alternative to `prometheusSingleCluster`; leave both unset for automatic matching | `{}` |
| `traffic.beylaJobSelector` | Beyla Live Traffic matcher fragment. Workload charts use it only with an explicit scope override and accept one `job` equality or regex matcher; custom workload jobs are normally discovered automatically | `""` |
| `argocd.existingSecret` | Name of a Secret holding the Argo CD API token (recommended — keeps it out of the release) | `""` |
| `argocd.existingSecretKey` | Key within `argocd.existingSecret` holding the token | `token` |
| `argocd.token` | Inline Argo CD API token (dev only — lands in the release state) | `""` |
| `argocd.url` | Explicit `argocd-server` URL; blank auto-discovers in-cluster | `""` |
| `argocd.insecureTls` | Skip TLS verification for a self-signed `argocd-server` | `false` |
| `mcp.execTools` | Opt in to `exec_pod` (`--mcp-exec-tools`); requires exec access. With auth off, every reachable client shares KubeAtlas’s ServiceAccount permissions. Ignored with `--disable-exec` or `--mcp-read-only` | `false` |
| `mcp.readOnly` | Expose only read-only MCP tools (`--mcp-read-only`); requires MCP enabled | `false` |
| `usageReporting.enabled` | Anonymous daily usage stats: views, actions and MCP tools used, plus this cluster's version, platform, node-count range and known integrations. Off unless `true`; KubeAtlas never asks in a cluster ([what is sent](../../../docs/usage-stats.md)) | unset |
| `resources.requests.cpu` | CPU request | `200m` |
| `resources.requests.memory` | Memory request | `256Mi` |
| `resources.limits.cpu` | CPU limit | `2` |
| `resources.limits.memory` | Memory limit. KubeAtlas sets `GOMEMLIMIT` to 85% of the container's cgroup limit at startup, so the GC collects harder as it approaches the limit; set `GOMEMLIMIT` under `env` to override | `1Gi` |

See `values.yaml` for all configuration options.

The self-hosted Cloud Hub, required Hub state file, OIDC settings, and its
separate chart are described in the [Cloud Hub guide](../../../docs/portal.md).

### Installation-owned settings (OSS)

Shared OSS Settings is read-only. Configure integrations through the values above,
audit policy through `audit.ignoredNamespaces` / `audit.disabledChecks`, and OCI
chart prefixes through `helm.ociSources`. `audit: null` preserves the default system
namespace exclusions; explicitly empty lists include all namespaces and checks.
The chart mounts a versioned, non-secret operator settings ConfigMap read-only and
rolls KubeAtlas when it changes. No runtime ConfigMap/Secret writes or extra RBAC are
required. External Secret rotation requires a restart.

Before upgrading, resupply any old UI-written integration settings as Helm values;
Pod-local files are not adopted as deployment configuration. Use matching chart
and image versions. Legacy Cloud configuration is unavailable in KubeAtlas;
no Cloud settings are loaded. See [installation settings](../../../docs/in-cluster.md#installation-settings)
for examples and run-mode behavior.

### Timeline storage: memory vs sqlite vs postgres

KubeAtlas's timeline records every cluster change so you can scrub backwards through "what happened, when." Three backends:

- **`memory`** (default): events live in-process. Lost on pod restart. Lower memory footprint per retention window than SQLite (no indexes, no WAL). Pick this if you only need recent activity (last few hours), don't care about losing history when a pod cycles, or want the simplest setup.
- **`sqlite`**: events persist to a PVC across restarts. Pick this if you want a multi-day audit trail, need to inspect changes that happened while you weren't looking, or run KubeAtlas in-cluster long-term. Adds operational concerns: the PVC will fill if retention is unbounded; restarting on a multi-GB DB is slower (more rows to load). Requires `persistence.enabled=true`.
- **`postgres`**: events persist in an externally managed PostgreSQL database. Pick this when you want history to survive pod restarts and rolling updates without attaching a PVC. It does not make KubeAtlas multi-replica — informer caches, SSE streams, and exec/port-forward sessions remain per-pod, so `replicaCount` must stay 1. Use a dedicated database per KubeAtlas deployment. The DSN must be provided via an existing Kubernetes Secret; Helm never touches the credential.

**Provider-agnostic PostgreSQL setup**:

The chart does not install, configure, or upgrade PostgreSQL. It creates no
PostgreSQL workload, Service, PVC, CRD, or credential Secret. Use any managed
database, PostgreSQL operator, or secret manager. The only requirement is a
Secret in KubeAtlas's namespace containing a PostgreSQL DSN under the key selected
by `timeline.postgres.secretKey`.

```bash
kubectl create secret generic kubeatlas-postgres -n kubeatlas \
  --from-literal=dsn='postgres://kubeatlas:password@postgres.example:5432/kubeatlas?sslmode=require' \
  --dry-run=client -o yaml | kubectl apply -f -
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas --version "$VERSION" -n kubeatlas \
  --set timeline.storage=postgres \
  --set timeline.postgres.existingSecret=kubeatlas-postgres \
  --set timeline.retention=720h
```

For an operator or secret manager that supplies a different key, reference that
key instead:

```yaml
timeline:
  storage: postgres
  postgres:
    existingSecret: app-db-credentials
    secretKey: uri
```

The Secret is managed independently of Helm, so credential rotation does not require a Helm upgrade. Restart the KubeAtlas Deployment after updating the Secret because Kubernetes does not refresh environment variables in running containers. The same applies to GitOps users: manage the Secret with SealedSecrets / SOPS / External Secrets and reference it via `timeline.postgres.existingSecret`.

**Sizing**: timeline volume depends on cluster size and controller churn. For sqlite, tune `timeline.retention`, `timeline.maxSize`, and `persistence.size` together. Set `timeline.retention=0` to disable age cleanup; keep `timeline.maxSize` enabled for in-cluster SQLite deployments so KubeAtlas prunes oldest events before the PVC fills. `maxSize` is ignored for postgres and memory.

`/api/diagnostics` surfaces `timeline.retentionAge`, `timeline.maxStorageBytes`, `timeline.lastCleanupAt`, `timeline.lastCleanupDeletedRows`, `timeline.lastCleanupError`, and `timeline.storageBytes` so you can confirm cleanup is keeping up without tailing logs.

## RBAC

The chart creates a ClusterRole with read-only access to common Kubernetes resources.

Grant changes take effect when the KubeAtlas pod restarts: KubeAtlas decides what it can
read at startup and keeps serving objects it already cached after a grant is
removed. `helm upgrade` rolls the pod whenever `rbac` values change. If you change
KubeAtlas's RBAC outside Helm, restart KubeAtlas.

### Default Permissions (Core K8s Resources)

Always granted (required for basic functionality):

| API Group | Resources |
|-----------|-----------|
| Core (`""`) | pods, services, configmaps, events, namespaces, nodes, pvcs, serviceaccounts, endpoints |
| `apps` | deployments, daemonsets, statefulsets, replicasets, controllerrevisions |
| `networking.k8s.io` | ingresses, networkpolicies |
| `batch` | jobs, cronjobs |
| `autoscaling` | horizontalpodautoscalers |
| `scheduling.k8s.io` | priorityclasses; feature-gated workloads, podgroups, compositepodgroups when served |
| `certificates.k8s.io` | podcertificaterequests, clustertrustbundles (stable in Kubernetes 1.37) |
| `apiextensions.k8s.io` | customresourcedefinitions (for CRD discovery) |

### Privileged Permissions (Opt-in)

Disabled by default for security:

| Feature | Value | Description |
|---------|-------|-------------|
| Secrets | `rbac.secrets: true` | View secrets in resource list. Also granted by `rbac.helm` (Helm stores releases in Secrets), by auth (`auth.mode` other than `none`), and in cloud mode, where every Secret read is re-checked against the requesting user's own RBAC. The grant is cluster-wide `get/list/watch` on all Secrets; `rbac.secrets: false` does not remove it in those modes. |
| Terminal | `rbac.podExec: true` | Shell access to pods |
| Port Forward | `rbac.portForward: true` | Port forwarding to pods. Also the fallback for traffic sources (Hubble/Caretta) — KubeAtlas dials the relay/metrics Service directly first, so in-cluster installs only need this when a NetworkPolicy or routing blocks KubeAtlas's namespace from reaching the service |
| Logs | `rbac.podLogs: true` | View pod logs (**enabled by default**) |
| Helm Write | `rbac.helm: true` | Install/upgrade/rollback/uninstall Helm releases. No-auth installs: grants KubeAtlas's ServiceAccount create/update/patch/delete on all resource types, since Helm runs as it. Under auth or cloud-mode Helm runs as the signed-in user, so the ServiceAccount gets no write grant; instead a split helm add-on ClusterRole is emitted — `kubeatlas-helm` (member-safe: CRDs, storage, namespaces) and `kubeatlas-helm-admin` (owner-only: RBAC, webhooks, ApiServices) |
| RBAC view | `rbac.viewRBAC: true` | Show ClusterRoles, ClusterRoleBindings, Roles, RoleBindings in the resource browser. Off by default — cache-served reads bypass per-user RBAC, so this exposes the cluster's authorization graph to every authenticated KubeAtlas user. Auto-enabled under auth or cloud mode (every read is re-checked per user there). |
| Webhooks view | `rbac.viewWebhooks: true` | Show MutatingWebhookConfigurations and ValidatingWebhookConfigurations in the resource browser. Off by default — the configurations reveal which admission controls are enforced (Gatekeeper / Kyverno policies, image scanners, DLP) and where the gaps are, which is recon value for a low-trust viewer. Auto-enabled under auth or cloud mode. |
| Node runtime evidence | `rbac.viewNodeRuntime: true` | Let upgrade-impact checks inspect kubelet metrics and effective configuration through `nodes/proxy`. Off by default because this exposes node-level runtime and configuration details to anyone who can reach a no-auth KubeAtlas install. Under auth, grant `get` on `nodes/proxy` to each Kubernetes identity that should inspect this evidence. |

### MCP container commands

`mcp.execTools` registers `exec_pod`; `rbac.podExec` grants KubeAtlas's ServiceAccount
exec permission. These are separate controls:

| `mcp.execTools` | `rbac.podExec` | Result |
|---|---|---|
| false | false | MCP exec unavailable; chart grants no SA exec access. |
| false | true | MCP exec unavailable; SA exec grant supports the existing terminal. |
| true | false | MCP exec registered; no SA exec grant from this option. It works only if the executing identity has permission from another grant. |
| true | true | MCP exec registered; SA exec grant present except in user-token mode. |

With auth off, execution uses the ServiceAccount, shared by every reachable
client. With proxy/OIDC impersonation, it uses the caller's Kubernetes permissions;
the SA grant does not grant the caller exec. In user-token mode, the chart emits
no SA exec rule and execution uses the forwarded caller token. The chart's SA
rule grants `create pods/exec` (SPDY); WebSocket also needs `get pods/exec`.
`--disable-exec`, read-only mode and disabled MCP suppress registration regardless
of either value. See [the exec contract](../../../docs/mcp.md#exec_pod).

### Cloud-triggered in-app upgrades (unsupported)

KubeAtlas has no Cloud-triggered upgrade UI or supported Cloud owner flow.
Manage chart and image upgrades through your normal Helm or GitOps process.

### CRD Access

KubeAtlas discovers CRDs in your cluster. Common CRD groups are enabled by default. Granting RBAC for CRDs that don't exist has no effect.

**Wildcard option:** Grant the collector service account read access to every API group and resource:
```bash
--set rbac.crdGroups.all=true
```
This grants `get/list/watch` on `*/*`, including **core Secrets** and all
credential-bearing CRDs. It overrides individual flags and resource exclusions,
including VictoriaMetrics exclusions. Kubernetes RBAC cannot express wildcard
reads minus Secrets. These are collector permissions; the Cloud caller roles
retain their finite grants.

| Option | API Groups |
|--------|------------|
| `antrea` | `crd.antrea.io` (`networkpolicies`, `clusternetworkpolicies`) |
| `apiRegistration` | `apiregistration.k8s.io` |
| `argo` | `argoproj.io` |
| `awsNetworkPolicy` | `networking.k8s.aws` (`applicationnetworkpolicies`, `clusternetworkpolicies`) |
| `awx` | `awx.ansible.com` |
| `calico` | `projectcalico.org`, `crd.projectcalico.org` |
| `certManager` | `cert-manager.io`, `acme.cert-manager.io` |
| `cilium` | `cilium.io` |
| `cloudnativePg` | `postgresql.cnpg.io`, `barmancloud.cnpg.io` |
| `clusterApi` | `cluster.x-k8s.io`, `infrastructure.cluster.x-k8s.io`, `controlplane.cluster.x-k8s.io`, `bootstrap.cluster.x-k8s.io`, `addons.cluster.x-k8s.io` |
| `contour` | `projectcontour.io` |
| `crossplane` | `crossplane.io`, `pkg.crossplane.io`, `apiextensions.crossplane.io`, `helm.crossplane.io`, `kubernetes.crossplane.io`. For Upbound provider groups (e.g. `s3.aws.upbound.io`, `compute.gcp.upbound.io`) use `additionalCrdGroups` — K8s RBAC has no apiGroup wildcards. |
| `descheduler` | `descheduler.alpha.kubernetes.io` |
| `envoyGateway` | `gateway.envoyproxy.io` |
| `externalDns` | `externaldns.k8s.io` |
| `externalSecrets` | `external-secrets.io` |
| `flux` | `*.toolkit.fluxcd.io` |
| `gatewayApi` | `gateway.networking.k8s.io` |
| `gcpMonitoring` | `monitoring.googleapis.com` |
| `grafana` | `monitoring.grafana.com`, `tempo.grafana.com`, `loki.grafana.com`, `grafana.integreatly.org` |
| `istio` | `networking.istio.io`, `security.istio.io` |
| `karpenter` | `karpenter.sh`, `karpenter.k8s.aws`, `karpenter.azure.com`, `karpenter.k8s.gcp`, `eks.amazonaws.com` |
| `keda` | `keda.sh` |
| `knative` | `serving.knative.dev`, `eventing.knative.dev`, `sources.knative.dev`, `messaging.knative.dev`, `flows.knative.dev`, `networking.internal.knative.dev` |
| `kubeshark` | `kubeshark.io` |
| `kured` | `kured.io` |
| `kyverno` | `kyverno.io`, `policies.kyverno.io`, `wgpolicyk8s.io`, `reports.kyverno.io`, `openreports.io` |
| `mariadb` | `mariadb.mmontes.io` |
| `networkPolicyApi` | `policy.networking.k8s.io` |
| `nginx` | `nginx.org` |
| `openshift` | `observability.openshift.io` |
| `opentelemetry` | `opentelemetry.io` |
| `prometheus` | `monitoring.coreos.com` |
| `reflector` | `reflector.v1.k8s.emberstack.com` |
| `reloader` | `reloader.stakater.com` |
| `sealedSecrets` | `bitnami.com` |
| `strimzi` | `strimzi.io`, `kafka.strimzi.io` |
| `tekton` | `tekton.dev` |
| `traefik` | `traefik.io`, `traefik.containo.us` |
| `trivy` | `aquasecurity.github.io` |
| `velero` | `velero.io` |
| `verticalPodAutoscaler` | `autoscaling.k8s.io` |

**VictoriaMetrics collection:** the chart grants KubeAtlas cluster-wide `get/list/watch`
on VMAgent, VMCluster and VMSingle (`vmagents`, `vmclusters`, `vmsingles` in
`operator.victoriametrics.com`) whenever it grants cluster-wide Secrets reads:
`rbac.secrets: true`, `rbac.helm: true`, auth enabled (`auth.mode != none`), or
cloud mode. These objects can carry inline credentials, so they share the same
grant decision as Secrets. There is no separate VictoriaMetrics setting.

This rule excludes all other VictoriaMetrics operator kinds, including
VMUser/VMAuth, alerting and scrape/probe resources, and the Logs, Traces,
Distributed and Anomaly families. `crdGroups.all=true` or adding
`operator.victoriametrics.com` to `additionalCrdGroups` grants them anyway;
permissions are additive, and these exclusions are not deny rules.

**Disable groups:** `--set rbac.crdGroups.calico=false`

**Add unlisted CRDs:**
```yaml
rbac:
  additionalCrdGroups:
    - mycompany.io
```

### Troubleshooting: "Failed to list resource" Warnings

If you see these warnings, KubeAtlas discovered a CRD but doesn't have RBAC access. This is **not an error** — add the API group to `additionalCrdGroups` if you need it.

### Advanced: Custom Rules

For fine-grained control, use `additionalRules` to add arbitrary RBAC rules:
```yaml
rbac:
  additionalRules:
    - apiGroups: ["custom.example.com"]
      resources: ["myresources"]
      verbs: ["get", "list", "watch"]
    - apiGroups: [""]
      resources: ["pods"]
      verbs: ["delete"]  # Dangerous - use with caution
```

### Capability Detection

KubeAtlas uses its ServiceAccount permissions to access the Kubernetes API. The UI automatically detects which features are available based on RBAC and hides unavailable features (e.g., the terminal button won't appear if `podExec` is disabled).

## Uninstalling

```bash
helm uninstall kubeatlas -n kubeatlas
kubectl delete namespace kubeatlas
```

## Self-hosted Hub Agent

When `hub.enabled=true`, the organization-scoped
`kubeatlas:hub:owner`, `kubeatlas:hub:member`, and
`kubeatlas:hub:viewer` groups map to Kubernetes `admin`, `edit`, and
`view`. All three groups also receive a limited `get`/`list`/`watch` role for
nodes, namespaces, storage classes, PVs, and PVCs. The owner group is not
cluster-admin; only the exact `hub.ownerUserID` local `User` binding receives
cluster-admin. These mappings do not enable legacy `cloud.enabled`.

See [Hub-hosted Agent onboarding](../../../docs/hub-agent-onboarding.md) for the private local bundle, bootstrap, and trust model. Agent mode is a separate `hub` values block; it does not re-enable the legacy `cloud.enabled` contract. It requires a pinned `image.digest`, the Hub origin's `kubeatlas/agent` repository, a per-cluster token Secret mounted as a file, proxy authentication, and MCP. The chart binds cluster-admin only to the exact `hub.ownerUserID` `User`; organization role groups receive only their mapped role and limited infrastructure-read bindings.

An existing Agent deployment needs an operator-controlled Helm rollout to
install or update these role bindings. The Hub does not remotely change Agent
Secrets or RBAC. Target nodes must reach the Hub OCI endpoint and trust its TLS
certificate independently of the optional Agent CA file. The default installer
refuses adoption and requires a new namespace/release.
