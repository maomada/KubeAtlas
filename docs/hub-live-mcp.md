# Live Hub MCP facade

`internal/hubmcp.New` implements the Portal `HubConfig.MCPFactory` seam. It
requires the Portal's exact Host/Origin policy and a freshly authenticated PAT
principal on **every** HTTP request. Constructing this package is not evidence
that a deployed Portal has enabled Hub mode. Legacy standalone/snapshot mode is
unchanged; this facade never reads snapshots or falls back to their tools.

## Opt-in Portal startup

Run `kubeatlas-portal --hub-state-file ./portal-data/hub/state.json` with the
existing `KUBEATLAS_PORTAL_USERNAME` and `KUBEATLAS_PORTAL_PASSWORD` supplied
through your secret manager. `KUBEATLAS_PORTAL_HUB_STATE_FILE` is the equivalent
environment setting. Without this option, standalone Portal behavior is unchanged.
Startup rejects collisions among active `--data-file`, `--hub-state-file` and
`--fleet-data-file` paths before opening any store. Standalone mode uses
`<registry-directory>/fleet/state.json` when `--fleet-data-file` is omitted;
Hub-only mode has no Fleet store unless that flag is explicitly supplied.
Preflight resolves absolute paths and existing symlink aliases, including a
symlinked parent with a not-yet-created state file, and also detects existing
same-file aliases. Unresolvable paths fail closed without creating or changing
state. Paths containing `..` are rejected because state stores normalize paths
differently. Store-specific file and permission checks still apply, including
Hub state-file symlink rejection. This startup check does not protect against
privileged concurrent filesystem replacement. The state store persists token
digests, not bearer values.

Hub-only mode needs neither `KUBEATLAS_PORTAL_TOKENS` nor a Fleet snapshot store.
An explicitly supplied `--fleet-data-file` opens optional legacy storage; it does
not change the live-only `/mcp-fleet` route or permit Cookie authentication there.
Configure external proxy authorities with `--mcp-allowed-hosts`; keep public
traffic on HTTPS and preserve the Host/Origin checks.

`--hub-groups` (or `KUBEATLAS_PORTAL_HUB_GROUPS`) supplies comma-separated
Kubernetes identity groups for the existing single Portal operator. It grants no
Kubernetes permissions by itself: create appropriate, explicit RBAC bindings in
each cluster. The user identity is `kubeatlas:portal:<SHA-256(username)>`.
Changing the configured username changes that identity and PAT ownership. No
cluster-admin binding, OAuth issuer, multi-user organization policy, enrollment
key exchange or Cloud-style management UI is provisioned by these flags.

Use the Hub management APIs to configure clusters and issue scoped, expiring
PATs. MCP clients send `Authorization: Bearer <PAT>` to `/mcp-fleet`; browser
login Cookies remain management-only. Do not reuse legacy registration tokens
or Portal Cookies as MCP credentials. Real-cluster acceptance is still required
before deploying this candidate over an existing trial environment.

## Tools

| Tool | Scope and result |
| --- | --- |
| `list_clusters` | PAT scope intersected with optional `fleetId` Hub group, or one `clusterId`; directory state is not Kubernetes health. |
| `list_cluster_tools` | At most eight scoped clusters, compact summaries, identical catalogs referenced by hash; failures remain visible per cluster. |
| `get_tool_schema` | One source definition, including its original schema and annotations; optional `catalogHash` rejects changed definitions. |
| `call_cluster_tool` | One explicit cluster and native tool, never broadcast; original source result retained under `data`. |
| `get_capabilities` | Calls the selected source's lightweight native capability tool; older sources report `tool_not_found`, not invented capabilities. |

Directory/catalog pages default to 20, accept 1–100, and return `limit`,
`returned`, `matchingAtRead`, `truncated`, `nextCursor`, and
`consistency: live_non_atomic`. Tool descriptions are capped at 160 characters
and explicitly flag shortening. Catalog summaries omit full schemas. Signed
cursors bind credential, selection, page size, connection/catalog versions and
a ten-minute lifetime from the first page. Restarting the Hub invalidates them.
Every request still authenticates and checks scope; a cursor grants no access.

Catalog results expose `partial` and `respondedClusters`, plus each cluster's
error when offline, denied or unsupported. Zero responding sources is an error,
not an empty successful inventory. Source discovery is performed anew rather
than retaining cluster manifests in the Hub.

`list_resources` calls require source pagination support and default to a
source-side page of 50, before resource enrichment. Explicit limits must be
1–200. Pass the source's cursor inside `arguments`; it is separate from Hub
directory/catalog cursors. Legacy snapshot `get_agent_context` and
`get_resource_context` are not live Hub tools. Use scoped native reads rather
than downloading the snapshot context bundle.

## Provenance and failure semantics

Every facade tool result is structured JSON and equivalent JSON text. A native
call adds `clusterId`, `tool`, `source: live`, `connectionId`, `agentVersion`,
`observedAt`, `receivedAt`, `durationMs`, `requestId`, and `data`. `data` retains
the original MCP result, including content, structured content and metadata.
`observedAt` comes only from the source's `kubeatlas/observedAt` result metadata;
it is null when absent or invalid. Hub receipt time is separate, never a
replacement observation timestamp. Directory results use `source: directory`.

Failures expose `error_code`, `message`, `retryable`, `next`, and the same
correlation fields. Known source read-reason codes are preserved; unclassified
source errors use `source_tool_error`, retaining the original result. MCP wire
syntax/transport errors retain the protocol's own error semantics. Arguments,
payloads and bearer values are not copied into Hub audit log lines; only the
credential ID, target, result code, timing and correlation ID are logged. These
logs are not a separately durable operation journal or a retention guarantee.

The facade has a 150-second whole-request budget, 1 MiB incoming request limit,
512 KiB native argument limit, 1 MiB per-source-response/catalog budget, and
256 KiB serialized MCP tool-result limit (including both JSON representations).
Oversized results fail explicitly; bytes are not silently cut into invalid JSON
and a fabricated continuation is never returned. Tools with larger results need
native filters or smaller pages. Operations needing longer than this budget can
time out after having started; the budget does not roll back accepted changes.

Read-only PATs use only `/mcp-readonly`. A writable PAT can initialize that route
when the source's full `/mcp` route returns 404 during initialization. This is
endpoint selection, not a retry of a tool or fallback to snapshot data. No
source tool call is retried automatically. Tool annotations are advisory: calls
through the writable endpoint conservatively report an unknown outcome on
transport failure, source error or oversized output. A reported result is not
proof that a long-running operation completed; inspect its native payload.
Reconcile the target before repeating a possibly mutating call.

The source still enforces per-user Kubernetes authorization for reads and
writes. PAT scope, directory visibility and catalog metadata are not RBAC
grants. Protocol fixtures prove routing and boundaries, not real-cluster RBAC,
browser behavior or production readiness.
