# Native source capabilities

The native read-only MCP tool `get_capabilities` takes an empty object and
reports cached metadata about this KubeAtlas source runtime. It performs no
discovery refresh, configuration refresh, data query, permission probe, or
network request.

The response has two parts:

- `capabilities`: source observation time, MCP/read-only/exec mode, exposed
  operation classes, cached dependency states, and limitations;
- `pagination`: `maxLimit: 200`, `cursorLifetimeSeconds: 600`, and
  `consistency: "live_non_atomic"`, matching native `list_resources`.

`mcpEnabled` is true because the tool is registered. `readOnly` and
`execEnabled` describe the server's registered tools, captured at registration
rather than inferred from later mutable configuration. `allowedOperations`
lists exposed classes (`read`, `write`, `exec`), not caller authorization
grants. Every operation still requires per-request Kubernetes RBAC.
The standard wrapper also supplies `_meta["kubeatlas/observedAt"]` at tool
completion; neither timestamp certifies informer freshness or dependency health.

## Cached dependencies, not availability guarantees

| Dependency | Meaning |
| --- | --- |
| `prometheus` | `connected` means a cached connection; `discovering` means an existing discovery is in flight. Nil or disconnected is `unknown`, not proof Prometheus is absent. No data probe is made. |
| `costs` | Cached safe state and reason from `opencost.StatusSnapshot()`, with a source enum when known. `selected` describes source selection, not cost-data availability. |
| `logs` | `conditional`: needs a live Pod and the caller's `pods/log` RBAC. |
| `imageMetadata` | `conditional`: needs registry reachability and identity-permitted credentials. No registry or permission probe is made. |
| `runtimeVersions` | `conditional`: OCI image declarations, not proof of installed or running binaries. Missing declarations remain unknown. |

Cached status does not initiate retries or discovery. Dependency results do not
include URLs, API keys, cluster IDs, arbitrary error text, image names, or
credentials. Source values are restricted to the supported enums.

## Verification limits

The capability tool describes one source runtime, not a cluster-global
inventory or health check. `get_capabilities` unit tests cover registered
read-only/exec modes, dependencies, and pagination; they do not establish
real-cluster, browser, or Hub acceptance.
