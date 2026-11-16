# PR #17 security revalidation

This is a review ledger, not a passing scan or approval of unresolved findings.
The original candidate was `a97fc651ede3242541f84f1fdf194496220d2ccf`;
the repair branch also includes main `e4193b22c2f5acc6bdbb21c45b3016c08eb266f5`.
The final head, tested merge source and fresh Actions results must be recorded
in the PR verification checkpoint after the candidate is pushed.

## Retained evidence

[Security run 43843220982](https://github.com/maomada/KubeAtlas/actions/runs/43843220982)
scanned 1,981 files and reported 111 findings, but two Go files had partial
parsing errors. It is incomplete evidence and cannot satisfy the security gate.
The fixed scanner does not understand Go 1.26 expression operands to `new` in
`internal/hubmcp/handler.go` and `internal/mcp/input_constraints.go`.
The repair uses equivalent typed values and addresses; scanner errors and
coverage checks remain blocking. A fresh complete scan is still required.

## New finding review

Locations below refer to the retained report. Final identities must come from
the fresh scan; no rule, file or finding is suppressed by this document.

| Source | Source-to-sink review | Current disposition |
| --- | --- | --- |
| `internal/hub/http_client.go:162` | The incoming request is cloned and its URL independently copied before the outbound Host is changed. | False-positive candidate; bind to the fresh finding and exact source. |
| `internal/hub/http_client.go:473` | SHA-1 computes the WebSocket protocol's Accept challenge using its fixed GUID; it is not used for passwords or application signatures. | False-positive candidate within that protocol-only scope. |
| `internal/hub/hub.go:234` | The agent route rejects any browser Origin, validates one bearer credential and its cluster, and supplies an explicit no-Origin `CheckOrigin` function. | False-positive candidate for the missing-origin-check rule; not general Hub authorization acceptance. |
| `internal/hubartifacts/catalog.go:365` | Entry reads now use CopyN. The total expanded-size check runs before addition, rejecting oversized headers without int64 overflow. | Normal, truncated and MaxInt64-declared-entry regressions pass; require fresh scan. |
| `internal/hubartifacts/prepare.go:85` | The Helm executable is selected by a local artifact-preparation CLI; arguments are separate and no shell or HTTP-selected command is used. | False-positive candidate with a trusted local operator/environment boundary. |
| `internal/portal/browser_proxy.go:267` | Director is replaced with Rewrite. Routing is fixed after hop-by-hop header removal, while principal injection remains in the internal transport. | Connection-header identity and routing regression passes through a real loopback Hub/Agent tunnel; require fresh scan. |
| `internal/portal/browser_proxy.go:272` | The proxy now explicitly copies the outbound URL before rewriting it. | Original-URL alias and raw-query regressions pass; require fresh scan. |
| `internal/portal/oidc.go:310,354` | Cookies explicitly use `cookieSecureForRequest`; an explicitly configured non-loopback HTTP-IP origin may make Secure false. | Pending maintainer choice; old loopback-only evidence does not cover this behavior. |
| `web/src/portal/portalNavigation.ts:2` | Call sites use local constant paths or an authenticated logout URL constructed from a validated IdP endpoint. | False-positive candidate with server-selected logout destinations; not approval for arbitrary future URL callers. |

## Existing records

Thirteen migrated identities retain the same matched code: SAST-003, 021, 037,
051, 052, 057 and 069–075. Each still requires source-to-sink revalidation;
matching text alone is insufficient. In particular, CodeViewer still uses
fixed themes and Shiki's escaped `codeToHtml` output with the new highlighter.

SAST-038 requires a new decision together with the two OIDC-cookie findings:
its old evidence restricted insecure cookies to an explicit loopback setting.
The HTTP-IP behavior is a broader boundary. Source controls, network/TLS
limitations and the maintainer's decision must be recorded before acceptance.

The old SAST-112 source in `internal/portal/agent_hub.go` was removed with the
legacy Fleet path. Preserve its historical review; a new Hub origin finding
must receive its own identity and review rather than inherit that approval.

The initial review was performed by Codex and the independent
`actions_cost_audit` reviewer. It creates no human approval. New decisions must
retain their own reviewer, evidence, source/rules binding and limitations.

## Verification boundaries

The release-receipt regression fixture is isolated from real SAST dispositions.
The same production validators still reject stale source and incomplete scans.
Windows compilation and focused input-constraint tests are separate from Linux
Actions acceptance. Full Hub tests on Windows hit the secure-store filesystem
precondition; no passing result is inferred from compilation.

Targeted checks on Windows with Go 1.26.11 passed: the three MCP input-constraint
tests; four Portal proxy/response-limit tests; and three Chart reader tests.
The overflow regression was also run against the old guard and failed with an
unexpected EOF, demonstrating that the previous guard reached the oversized
payload instead of rejecting its declared size. These are unit/mock-transport
results, not a complete Linux suite or Kubernetes acceptance.

No new Kubernetes or browser acceptance is claimed. The repository's
`test-radar` workflow stops when Playwright MCP is unavailable in this session.
Historical browser and cluster evidence remains bound to its historical SHAs.
