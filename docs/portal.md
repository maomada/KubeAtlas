# Self-Hosted KubeAtlas Cloud Hub

Public registrations create pending accounts with no organization membership or
active session. The platform owner approves pending accounts and grants initial
membership; organization owners manage roles and scope for existing approved
members in their organization. Organization member lists and counts exclude
platform administrators and local recovery accounts. Organization member
updates and removals reject those platform identities, and last-owner
protection counts only active ordinary organization owners. Existing internal
recovery memberships remain compatible; they do not grant visibility or editing
through the organization member directory. The configured recovery identity is
the only platform owner; only its exact local `User` identity receives `cluster-admin`.
The chart maps organization role groups to `admin`, `edit`, or `view`, plus
limited infrastructure-read access. Existing Agent deployments need an
operator-controlled chart rollout to apply those bindings; the Hub does not
change remote RBAC or Agent token Secrets.
Requests bind the authenticated account, active organization, and cancellation
context at entry. Role groups are derived server-side, never accepted from
client headers, and MCP credentials remain pinned to their issuance
organization.

The Hub persists a per-account authorization revision and returns it as a
canonical decimal string in both active and pending/no-organization
`GET /api/hub` responses. It advances only when account authorization fields
or organization membership semantics change; timestamps and display-name-only
updates do not advance it. The revision is invalidation metadata, not a
permission grant or session ID. Portal YAML draft scopes include the revision:
same-revision login, reload, and ordinary session recovery retain the narrow
draft scope, while a changed revision invalidates drafts from the prior
authority. Missing or malformed revision metadata fails closed in the UI.

The `kubeatlas-portal` binary name is retained for a self-hosted Cloud Hub that
serves the Cloud UI, member identity, browser proxy, and live MCP transport.
It is separate from the native KubeAtlas Agent. This repository does not
provide a hosted KubeAtlas Cloud service.

The Hub uses connected Agents for live cluster access. It does not read remote
kubeconfigs or make Kubernetes API requests itself, and it does not collect or
upload context snapshots.

## Required Hub state

The Hub cannot start without a persistent state file. Pass
`--hub-state-file=/path/to/hub-state.json` or set
`KUBEATLAS_PORTAL_HUB_STATE_FILE`. There is no default path or legacy mode.
The state file holds the self-hosted organization, memberships, cluster
assignments, and MCP credential records; protect and back it up as sensitive
identity data. Back up the state file before the first start of a version-2
build: version-1 state migrates to version 2 in place. Do not downgrade using a
migrated file; restore the pre-upgrade backup first. Unknown or corrupt state
fails closed and is not reset. Migration preserves directory entries,
credential hashes, scopes, groups, and legacy Kubernetes restrictions;
unrelated older Portal data is not imported or removed.

The local owner account is configured with
`KUBEATLAS_PORTAL_USERNAME` and `KUBEATLAS_PORTAL_PASSWORD`. The username must
be 1–256 bytes without a colon or control characters, and the password must be
16–4096 bytes; these existing recovery-login limits are unchanged. Public
registration has a separate rule: passwords must contain at least 6 Unicode
scalar values, be valid UTF-8, and contain at most 72 UTF-8 bytes because
passwords are hashed with bcrypt. The owner can configure the organization,
manage members and cluster assignments, and issue or revoke scoped MCP
credentials.

## Optional OIDC

An independent self-hosted OIDC provider such as Keycloak can provide
authorization-code login with PKCE, state, and nonce validation. Configure
`--oidc-issuer`, `--oidc-client-id`, `--oidc-redirect-url`, and
`KUBEATLAS_PORTAL_OIDC_CLIENT_SECRET` together. An optional issuer CA bundle is
provided with `--oidc-ca-cert`. The redirect URL must be fixed to the Portal's
callback URL.

OIDC accounts are provisioned through the login flow as pending accounts with
no organization memberships. A platform owner approves account status and
grants initial organization memberships. Organization owners can change roles
or remove existing memberships in their organization; they cannot approve
accounts or set per-user cluster scopes or Kubernetes groups. New memberships
cover all clusters in their organization; migrated legacy-restricted
memberships retain their existing scope. The Hub derives the fixed
`kubeatlas:hub:owner`, `kubeatlas:hub:member`, and `kubeatlas:hub:viewer` groups
from the current organization role. Pending or disabled accounts cannot use
live cluster access. Every proxied Kubernetes request is still evaluated by
the target cluster's Kubernetes RBAC using that member's identity.

Directory synchronization and OIDC backchannel logout are not implemented.
Disabling an identity-provider account alone does not immediately revoke an
existing Hub session. Sessions are owner-bound and re-check current membership
and scope on Hub requests; they expire or can be revoked by member changes.
Local owner login remains available.

## Live endpoints

- The self-hosted Cloud UI and identity API are served by the Hub.
- `GET /agent` upgrades to the Agent's outbound WebSocket connection to the Hub.
- `/mcp` is the canonical Cloud Hub MCP endpoint. It requires an independent
  bearer credential and current active membership, explicit cluster scope,
  and read-only enforcement for viewer credentials.
- Native Agent MCP endpoints `/mcp` and `/mcp-readonly` are separate from the
  Hub's `/mcp`; they are served by the Agent process, not aliases or fallback
  routes on the Hub.

Hub bearer credentials are never taken from browser cookies. They expire and
can be revoked or refreshed through the authenticated Hub API. The live path
uses the member's principal and tunneled Kubernetes identity; it does not
fall back to cached snapshots or direct Fleet tools.

The Agent page provides client-neutral Streamable HTTP connection details.
After issuance, the one-time copy action includes the endpoint and the
`Authorization: Bearer` header. Tokens are never appended to URLs. Credential
names start empty, and confirmed revocations disappear from the page while
the backend retains the revoked metadata for its normal retention period.

See [Hub state](hub-state.md) for persistence and validation,
[self-hosted identity](self-hosted-hub-identity.md) for login and membership,
[live Hub MCP](hub-live-mcp.md) for transport and authorization, and
[Hub API](hub-portal-api.md) for route and payload contracts. The separate
[Hub Helm chart](../deploy/helm/kubeatlas-portal/README.md) requires an
existing credentials Secret and persistent storage.

## Deployment and security

Deploy Hub using the [systemd installer](deploy-with-ai.md#install-from-the-release-bundle),
the [Helm chart](../deploy/helm/kubeatlas-portal/README.md), or
[Docker Compose](../deploy/compose/README.md). Use a release that publishes the
required artifacts for the selected method. The chart persists the required
state file, runs one Hub replica, and mounts an existing Secret for the local
owner credentials. Optional Secret keys configure Hub groups, OIDC, and trusted
MCP host authorities.

Use TLS at a trusted reverse proxy before exposing the login page, callback,
Hub API, Agent transport, or MCP endpoint. Cookies are secure by default;
`--insecure-cookie` remains restricted to loopback development. A fresh install
served on a trusted private network over HTTP at a literal IP can explicitly set
`--http-ip-origin=http://192.0.2.10:9290` or
`KUBEATLAS_PORTAL_HTTP_IP_ORIGIN=http://192.0.2.10:9290` (the Portal chart uses
`httpIPOrigin`). This exact root origin enables login with host-scoped,
HttpOnly, SameSite cookies without `Secure` before any LAN profile exists; it
does not create or overwrite a connection profile. Only literal IPv4 or
bracketed IPv6 authorities are accepted; DNS names, other hosts and ports,
paths, queries, and fragments are not. HTTP exposes credentials and bearer
traffic to interception, so use this only on a trusted network. Forwarded
headers do not change the decision, and this option does not allow an HTTP OIDC
provider. If a saved HTTPS profile uses the same external authority, HTTPS wins
and cookies remain Secure; do not multiplex HTTP and HTTPS on one authority
behind a proxy. Connection-profile writes are platform-owner-only; active
members may read them. Set `--hub-origin` or
`KUBEATLAS_PORTAL_HUB_ORIGIN` to seed the public HTTPS address for new Hub
state. The Helm value is `hubOrigin`. If a trusted proxy uses a public
hostname, configure exact allowed host authorities with
`--mcp-allowed-hosts` or `KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS`; wildcards are
not accepted. The Hub also validates browser origin and same-origin requests.

The Hub container does not need a Kubernetes ServiceAccount token. Agents
connect outbound and retain the native KubeAtlas Agent's live read, write,
audit, and MCP behavior under the current user's Kubernetes RBAC.

Hub serves version-pinned Agent downloads and read-only OCI GET/HEAD content
from its verified catalog. The systemd installer imports the matching release
bundle. Official Hub images with a built-in catalog serve it from
`/opt/kubeatlas/agent-catalog`. Custom deployments can use
`--hub-artifacts-dir` or `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` to supply a
[private catalog](hub-agent-onboarding.md#prepare-a-private-bundle).

The TLS reverse proxy must preserve the selected Host authority and route
`/api/`, `/agent`, `/mcp`, `/downloads/agent/`, and `/v2/` to the Portal without
path rewriting or redirects. Nodes pulling the Agent image must reach this same
origin and trust its certificate; the Agent's optional CA file does not
configure node container-runtime trust.

### Maintain a custom Hub service

On an existing self-managed Linux service, verify and install the matching
`kubeatlas-portal` package and keep the existing service account, state path,
and login configuration. Run Hub as a dedicated unprivileged service user.
Provide `KUBEATLAS_PORTAL_USERNAME` and `KUBEATLAS_PORTAL_PASSWORD` through a
root-owned systemd environment file with mode `0600`. Put the catalog at a
versioned directory readable by that user and keep it operator-owned.

For example, start the Hub behind a same-host TLS proxy with
`/usr/bin/kubeatlas-portal --listen=127.0.0.1:9290 --hub-state-file=/var/lib/kubeatlas-portal/state.json --hub-artifacts-dir=/srv/kubeatlas/catalogs/VERSION`.
Replace the state and catalog paths with the actual paths. After updating the
unit, run `systemctl daemon-reload` and restart the Hub service. Verify the
service, HTTPS login, and Agent downloads. [Hub state and recovery](hub-state.md)
