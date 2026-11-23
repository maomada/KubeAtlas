# Self-hosted Hub identity

The self-hosted Hub supports multiple organizations with separate memberships
and request scopes. The platform owner is the administrator created during
first-run setup or migrated from an existing installation. Its account ID and
Kubernetes identity survive administrator credential resets. [Platform administrator](portal.md#platform-administrator)

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
First login creates a pending account with no organization membership, cluster
scope, or Kubernetes groups. A platform owner approves pending accounts and
grants initial membership. Account status is `pending`, `active`, or `disabled`
and is managed by the platform owner through platform-user management.
Organization owners cannot approve registrations or disable accounts; they can
change an existing approved member's role or remove that membership.

Organization roles are `owner`, `member`, and `viewer`. New memberships cover
all clusters in their organization; only memberships migrated with the
`LegacyRestricted` marker retain their old explicit cluster and Kubernetes
group restrictions. The viewer role is read-only. The Hub derives
`kubeatlas:hub:owner`, `kubeatlas:hub:member`, or `kubeatlas:hub:viewer` from the
selected organization role on the server. Clients cannot supply those groups in
request headers, and the Hub has no arbitrary Kubernetes-group assignment UI
for new memberships. Existing groups on migrated memberships remain preserved.
Identity-provider claims never grant roles, clusters, or Kubernetes RBAC.

Disabling an IdP account does not update the Hub account automatically. The
platform owner can set the Hub account to `disabled`, which denies access across
organizations, revokes its PATs, and cancels local Portal sessions. An
organization owner who needs to remove access only in one organization removes
that membership instead. Without directory sync or backchannel logout, an
IdP-only disable takes effect only at the next sign-in or when the current
session expires.

Organization owners manage their organization's clusters; members and viewers
have the organization's cluster scope, except for migrated memberships that
retain their legacy restrictions. Viewer role forces read-only behavior for
browser and MCP requests. Every request reloads current account and membership
state. A role change, membership removal, or platform-account status change
revokes affected PATs and cancels applicable local Portal requests; restoring
access does not restore revoked PATs. Bearer authentication verifies both the
stored token and its owner. The Hub roles do not themselves install Kubernetes
grants; chart role-group bindings must be applied separately by the operator.

The configured recovery identity is a persisted local account and owner
membership, not a synthetic directory-only member. The platform-users API
exposes it under the public ID alias `local`; it does not expose its password
hash. Its platform-owner status comes from trusted startup configuration. The
Hub's roles and scopes are an application-level boundary only and do not create
or replace Kubernetes RBAC grants. Existing browser authorization still applies
the Hub principal's cluster, user, group, and read-only policy.

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
