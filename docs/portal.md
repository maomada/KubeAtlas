# Self-Hosted KubeAtlas Cloud Hub

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
identity data. The Hub does not open, migrate, or remove older Portal data.

The local owner account is configured with
`KUBEATLAS_PORTAL_USERNAME` and `KUBEATLAS_PORTAL_PASSWORD`. The username must
be 1–256 bytes without a colon or control characters, and the password must be
16–4096 bytes. The owner can configure the organization, manage members and
cluster assignments, and issue or revoke scoped MCP credentials.

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
`--insecure-cookie` is restricted to loopback development. If a trusted proxy
uses a public hostname, configure exact allowed host authorities with
`--mcp-allowed-hosts` or `KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS`; wildcards are
not accepted. The Hub also validates browser origin and same-origin requests.

The Hub container does not need a Kubernetes ServiceAccount token. Agents
connect outbound and retain the native KubeAtlas Agent's live read, write,
audit, and MCP behavior under the current user's Kubernetes RBAC.
