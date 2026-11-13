# Self-Hosted KubeAtlas Cloud Hub

Public registrations create pending accounts with no organization membership or
active session. The platform owner approves pending accounts and grants initial
membership; organization owners manage roles and scope for existing approved
members in their organization. The configured recovery identity is the only
platform owner; only its exact local `User` identity receives `cluster-admin`.
The chart maps organization role groups to `admin`, `edit`, or `view`, plus
limited infrastructure-read access. Existing Agent deployments need an
operator-controlled chart rollout to apply those bindings; the Hub does not
change remote RBAC or Agent token Secrets.
Requests bind the authenticated account, active organization, and cancellation
context at entry. Role groups are derived server-side, never accepted from
client headers, and MCP credentials remain pinned to their issuance
organization.

The `kubeatlas-portal` binary name is retained for a self-hosted Cloud Hub that
serves the Cloud UI, member identity, browser proxy, and live MCP transport.
It is separate from the native KubeAtlas Agent. This repository does not
provide a hosted Radar Cloud service.

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
registration has a separate rule: passwords must contain at least 12 Unicode
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

OIDC accounts are provisioned through the login flow and require owner
approval before they can use the Cloud UI. The owner assigns a role, cluster
scope, and Kubernetes groups. Pending or disabled members cannot use live
cluster access. Every proxied Kubernetes request is still evaluated by the
target cluster's Kubernetes RBAC using that member's identity.

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

See [Hub state](hub-state.md) for persistence and validation,
[self-hosted identity](self-hosted-hub-identity.md) for login and membership,
[live Hub MCP](hub-live-mcp.md) for transport and authorization, and
[Hub API](hub-portal-api.md) for route and payload contracts. The separate
[Hub Helm chart](../deploy/helm/kubeatlas-portal/README.md) requires an
existing credentials Secret and persistent storage.

## Deployment and security

Build the container from `Dockerfile.portal` or install a published chart
version through the documented release process. The chart persists the
required state file, runs one Hub replica, and mounts an existing Secret for
the local owner credentials. Optional Secret keys configure Hub groups, OIDC,
and trusted MCP host authorities.

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
members may read them. If a trusted proxy
uses a public hostname, configure exact allowed host authorities with
`--mcp-allowed-hosts` or `KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS`; wildcards are
not accepted. The Hub also validates browser origin and same-origin requests.

The Hub container does not need a Kubernetes ServiceAccount token. Agents
connect outbound and retain the native KubeAtlas Agent's live read, write,
audit, and MCP behavior under the current user's Kubernetes RBAC.

Hub-hosted onboarding artifacts are optional and operator-staged. Configure
`--hub-artifacts-dir` or `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` with a validated
private bundle; see [Hub-hosted Agent onboarding](hub-agent-onboarding.md).
The Portal serves version-pinned downloads and read-only OCI GET/HEAD content
from that local directory only.

The TLS reverse proxy must preserve the selected Host authority and route
`/api/`, `/agent`, `/mcp`, `/downloads/agent/`, and `/v2/` to the Portal without
path rewriting or redirects. Nodes pulling the Agent image must reach this same
origin and trust its certificate; the Agent's optional CA file does not
configure node container-runtime trust.
