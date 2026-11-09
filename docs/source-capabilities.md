# Source capabilities and optional snapshot collection

The native read-only MCP tool `get_capabilities` takes an empty object. It reads
cached metadata only: no discovery refresh, configuration refresh, data query,
permission probe, registry request, cost-source selection or lease callback.
The live source/Hub path remains primary. This change does not make periodic
snapshots a real-time fallback or add any Hub/Portal endpoint.

## Response

The result contains:

- `capabilities`: the existing `fleetsync.SourceCapabilities` shape, including
  source `observedAt`, `mcpEnabled`, `readOnly`, `execEnabled`,
  `allowedOperations`, dependency states and limitations;
- `collection`: the existing `fleetsync.CollectionCoverage` shape describing
  **optional snapshot-collection support**, not a prohibition on direct/native
  live tools (including metrics tools);
- `pagination`: `maxLimit: 200`, `cursorLifetimeSeconds: 600`,
  `consistency: "live_non_atomic"`, matching native `list_resources`.

The usual wrapper also supplies `_meta["kubeatlas/observedAt"]` at source tool
completion. Neither timestamp certifies informer freshness or dependency health.

`mcpEnabled` is true because this native tool is registered. `readOnly` and
`execEnabled` describe this server's actual registered tools, captured at
registration rather than inferred from later mutable configuration. The read-only
mount exposes no write or exec tools; a full mount still needs both its exec
registration option and the process exec opt-in. `allowedOperations` names exposed
classes (`read`, `write`, `exec`), **not grants to the caller**. Every read, write
and exec still needs per-request Kubernetes RBAC. No namespace permissions were
evaluated. This is source runtime metadata, not a cluster-global inventory.

## Cached dependencies, not availability guarantees

| Dependency | Meaning |
| --- | --- |
| `prometheus` | `connected` means a cached connection; `discovering` means an existing discovery is in flight. Nil/disconnected is `unknown`, not proof Prometheus is absent. No data probe. |
| `costs` | Cached safe state/reason from `opencost.StatusSnapshot()`, with a source enum when known. `selected` means selection only, not cost-data availability. Forced Prometheus selection does not verify cost metrics. |
| `logs` | `conditional`: needs a live Pod and the caller's `pods/log` RBAC. |
| `imageMetadata` | `conditional`: needs registry reachability and identity-permitted credentials. No registry/permission probe. |
| `runtimeVersions` | `conditional`: OCI image declarations, not proof of installed/running binaries. Missing declarations remain unknown. Node identity registry credentials are not used. The source runtime-versions tool's evidence basis is reused verbatim. |

The cost status reader holds the manager's read lock and reads only selection
metadata. A fresh cached `ErrNoCostSource` becomes
`unavailable/no_cost_source`; other fresh failures become
`unavailable/cost_source_unavailable`. Environment configuration errors become
`unavailable/invalid_configuration`. Expired cached failures become
`unknown/cached_failure_expired`. An untouched configuration is
`unknown/not_probed`. The selected state uses
`selection_not_data_availability`. Failure expiry uses the existing retry
timestamp; reading status does not initiate a retry.

No dependency response includes URLs, API keys, cluster IDs, arbitrary error
text, image names or credentials. Source values are restricted to the known
`auto`, `prometheus` and `kubecost` enums; unknown values are omitted.

## One collection policy

Both the capability report and optional `CaptureFleetSnapshot` use the same
selection policy. It consumes a copied, non-refreshing discovery catalog and
the preferred entry resolved by the same group-aware lookup as native reads.
The policy never merges verbs across served versions. Group collisions remain
distinct. Shared discovery slices and verbs are not mutated.

Snapshot resource inventories require **get, list and watch**. Known missing
verbs are reported as `missing_verbs:<comma-separated verbs>` and skipped,
including watchless componentstatuses and metrics APIs. Skips do not produce
repeated error documents. Secrets and subresources are excluded entirely.

If discovery is complete, an absent built-in API is skipped as `api_not_served`:
for example, DRA kinds on a cluster that does not serve them. Its version is the
built-in version when fixed, otherwise empty rather than fabricated. If discovery
is incomplete or absent, absence is unknown: legacy built-in attempts remain,
and limitations explicitly identify incomplete collection knowledge. Explicit
missing verbs can still exclude a known entry. A discovered kind whose preferred
entry cannot be resolved is omitted with an unknown-support limitation, not
declared absent or forbidden.

`unsupportedKinds` records are unique and sorted by group/version/resource,
capped at 100. Truncation is explicit; all unsupported inventories are still
skipped, including ones beyond the metadata cap. `discoveryComplete` describes
cached discovery only, not collection success, permissions or snapshot atomicity.

Snapshots populate `coverage` and append its limitations. They retain canonical
resource payloads, secret exclusion, cancellation and cluster-binding checks.
`capabilities` remains unset on snapshots: capture does not know the actual
native MCP registration options and must not invent a mode. Existing document,
byte and detail limits remain unchanged. Incomplete-discovery fallback attempts
can still produce canonical read errors; they are not known-unsupported skips.

## Local verification

Unit tests use fake discovery/cache state, SDK in-memory transport and a fixed
cost-status clock; these are not real-cluster, browser or Actions acceptance.
The single human catalog entry is checked by existing Go catalog-parity tests.
The previously recorded unavailable Vitest dependency environment is unchanged;
no frontend bootstrap or GUI changes are part of this unit.

```sh
go test ./internal/mcp -run 'Capabilities|CollectionPlan|CaptureFleetSnapshot|ToolCatalog|SetupDialog'
go test ./internal/opencost -run 'StatusSnapshot'
go test -race ./internal/mcp ./internal/opencost -run 'SourceCapabilities|CollectionPlan|StatusSnapshot'
go vet ./internal/mcp ./internal/opencost
(cd pkg && go test ./investigation -run '^TestReadOnlyToolsExcludeWrites$')
go test ./internal/mcp
```
