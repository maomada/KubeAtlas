# Native live resource pages and source observation metadata

The source MCP `list_resources` tool optionally pages **authorized raw objects**
before minification and per-row summary-context attachment. This is not Hub-side
slicing of a downloaded JSON inventory. It applies to typed resources, dynamic
CRDs, and direct dynamic reads without changing their collection/RBAC gates.

## Opt-in compatibility

Omit both `limit` and `cursor` to retain the existing unpaged response: the same
`summary`, optional `next`, `coverage`, and `items`, without a `page` field. Native
callers and optional snapshot callers are not silently truncated.

An explicit `limit` must be an integer from 1 through 200. The published SDK
schema enforces these bounds and injects **no default**. Direct handler calls
also reject negative/excessive limits and a cursor without a positive limit;
zero with no cursor is the handler representation of omission. To start:

```json
{"kind":"pods","namespace":"team-a","context":"none","limit":50}
```

Paged responses add:

```json
{
  "page": {
    "limit": 50,
    "returned": 50,
    "matchingAtRead": 127,
    "truncated": true,
    "nextCursor": "<opaque continuation>",
    "consistency": "live_non_atomic"
  }
}
```

The numbers above are illustrative, not observed cluster data. `matchingAtRead`
counts authorized rows actually obtained in **this request**, before paging; it
is not a global cluster total and excludes inaccessible/uncollected objects.
`returned` counts this page. `nextCursor` exists only when more currently read
rows follow the last returned key. Keep the same query, context option, and
limit and pass that cursor to continue. The final page has `truncated:false`
and no `nextCursor`; a live deletion can make it empty.

The summary explicitly labels a PAGE, not a complete inventory. `coverage`
continues to describe collection/RBAC completeness, independently of paging.
Complete collection coverage does not imply a page includes every object.
Existing collector gaps, partial observations, and current-cache checks remain.

## Cursor and consistency contract

An owned reference slice is sorted by namespace then name without modifying
informer objects or their shared slices. Resume keys are strictly greater than
the signed last namespace/name. No result cache or pointer registry is retained.
The source still gathers its current authorized raw list on each request; this
is not Kubernetes API-server `continue` pagination and does not bound informer
memory or upstream list acquisition. It bounds rows minified and enriched for
the response.

Cursors are bounded to 4096 bytes and contain versioned JSON encoded as
unpadded base64url plus an HMAC-SHA256 signature. A process-local random 32-byte
key signs them; restart invalidates all previous cursors. They bind:

- kind, API group, namespace, context option, and limit;
- the authenticated username, sorted groups, UID, System flag, sorted HubGroups,
  Extra values, and a hash of any bearer token (never the raw bearer);
- the source resource cache's construction generation, which changes on context
  switches and namespace rescoping;
- an expiry fixed ten minutes after the first page, never extended by paging.

Only a fingerprint of query/identity is carried, not the underlying identity or
token hash. The cursor is signed, **not encrypted**, and can expose the last
resource namespace/name. Treat it as opaque; it is never an authorization grant.
Every request repeats normal authorization, so changed permissions can narrow
subsequent pages. Cursor values are redacted from tool-input logs and omitted
from error messages.

Invalid/tampered/cross-query/cross-principal cursors return `invalid_cursor`;
expiry returns `cursor_expired`; invalid paging parameters return
`invalid_parameters`. These declared read reasons use `change_request` actions
and actionable structured/text errors. Cache generation changes return the
existing `connection_changed` reason: confirm the current target and restart.

Pages are explicitly **live_non_atomic**: inserts, deletions, and permission or
collector changes can alter later pages and their counts. Inserts before the
resume key can be missed, and a deleted/recreated object with the same key is
not a separate snapshot entry. A full traversal is not a point-in-time inventory
or proof of absence. Restart the query when a new complete sweep is needed, and
still retain its coverage and non-atomic limitations.

## Source completion observation

The shared source `logToolCall` wrapper adds
`_meta["kubeatlas/observedAt"]` to each nonnil tool result after converting
declared read errors. It is a UTC RFC3339Nano timestamp taken at **source handler
read/operation completion**, not an object's modification time, informer
freshness, Kubernetes consistency bound, or Hub receipt time. Content,
StructuredContent, extra handler data, and other metadata are not rewritten.

Protocol failures without a tool result can lack the timestamp. A live facade
must report that observation as unknown, not substitute its own receipt time.
This change does not add a Portal/Hub facade, HTTP route, deployment, or snapshot
capability.

## Focused local evidence

Tests reuse fake resource-cache, authorization, and dynamic fixtures, plus an
injected helper clock for cursor expiry. They are not live-cluster acceptance.

```sh
go test ./internal/mcp -run 'Pagination|ObservedAt|ToolInputConstraints|ToolCatalogContextBudget'
go test ./internal/mcp
go test -race ./internal/mcp -run 'Pagination|ObservedAt'
go vet ./internal/mcp
(cd pkg && go test ./readreason && go vet ./readreason)
```
