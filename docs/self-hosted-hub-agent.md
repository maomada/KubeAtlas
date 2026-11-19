# Self-hosted Hub source agent

The source agent is an explicit opt-in that connects a KubeAtlas process to the
self-hosted Portal Hub mode. It reuses the authenticated outbound
WebSocket/yamux tunnel and serves the source's native HTTP handler. Current
cluster state stays at the source; snapshots are not required by this tunnel.

This document describes the source Agent contract and does not claim a
published/released Agent image or chart, or live-cluster acceptance. The
repository chart is source-deployable, and an operator can package that
unchanged chart together with an approved local OCI image layout for a private
Hub bundle; see [Hub-hosted Agent onboarding](hub-agent-onboarding.md).

## Configure the source

Provide all three required options (or their corresponding environment
variables). The CA file is optional:

| Option | Environment variable | Meaning |
| --- | --- | --- |
| `--hub-url` | `KUBEATLAS_AGENT_HUB_URL` | Root HTTP(S) Portal origin |
| `--hub-cluster-id` | `KUBEATLAS_AGENT_HUB_CLUSTER_ID` | Stable cluster ID |
| `--hub-token-file` | `KUBEATLAS_AGENT_HUB_TOKEN_FILE` | File containing the cluster bearer token |
| `--hub-ca-file` | `KUBEATLAS_AGENT_HUB_CA_FILE` | Optional CA bundle for a private Hub certificate |

For example:

```sh
KUBEATLAS_AGENT_HUB_URL=https://portal.example.test \
KUBEATLAS_AGENT_HUB_CLUSTER_ID=prod-east-1 \
KUBEATLAS_AGENT_HUB_TOKEN_FILE=/var/run/secrets/kubeatlas/hub-token \
KUBEATLAS_AGENT_HUB_CA_FILE=/var/run/secrets/kubeatlas/hub-ca.crt \
./kubeatlas
```

The existing `KUBEATLAS_HUB_URL` and `KUBEATLAS_HUB_APP_URL` variables remain
origin-only hints; they do not enable the source agent.

The URL is an origin, not an agent endpoint: the source derives `/agent` and
uses `wss`. It rejects credentials, query strings, fragments and subpaths.
Plain HTTP/`ws` is permitted for loopback development and explicitly configured
literal IP origins on a trusted network; HTTP DNS names are rejected. For a
Hub-hosted image, each target node must also permit the selected HTTP registry
authority. See the [onboarding prerequisites](hub-agent-onboarding.md) before
using this mode. The cluster ID must
match `^[a-z0-9][a-z0-9-]{0,62}$`. Supplying any Hub option requires the full
URL/ID/token-file set. The validated origin is also used for API and app origin
hints.

The token is read from a file, never from a flag or environment variable. The
file is capped at 4096 bytes and may end in a newline; after trimming
surrounding whitespace, the token must contain at least 32 characters and no
embedded whitespace or control characters. Projected Kubernetes Secret
symlinks are supported. A private CA extends the system trust roots;
certificate verification remains enabled.

Token rotation currently requires updating the token file and restarting the
source process. The source does not hot-reload the token.

## Identity and access

Hub mode forces proxy authentication with the exact `X-Forwarded-User` and
`X-Forwarded-Groups` headers. Those headers are trusted only on requests marked
by the authenticated Hub tunnel; ordinary source TCP remains health-only.
User calls continue through Kubernetes impersonation and do not fall back to
the shared ServiceAccount identity. Read-only MCP mode remains read-only.

The source does not create Kubernetes role bindings. Operators own the exact
username and group values that the Hub asserts and must bind those identities
to the intended Roles or ClusterRoles. This mode does not automatically grant
cluster-admin. OIDC auth, user-token credential mode, a nonempty base path,
disabled MCP, and catalog-only/catalog-stdio modes cannot be combined with the
Hub agent.

This slice does not enroll clusters, issue licenses, call legacy update APIs,
or enable self-upgrade. The source advertises self-upgrade as unavailable.
Standalone startup without Hub options remains unchanged.

For the Hub-hosted installer, chart, OCI image, LAN/public profiles, and new-install bootstrap, see [Hub-hosted Agent onboarding](hub-agent-onboarding.md). The optional private bundle is operator-staged and immutable; no external registry fetch or publish occurs when the Portal serves it.
