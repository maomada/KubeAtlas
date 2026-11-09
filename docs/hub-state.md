# Hub directory and opaque credentials

`internal/hubstate` is a standard-library-only persistence package. It stores
configured clusters, explicit groups, per-cluster agent credentials, and
independent MCP personal access tokens (PATs). It does not store Kubernetes
resources or snapshots, contact Kubernetes, implement HTTP/UI authentication,
resolve groups for authorization, or manage live Hub sessions. Directory entries
remain available when their sources are offline. Revoking an agent token does
not delete or permit reuse of its cluster ID.

## Persistence and deployment boundary

Call `OpenStore(path)` once per file and share that `Store` within one process.
Concurrent methods on that instance are serialized. This is **single-writer**
storage: multiple processes or multiple `Store` instances writing the same file
are unsupported; there is no distributed lock, replication, or background GC.
Use a dedicated private directory and storage-level backups. Existing store
files must be regular files with mode `0600`; the containing directory must be
`0700`. Missing directories are created with `0700` at the first mutation;
existing broad directory permissions are rejected, not silently changed.

The version-1 JSON object contains `version`, `clusters`, `groups`, and
`credentials`. The last three are ID-keyed maps. Internal records contain
SHA-256 digests, and PAT records also retain original TTL and family ancestry.
Public `Cluster`, `Group`, and `Credential` metadata never contains digests or
plaintext tokens. Lists are deterministic by ID; input/output slices and
optional timestamps do not alias store state.

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
- Names are nonempty and at most 256 bytes. Cluster descriptions are at most
  1000 bytes; environment and region are at most 128 bytes each.
- User-controlled text is valid UTF-8 without control characters. Required
  text cannot be whitespace-only.
- `UpdateCluster` requires the input ID to equal the addressed ID. Agent token
  rotation preserves identity and restores a previously revoked credential.
- Group members must be unique, known cluster IDs and are sorted when saved.
  An empty group is valid and means **no clusters**, never all clusters.
- Group changes do not expand existing PAT grants. PATs store explicit cluster
  IDs; agent revocation does not implicitly revoke a separately issued PAT.

## Credential separation

Agent tokens start with `ka_agent_`; MCP PATs start with `ka_mcp_`. Each contains
a newly generated 32-byte `crypto/rand` secret encoded as unpadded base64url.
Only SHA-256 of the full token is persisted, and candidate digests are compared
in constant time. Credential IDs are independent random 32-byte hex values.
Plaintext appears only in the immediate result of cluster creation/agent
rotation or PAT issue/refresh/rotation. It cannot be retrieved later and must
not be logged by callers. These tokens do not interoperate with Portal cookies,
shared enrollment keys, or each other.

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
