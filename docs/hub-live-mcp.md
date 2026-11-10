# Live Hub MCP

`internal/hubmcp.New` implements the self-hosted Cloud Hub's `HubConfig.MCPFactory`
seam. The Hub's canonical MCP endpoint is `/mcp`; it is not aliased as
`/mcp-fleet`. Each HTTP request is authenticated with an independent bearer
credential and checked against the current member status, role, explicit
cluster scope, and read-only setting.

The Hub forwards the authorized request to a connected Agent over the live
tunnel. It does not dispatch snapshot tools, use a registration token map, or
fall back to collected context. Kubernetes operations run with the member's
principal and remain subject to each target cluster's Kubernetes RBAC.

## Required Hub configuration

`kubeatlas-portal` requires a persistent Hub state file. Configure
`--hub-state-file=/path/to/state.json` or
`KUBEATLAS_PORTAL_HUB_STATE_FILE`; startup fails if neither is set. Local owner
credentials remain `KUBEATLAS_PORTAL_USERNAME` and
`KUBEATLAS_PORTAL_PASSWORD`. Optional OIDC settings are documented in the
[self-hosted Cloud Hub guide](portal.md).

Configure the Portal's exact allowed external host authorities with
`--mcp-allowed-hosts` when it sits behind a trusted proxy. The Hub rejects
foreign or ambiguous Host/Origin values and cross-site browser requests.
Forwarded Host headers do not grant authority.

## Credential boundary

The Cloud UI issues credentials through the authenticated Hub API with
explicit cluster IDs and a required read-only choice. Every request uses one
`Authorization: Bearer …` header; browser cookies do not substitute for a
missing bearer. URL credentials and duplicate/ambiguous authentication headers
are rejected. The Hub removes bearer and cookie headers before invoking the
MCP handler and supplies a principal bound to the current membership.

Pending and disabled members cannot access live clusters. Viewer members are
forced read-only even if a credential requests writes. Member status, cluster
scope, revocation, and expiration are checked on every request, independent of
MCP session IDs. Credential refresh rotates the token without extending its
absolute refresh-family lifetime; member changes revoke the member's
credentials and sessions.

## Agent endpoints

The Agent's native `/mcp` and `/mcp-readonly` are separate endpoints on the
Agent process. They are not aliases to the Hub's `/mcp`, and they remain
available for direct native Agent use. The Hub's `/agent` WebSocket tunnel is
the Cloud path to live KubeAtlas source tools.

See [Hub API](hub-portal-api.md), [Hub state](hub-state.md), and
[self-hosted identity](self-hosted-hub-identity.md) for request, persistence,
and membership contracts.
