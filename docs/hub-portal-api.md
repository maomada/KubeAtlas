# Portal Hub identity, management and MCP credentials

The Cloud-only Portal requires a valid `HubConfig`; construction without Hub
state fails. This document describes the self-hosted Hub API and live MCP
transport.

## Identity and request boundaries

The local configured account remains the immutable break-glass owner. Optional
OIDC uses an independently operated Keycloak-compatible provider with the
authorization-code flow, S256 PKCE, nonce, issuer/audience/signature/expiry
verification, opaque server-side sessions and a fixed callback URL. New OIDC
identities are pending with no access until an owner or administrator approves
them. Identity keys hash the exact issuer and subject; usernames and email are
not account-linking keys. Only validated display names are retained.

Hub sessions are owner-bound and every request reloads current member status,
role, cluster scope and Kubernetes groups. Pending members may read Hub identity
information only. Disabled members are rejected. The local owner is synthetic
and cannot be changed through member management. An Authorization header is
rejected on cookie management endpoints; bearer credentials cannot administer
the Hub.

MCP requests and the credential `current`/`refresh` endpoints require a single
`Authorization: Bearer …` header. Cookies never substitute for an invalid or
missing bearer. Tokens in URLs are rejected. Authentication is repeated on each
HTTP request, independent of MCP session IDs. Revocation does not undo an already
dispatched operation. Credentials are removed from request headers before the
MCP factory receives the authenticated principal.

The local credential owner remains `portal:<SHA-256 of configured username>`.
OIDC credential owners use the opaque `oidc:<SHA-256>` member ID. Kubernetes
impersonation usernames are stored per member and `/api/hub` reports the current
username and groups for explicit Kubernetes role bindings. Viewer members are
read-only in browser and MCP routes. Member and viewer cluster scope is explicit;
an empty scope means no clusters. Administrator scope covers all configured
clusters. Hub roles and scopes do not grant Kubernetes RBAC.

The browser POSTs `/api/traffic/connect-managed` through its selected Agent when
the Hub session is writable. The Agent independently requires a fresh
Kubernetes SubjectAccessReview permitting all resource actions cluster-wide.
This is a shared integration connection using Agent credentials, so
namespace-only operators cannot initiate it; missing identity, refusal and
failed checks all stop before connection work. Callers cannot supply an endpoint
or credentials in this request. Existing namespace filtering still governs flow
reads. This does not enable general remote host port-forward management or
traffic-source configuration writes.

Use a matching Hub and Agent/UI version for this capability. The Agent retains
`POST /api/traffic/connect` as a local compatibility alias, but the Hub does not
proxy that legacy route. A newer UI calling the managed route against an older
Agent receives 404; there is no fallback to the legacy route.

The local owner's groups come only from trusted server configuration. OIDC
member groups are administrator-managed and validated against the existing
`radar:*`/`cloud:*` vocabulary; IdP roles and groups never grant access. No
Kubernetes RBAC binding is created automatically. PAT scope and read-only mode
further constrain the source identity's existing access.

Hub management and MCP routes require loopback or an exact configured `MCPAllowedHosts` authority.
Forwarded Host headers do not authorize another authority. Foreign, malformed or
duplicate Origins and cross-site browser requests are rejected. TLS termination
at a trusted proxy is supported; use HTTPS for credentials outside an isolated
trusted network. `/agent` also applies the tunnel's stricter no-Origin policy.

OIDC callbacks accept the provider's cross-site navigation only through the
browser-bound, one-use state transaction and the fixed configured callback.

## Directory API (Portal cookie)

| Method and path | Body / result |
| --- | --- |
| `GET /api/auth/providers` | `{local:true, oidc:{enabled, loginUrl?}}` |
| `GET /api/auth/oidc/login` | Fixed-provider authorization redirect with `prompt=login`, state, nonce and S256 PKCE |
| `GET /api/auth/oidc/callback` | One-use browser-bound state callback; redirects to `/` or a generic safe login error |
| `GET /api/hub` | Existing mode/readiness fields plus the current `member` |
| `GET /api/hub/organization` | `{id:"self-hosted", name, memberCount}`; active members only count |
| `PUT /api/hub/organization` | `{name}`; owner/admin only |
| `GET /api/hub/members` | All members, including the synthetic local owner; owner/admin only |
| `PUT /api/hub/members/{urlEncodedID}` | `{role,status,clusterIds,kubernetesGroups}`; owner/admin only |
| `GET /api/hub/sso` | Provider metadata and approval provisioning; no client secret |
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

Members and viewers see only their explicit cluster IDs in directory and group
lists. Only active administrators and the local owner may mutate directory,
member or organization state. A member authorization change atomically revokes
that member's PATs and cancels its local Portal sessions.

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
| MCP bearer | `/mcp` | Live MCP transport; no cookie-as-bearer fallback |

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
