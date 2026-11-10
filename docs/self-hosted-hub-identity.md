# Self-hosted Hub identity

This backend supports one self-hosted Hub organization (`self-hosted`, default
name `KubeAtlas`) with the existing local account retained as an immutable
break-glass owner. It is not a multi-tenant isolation system.

## OIDC configuration

The Portal uses the authorization-code flow with mandatory S256 PKCE, a
browser-bound one-use state cookie, nonce verification, and the existing
`go-oidc` verifier for issuer, audience, signature, and expiry validation.
Configure an OIDC client in the independently operated Keycloak realm with the
exact callback URL used by the Portal. The Portal never derives redirect
destinations from request Host or forwarded headers and always returns to `/`.

Required options and environment defaults:

| CLI option | Environment variable | Purpose |
| --- | --- | --- |
| `--oidc-issuer` | `KUBEATLAS_PORTAL_OIDC_ISSUER` | Exact OIDC issuer |
| `--oidc-client-id` | `KUBEATLAS_PORTAL_OIDC_CLIENT_ID` | Public client identifier |
| `--oidc-redirect-url` | `KUBEATLAS_PORTAL_OIDC_REDIRECT_URL` | Fixed `/api/auth/oidc/callback` URL |
| `--oidc-ca-cert` | `KUBEATLAS_PORTAL_OIDC_CA_CERT` | Optional CA PEM file |

The confidential client secret is read only from
`KUBEATLAS_PORTAL_OIDC_CLIENT_SECRET`; there is no CLI secret flag. Issuer,
client ID, redirect URL, and secret must be configured together. OIDC requires
Hub mode; local username/password authentication remains required and available.
Provider discovery must succeed during startup. If the provider is unavailable
later, local login remains available.

HTTPS is required for the issuer and discovered endpoints. For isolated local
development only, literal loopback HTTP is accepted when `--insecure-cookie`
is enabled. Discovery and provider requests use bounded responses, do not
follow redirects, and retain normal TLS verification using system roots with an
optional configured CA. Do not disable certificate validation.

## Provisioning and authorization

OIDC identity IDs are `oidc:` followed by the lowercase SHA-256 hex digest of
`issuer + NUL + subject`. Raw subjects and email addresses are not exposed or
used for linking. A validated preferred username or name is display-only.
First login creates a pending member with no cluster scope or Kubernetes groups.
An owner or administrator must explicitly assign `admin`, `member`, or `viewer`,
set status to `active`, select explicit cluster IDs for members/viewers, and
assign any Kubernetes groups. Group assignments must satisfy the Hub's
`radar:*` / `cloud:*` rules. Identity-provider claims never grant roles,
clusters, or Kubernetes RBAC.

Disabling an IdP account does not immediately invalidate an existing Portal
session. To suspend access immediately, set the Hub member to `disabled`; this
cancels local Portal sessions, revokes the member's PATs, and denies new access.
Without directory sync or backchannel logout, an IdP-only disable may take
effect only at the next sign-in or when the current session expires.

Admins manage all configured clusters; members and viewers see only explicitly
assigned cluster IDs, with an empty list meaning none. Viewer role forces
read-only behavior for browser and MCP requests. Every request reloads current
member state. Changing role, status, cluster scope, or Kubernetes groups revokes
the member's PATs and cancels that member's local Portal sessions; reactivation
does not restore old PATs. Bearer authentication verifies both the stored token
and its owner.

The local owner is synthetic and cannot be changed through member management.
The Hub's roles and scopes are an application-level boundary only and do not
create or replace Kubernetes RBAC grants. Existing browser authorization still
applies the Hub principal's cluster, user, group, and read-only policy.

## Logout and supported boundaries

OIDC state transactions expire after five minutes and are consumed before code
exchange. Sessions are opaque, server-side, and expire no later than the
verified ID token. OIDC logout first cancels the local session and returns a
fixed provider RP-logout URL using the configured client ID and callback origin;
no ID token is placed in that URL.

`/api/hub/sso` reports `directorySync:false`, `backchannelLogout:false`, and
approval provisioning. Directory synchronization and IdP backchannel logout
are not implemented. Mock-issuer tests verify the protocol logic, not real
Keycloak configuration or browser acceptance. This backend slice alone is not
a production-readiness or full Cloud parity claim.

See [Hub Portal API](hub-portal-api.md) for route shapes and
[Hub state](hub-state.md) for persistence and credential semantics.
