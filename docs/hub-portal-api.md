# Portal Hub management and MCP credentials

`portal.Credentials.Hub` explicitly enables this backend integration. A nil
configuration preserves the legacy Portal. This document describes the backend
contract, not a deployed Hub or a finished browser management workspace. CLI and
live MCP facade wiring are separate. Without an MCP factory, Hub-mode
`/mcp-fleet` returns `live_mcp_unavailable`; it never falls back to snapshot tools.

## Identity and request boundaries

The current Portal has **one configured administrator**, not an IdP or multi-user
directory. Its existing login cookie is required for all management endpoints.
An Authorization header is rejected on those endpoints, including when a cookie
is also present. Agent credentials and MCP credentials cannot administer the Hub.

MCP requests and the credential `current`/`refresh` endpoints require a single
`Authorization: Bearer …` header. Cookies never substitute for an invalid or
missing bearer. Tokens in URLs are rejected. Authentication is repeated on each
HTTP request, independent of MCP session IDs. Revocation does not undo an already
dispatched operation. Credentials are removed from request headers before the
MCP factory receives the authenticated principal.

The credential owner is `portal:<SHA-256 of configured username>`. The Kubernetes
impersonation username is `kubeatlas:portal:<same digest>`; `/api/hub` reports it
for explicit Kubernetes role bindings. Changing the configured username makes
previous grants unusable by that owner. Password changes and Portal restarts do
not revoke independent PATs; revoke them explicitly when required.

Optional groups come only from trusted server configuration, must use the source
Agent's `radar:*`/`cloud:*` vocabulary, and default to none. The client cannot
choose its impersonated username or groups. No Kubernetes RBAC binding is created
automatically. PAT cluster scope and read-only mode further constrain the source
identity's existing access; they do not grant Kubernetes access themselves.

All new routes require loopback or an exact configured `MCPAllowedHosts` authority.
Forwarded Host headers do not authorize another authority. Foreign, malformed or
duplicate Origins and cross-site browser requests are rejected. TLS termination
at a trusted proxy is supported; use HTTPS for credentials outside an isolated
trusted network. `/agent` also applies the tunnel's stricter no-Origin policy.

## Directory API (Portal cookie)

| Method and path | Body / result |
| --- | --- |
| `GET /api/hub` | Mode, MCP readiness, source username and configured groups |
| `GET /api/hub/clusters` | `{clusters, serverTime}` with persistent metadata and current connection status |
| `POST /api/hub/clusters` | Cluster input → one-time `{cluster, token}` |
| `PUT /api/hub/clusters/{id}` | Full cluster input, including the unchanged ID |
| `POST /api/hub/clusters/{id}/rotate` | `{}` → one-time replacement Agent token |
| `POST /api/hub/clusters/{id}/revoke` | `{}` → 204 |
| `GET /api/hub/groups` | `{groups}` |
| `POST /api/hub/groups` | Save `{id, name, clusterIds}` |
| `PUT /api/hub/groups/{id}` | Save with unchanged ID |
| `DELETE /api/hub/groups/{id}` | 204; no request body needed |

Cluster input contains `id`, `name`, `description`, `environment`, and `region`.
Directory entries are `configured: true` with `state` equal to `offline`,
`connected`, or `revoked`. A connected entry includes the current Agent session
metadata. Connection status is an observation, not cluster health. Groups are
organizational metadata, not implicit credential scopes; an empty group does not
mean all clusters. PAT issuance always names explicit cluster IDs.

Agent rotation/revocation is persisted **before** disconnecting the old session
and invalidating in-progress authentication. A failed persistence operation does
neither. Restart Agents with the new token file after rotation. IDs are never
deleted or reused. Revoked Agent credentials can be replaced by explicit rotation.

## Credential API

| Authentication | Method and path | Body / result |
| --- | --- | --- |
| Portal cookie | `GET /api/mcp/credentials` | `{credentials, serverTime}`, metadata only |
| Portal cookie | `POST /api/mcp/credentials` | `{label, clusterIds, readOnly, ttlSeconds}` → one-time `{credential, token}` |
| Portal cookie | `POST /api/mcp/credentials/{id}/rotate` | `{ttlSeconds}` → replacement credential/token |
| Portal cookie | `POST /api/mcp/credentials/{id}/revoke` | `{}` → 204 |
| MCP bearer | `GET /api/mcp/credentials/current` | `{credential, status: "active", serverTime}` |
| MCP bearer | `POST /api/mcp/credentials/refresh` | `{ttlSeconds}` → replacement credential/token |
| MCP bearer | `/mcp-fleet` | Live MCP factory; no cookie or snapshot fallback |

Issuance requires an explicit boolean `readOnly`, a nonempty explicit cluster
scope, and known cluster IDs. Wildcards are rejected. An omitted/zero TTL uses the
store default; explicit TTLs range from 15 minutes to seven days. Refresh/rotation
retain scope and read-only mode, revoke the previous token, and cannot extend the
original refresh family beyond 30 days. See [Hub state](hub-state.md).

Body-bearing operations require `application/json`, at most 32 KiB, one object,
exact supported field names and no duplicate fields. Responses are `no-store` and
errors carry `error_code`, `message`, `requestId`, and `retryable`. Invalid,
expired, revoked, and refresh-exhausted bearer credentials are distinguishable.
Management of an unknown credential returns 404; attempting to rotate an inactive
credential returns 409 without falsely implying the Portal session expired.

Tokens appear only in successful issuance/rotation/refresh responses, never in
list responses. The store persists only their digests. Save token values through
a secret manager or protected file, not source code, logs, or chat. MCP refresh
is not OAuth and does not imply an automatic OpenCode refresh integration.
