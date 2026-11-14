# Portal Hub identity, management and MCP credentials

The Cloud-only Portal requires a valid `HubConfig`; construction without Hub
state fails. This document describes the self-hosted Hub API and live MCP
transport.

## Identity and request boundaries

The configured recovery login is a persisted local account and organization
owner membership. The platform-users API exposes its public ID alias as `local`;
it is not a synthetic directory-only member. Optional OIDC uses an independently
operated Keycloak-compatible provider with the authorization-code flow, S256
PKCE, nonce, issuer/audience/signature/expiry verification, opaque server-side
sessions and a fixed callback URL. New OIDC identities are pending accounts
without organization memberships until a platform owner approves them and
grants initial membership. Identity keys hash the exact issuer and subject;
usernames and email are not account-linking keys. Only validated display names
are retained.

Account statuses are `pending`, `active`, and `disabled`. Platform owners manage
account status and organization memberships through platform-user management.
Organization owners may change the role of an existing membership or remove
that membership; they cannot approve pending accounts, change account status,
or set per-user cluster/group lists. Public organization roles are only
`owner`, `member`, and `viewer`.

Hub sessions are account-bound, organization-bound and revalidate current
account and membership state on each request. `GET /api/hub` reports
`activeOrganizationId: null` when no organization is active; the member's
`role` is omitted until an organization role is resolved. New memberships have
the cluster scope of their organization. Only migrated `LegacyRestricted`
memberships retain legacy explicit cluster and Kubernetes-group restrictions;
an empty legacy cluster scope grants none. Viewer memberships are read-only.
Both active and no-active-organization responses include the required
`authorizationRevision` as a canonical decimal string from the same account or
member snapshot as the response. The internal member object does not expose
this field. It identifies authority-history changes for narrow client-side
draft invalidation; it does not grant access or identify a session.
The server derives `kubeatlas:hub:owner`, `kubeatlas:hub:member`, or
`kubeatlas:hub:viewer` from the active organization role. Client-supplied group
and organization headers do not set authority, and there is no arbitrary
Kubernetes-group assignment UI for new memberships.

An Authorization header is rejected on cookie management endpoints; bearer
credentials cannot administer the Hub. Pending accounts may read limited Hub
identity information only, while disabled accounts cannot authenticate.

MCP requests and the credential `current`/`refresh` endpoints require a single
`Authorization: Bearer …` header. Cookies never substitute for an invalid or
missing bearer. Tokens in URLs are rejected. Authentication is repeated on each
HTTP request, independent of MCP session IDs. Revocation does not undo an already
dispatched operation. Credentials are removed from request headers before the
MCP factory receives the authenticated principal.

The local credential owner remains `portal:<SHA-256 of configured username>`.
OIDC credential owners use the opaque `oidc:<SHA-256>` member ID. `/api/hub`
reports the current public member and server-derived groups. Hub roles do not
create Kubernetes RBAC by themselves; the operator must apply the Agent chart's
role-group bindings. New organization roles receive organization-wide cluster
scope, while migrated restricted memberships preserve their legacy scope.

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

OIDC claims and client headers never grant roles, cluster scope, or Kubernetes
groups. The Hub does not automatically change remote RBAC or Agent token
Secrets. PAT scope and read-only mode further constrain the source identity's
existing access.

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
| `GET /register` | Public account-registration page; registration creates a pending account without membership or session |
| `GET /settings/platform` | Platform-owner-only settings page |
| `GET /api/auth/providers` | `{local:true, registrationEnabled:true, oidc:{enabled, loginUrl?}}` |
| `GET /api/auth/oidc/login` | Fixed-provider authorization redirect with `prompt=login`, state, nonce and S256 PKCE |
| `GET /api/auth/oidc/callback` | One-use browser-bound state callback; redirects to `/` or a generic safe login error |
| `GET /api/hub` | Readiness and identity; required decimal-string `authorizationRevision`, nullable `activeOrganizationId`, public organizations and current member when resolved |
| `GET /api/hub/organizations` | `{organizations:[{id,name,role}]}`; public roles are owner/member/viewer |
| `POST /api/hub/organizations` | Platform owner: `{name,ownerMemberId}` → `{id,name,role:"owner"}` |
| `POST /api/hub/organization/select` | `{organizationId}` → 204; selects an organization in the current session |
| `GET /api/hub/platform/users` | Platform owner: raw `{users:[...]}` envelope; local recovery ID is `local`; no password hashes |
| `PUT /api/hub/platform/users/{id}` | Platform owner: `{status,memberships}` → raw user object; account status and memberships are managed here |
| `GET /api/hub/platform/clusters` | Platform owner: `{clusters:[...]}` across organizations |
| `GET /api/hub/organization` | `{id,name,memberCount}` for the active organization |
| `PUT /api/hub/organization` | `{name}`; active organization owner or platform owner |
| `GET /api/hub/members` | `{members:[...]}`; active organization owner |
| `PUT /api/hub/members/{urlEncodedID}` | `{role}` only → `{member}`; active organization owner |
| `DELETE /api/hub/members/{urlEncodedID}` | 204; removes only the organization membership, not the platform account |
| `GET /api/hub/sso` | Provider metadata and approval provisioning; no client secret |
| `GET /api/hub/clusters` | `{clusters,serverTime}` with active organization directory entries filtered to membership scope |
| `POST /api/hub/clusters` | Active organization owner: cluster input → one-time `{cluster,token}` |
| `PUT /api/hub/clusters/{id}` | Active organization owner: cluster input with unchanged ID |
| `POST /api/hub/clusters/{id}/rotate` | `{}` → one-time replacement token; revoked/transferred clusters return 409 and require fresh pairing |
| `POST /api/hub/clusters/{id}/revoke` | `{}` → 204 |
| `POST /api/hub/clusters/{id}/transfer` | Platform owner: `{organizationId}` → `{cluster}` |
| `GET /api/hub/groups` | `{groups:[...]}`; organization cluster-directory groups, not Kubernetes role groups |
| `POST /api/hub/groups` | Active organization owner: save `{id,name,clusterIds}` |
| `PUT /api/hub/groups/{id}` | Active organization owner: save with unchanged ID |
| `DELETE /api/hub/groups/{id}` | 204 |
| `GET /api/hub/connections` | Active organization members may read connection profiles |
| `PUT /api/hub/connections` | Platform owner only: `{lanURL,publicURL}` |

Cluster input contains `id`, `name`, `description`, `environment`, and `region`.
Directory entries are `configured: true` with `state` equal to `offline`,
`connected`, or `revoked`. A connected entry includes the current Agent session
metadata. Connection status is an observation, not cluster health. Groups are
organizational cluster-group metadata, not Kubernetes role groups or implicit
credential scopes; an empty group does not mean all clusters. PAT issuance
always names explicit cluster IDs. New members see their organization's
clusters; only migrated `LegacyRestricted` memberships keep old explicit scope.
Role changes and removals revoke affected PATs and cancel applicable local
Portal requests. Platform-user status changes are separate from membership
role changes. The configured recovery membership is exposed with public ID
`local` and cannot be changed or removed through organization member routes.

Agent rotation/revocation is persisted **before** disconnecting the old session
and invalidating in-progress authentication. A failed persistence operation does
neither. Rotation is available only while the cluster token is active. Revoked or
transferred clusters cannot be restored by rotation; approve and claim a fresh
enrollment to pair again. IDs are never deleted or reused.

## Agent enrollment and bootstrap API

| Method and path | Authentication | Body / result |
| --- | --- | --- |
| `POST /api/hub/enrollments/command` | Portal cookie; owner/platform owner | `{profile:"lan"|"public"}` → `{command,origin}` |
| `GET /downloads/agent/{version}/bootstrap.sh` | None; exact catalog version | Same-origin bootstrap script, hash-pinned by the generated command |
| `POST /api/hub/enrollments` | Anonymous CLI; no cookie, Authorization header, or query | `{profile,contextName,clusterFingerprint}` → `{id,deviceSecret,userCode,expiresAt,pollIntervalSeconds}` |
| `POST /api/hub/enrollments/lookup` | Portal cookie; owner/platform owner | `{userCode}` → request context/status only; no device secret or Agent token |
| `POST /api/hub/enrollments/{id}/approve` | Portal cookie; owner of the selected organization or platform owner | `{userCode,organizationId,name,clusterId}` → `{id,status}`; nonempty clusterId must name a revoked cluster in that organization |
| `POST /api/hub/enrollments/{id}/deny` | Portal cookie; owner/platform owner | `{userCode}` → 204 |
| `POST /api/hub/enrollments/{id}/claim` | One device-secret Bearer; no cookie; empty body | Pending → `{status:"pending"}`; approved one-time claim → pinned cluster/origin/version/artifact metadata and `agentToken` |
| `GET /downloads/agent/{version}/installer.sh` | None; exact catalog version | Installer artifact |
| `GET /downloads/agent/{version}/chart.tgz` | None; exact catalog version | Agent chart artifact |
| `GET /downloads/agent/{version}/checksums.txt` | None; exact catalog version | Public artifact checksums |
| `GET /v2/kubeatlas/agent/...` | None; exact catalog image | Read-only OCI manifest and blob routes |

Enrollment requests expire after ten minutes. The browser lookup and approval
responses never contain either enrollment secret. The claim response is consumed
before it is returned, and the Agent token cannot be recovered if that response
is lost; create and approve a fresh pairing. Re-enrollment is restricted to an
explicit revoked cluster in the same organization.

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

## Connection profiles

`GET /api/hub/connections` is available to active Hub members and returns the
`lanURL` and `publicURL` origins, their derived `/mcp` endpoints, the exact
local-owner `ownerUserID`, and whether the version-matched local artifact
catalog is available. `PUT /api/hub/connections` accepts exactly
`{"lanURL":"...","publicURL":"..."}` and is restricted to the platform owner.
Either origin may be empty. Nonempty origins must use an accepted root HTTPS or
HTTP literal-IP/loopback form; the Hub does not resolve DNS or probe addresses.
Fresh HTTP literal-IP login before a profile exists separately requires the
explicit startup `httpIPOrigin` configuration. MCP and onboarding share the
profile selector.
