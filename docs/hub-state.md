# Hub directory and opaque credentials

`internal/hubstate` stores configured clusters, explicit groups, per-cluster
agent credentials, MCP personal access tokens (PATs), the self-hosted Hub
organization, and OIDC member records. It does not store Kubernetes
resources or snapshots, contact Kubernetes, implement HTTP/UI authentication,
or manage live Hub sessions. Directory entries remain available when their
sources are offline. Revoking an agent token does not delete or permit reuse of
its cluster ID.

## Persistence and deployment boundary

Call `OpenStore(path)` once per file and share that `Store` within one process.
Concurrent methods on that instance are serialized. This is **single-writer**
storage: multiple processes or multiple `Store` instances writing the same file
are unsupported; there is no distributed lock, replication, or background GC.
Use a dedicated private directory and storage-level backups. Existing store
files must be regular files with mode `0600`; the containing directory must be
`0700`. Missing directories are created with `0700` at the first mutation;
existing broad directory permissions are rejected, not silently changed.

Before the first start of a version-2 build against a version-1 state file,
make a verified backup of the state file. `OpenStore` migrates version 1 to
version 2 in place using the normal atomic commit. Do not open a migrated
version-2 file with an older binary; restore the pre-upgrade backup before a
downgrade. Unknown versions and malformed or corrupt state fail closed; the
Hub does not reset or replace them with an empty store.

The version-1 JSON object contains `version`, `clusters`, `groups`, and
`credentials`. Optional `organization` and `members` fields extend existing
version-1 files; a legacy file without them loads with the default
`self-hosted` / `KubeAtlas` organization and an empty member map. The last three
legacy collections and `members` are ID-keyed maps. Internal records contain
SHA-256 digests, and PAT records also retain original TTL and family ancestry.
Public `Cluster`, `Group`, `Member`, and `Credential` metadata never contains
digests or plaintext tokens. Lists are deterministic by ID; input/output slices
and optional timestamps do not alias store state.

The version-2 root fields are `version`, `clusters`, `groups`, `credentials`,
`organizations`, `accounts`, `memberships`, and `connectionSettings`.
`organizations` and `accounts` are ID-keyed maps; `memberships` is keyed first
by organization ID and then by account ID. Cluster records contain
organization-scoped cluster metadata and an Agent-token digest. Group records
contain organization-scoped cluster-group metadata. Credential records contain
organization-scoped PAT metadata, token digests, family ancestry, and TTL
metadata. Accounts contain identity, email/display name, account status, a
stored bcrypt hash where applicable, and local/platform-owner markers.
Membership records contain the organization role, legacy cluster and
Kubernetes-group scope, and timestamps. `connectionSettings` contains the
stored LAN and public origins. Plaintext passwords and tokens are not persisted.
Version-1-only `organization` and `members` aliases are migration input, not
version-2 root fields.

Migration preserves cluster and group records, credential digests, connection
settings, existing Kubernetes groups, and member cluster scopes. Every migrated
membership remains legacy-restricted: an empty old member/viewer scope grants
no clusters, while only an old admin with an empty cluster list retains the
legacy all-cluster restriction. Migration does not synthesize new
`kubeatlas:hub:*` role groups or Kubernetes RBAC grants.

Startup reads through a 16 MiB byte limit (plus one byte to detect overflow),
rejecting malformed/trailing JSON, duplicate or unknown fields, missing required
record fields, nulls, invalid UTF-8, unknown versions, inconsistent map IDs,
invalid digests/text/timestamps, unknown or noncanonical scopes, invalid TTLs,
inconsistent refresh windows, and inconsistent grants within a family. It does
not repair malformed files or silently discard their records.

Each mutation works on a copy, serializes the complete bounded state, writes a
same-directory temporary file with `0600`, syncs and closes it, then renames it
over the store. Only successful rename publishes new in-memory state. Any
failure before rename leaves the prior state and credentials intact and returns
a generic typed error. Rename is the commit point: subsequent best-effort
directory sync cannot be reported as rollback. A power failure on storage that
does not honor file/directory durability may lose the last update; operators
must choose appropriate persistent storage. The file is not encrypted and local
writers can alter grants; protect it and its backups as authentication state.

## Directory and groups

- At most 100 clusters and 100 groups; IDs match
  `^[a-z0-9][a-z0-9-]{0,62}$`.
- Version-1 compatibility input is limited to 1,024 legacy members including
  its former synthetic local owner. Version 2 stores accounts separately from
  organization memberships and supports up to 4,096 accounts.
- OIDC account IDs are `oidc:` plus a lowercase SHA-256 hex digest of the exact
  issuer, a NUL separator, and the OIDC subject. Raw issuer and subject values
  are not persisted; the version-2 account record may retain the validated
  email address and display name.
- Version-1 roles are `admin`, `member`, and `viewer`. Migration maps legacy
  `admin` to the version-2 organization role `owner`; current roles are
  `owner`, `member`, and `viewer`. Account statuses are `active`, `pending`,
  and `disabled`. New identities start pending without organization
  memberships.
- New memberships cover the clusters in their organization. Only migrated
  `LegacyRestricted` memberships retain legacy explicit cluster and
  Kubernetes-group restrictions; an empty migrated member/viewer scope grants
  no clusters, while an old admin with an empty scope retains its legacy
  all-cluster restriction. Viewer memberships are read-only.
- The server derives `kubeatlas:hub:owner`, `kubeatlas:hub:member`, and
  `kubeatlas:hub:viewer` from the organization role. Existing Kubernetes
  groups on migrated memberships are preserved; new memberships have no
  arbitrary Kubernetes-group assignment UI. These roles do not create RBAC
  bindings by themselves.
- Cluster and group names are nonempty, at most 256 bytes, and have no leading
  or trailing whitespace (including Unicode whitespace). Cluster descriptions are at most
  1000 bytes; environment and region are at most 128 bytes each.
- User-controlled text is valid UTF-8 without control characters. Required
  text cannot be whitespace-only.
- `UpdateCluster` requires the input ID to equal the addressed ID. Agent token
  rotation preserves identity only for a cluster with an active token; it
  cannot restore a revoked or transferred cluster. Those clusters require a
  fresh approved enrollment and one-time claim.
- Group members must be unique, known cluster IDs and are sorted when saved.
  An empty group is valid and means **no clusters**, never all clusters.
- Group changes do not expand existing PAT grants. PATs store explicit cluster
  IDs; agent revocation does not implicitly revoke a separately issued PAT.

## Credential separation

Agent tokens start with `ka_agent_`; MCP PATs start with `ka_mcp_`. Each contains
a newly generated 32-byte `crypto/rand` secret encoded as unpadded base64url.
Only SHA-256 of the full token is persisted, and candidate digests are compared
in constant time. Credential IDs are independent random 32-byte hex values.
Plaintext appears only in the immediate result of cluster creation, active
Agent-token rotation, fresh enrollment claim, or PAT issue/refresh/rotation. It
cannot be retrieved later and must not be logged by callers. A lost enrollment
claim response cannot be recovered; a new approved pairing is required. These
tokens do not interoperate with Portal cookies, device secrets, or each other.

The caller must supply a stable nonempty trusted `ownerID`, never an owner
asserted by bearer claims. Authentication checks the stored owner before
returning grants. Changing that trusted owner invalidates old-owner tokens in
the new identity context. Wrong owner and unknown PAT ID/token return the same
generic `credential_invalid` error. Only a matching bearer and owner can learn
expired/revoked bearer status. Agent authentication always returns the same
generic failure for an unknown ID, mismatch, or revoked credential.

PAT issue requires 1–100 unique known cluster IDs; empty scopes, wildcards,
duplicates, and unknown IDs are rejected. Labels are at most 128 bytes. The
`ReadOnly` flag is immutable during renewal. The package returns grants; callers
must enforce them for every operation, including live session use.

OIDC PAT issue, rotation, and refresh are bound to the stored member owner and
revalidated against current active status and current cluster grants in the
store lock. Viewer members always receive read-only credentials. Any member
role, status, cluster-scope, or Kubernetes-group change revokes that owner's
existing PATs in the same atomic store mutation; later reactivation does not
restore them. `CredentialOwner` performs digest lookup for bearer routing, but
callers must still use `Authenticate(token, expectedOwner)` to authenticate.

## Lifetime, replacement, and retention

- Issue TTL `0` means 8 hours; explicit TTL is 15 minutes through 7 days,
  inclusive. Integer bounds are checked before duration conversion.
- Authentication expires exactly at `ExpiresAt`, without an inclusive grace
  period. A refresh requires a currently valid matching-owner bearer; owner
  rotation by credential ID also requires an active, unexpired credential.
- Refresh/rotation TTL `0` inherits the **first-issued TTL**, even if an earlier
  renewal explicitly chose another TTL. Both retain the original 30-day family
  refresh deadline and cap new expiry at that deadline. Fewer than 15 minutes
  remaining rejects renewal; exactly 15 minutes is allowed.
- Refresh/rotation creates a new ID and random token and revokes the predecessor
  in one persistent mutation. Scope, label, owner, and `ReadOnly` cannot change.
  Concurrent refresh attempts on one bearer produce at most one success.
- Revocation is idempotent for the same known owner and ID. Known expired
  credentials can still be explicitly revoked. Owner-filtered lists retain
  expired and revoked metadata for status reporting.
- At most 16,384 credential records (including expired/revoked predecessors) and
  16 MiB total serialized state are permitted. Mutations opportunistically prune
  records only when `now > max(ExpiresAt, RevokedAt) + 30 days`. No live grants
  are evicted to make space; capacity errors leave all existing state intact.
  Authentication and listing do not write or trigger pruning. Families can
  outlive their earliest retained metadata record after retention cleanup.

Errors are `*hubstate.Error` with a generic `Code`: `invalid_parameters`,
`invalid_scope`, `conflict`, `cluster_not_found`, `credential_invalid`,
`credential_expired`, `credential_revoked`, `credential_refresh_expired`,
`member_pending`, `member_disabled`, `member_not_found`, `permission_denied`,
`capacity_exceeded`, or `store_unavailable`. Error strings include neither
tokens nor file contents.

This package does **not** terminate sessions or make a handshake atomic with
token revocation. The integrating Hub must serialize its authentication and
session reservation barrier with rotation/revocation and enforce any required
active-session disconnect policy externally. No endpoint, UI, or deployment is
wired by this package.

## Local evidence

Focused, clock-injected tests use temporary files and no live cluster:

```sh
go test ./internal/hubstate
go test -race ./internal/hubstate
go vet ./internal/hubstate
```
