# PR #17 security revalidation

This is a source-review ledger; current Actions acceptance is recorded separately
in the PR verification checkpoint.
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
coverage checks remain blocking.

[Fresh scan 43853451778](https://github.com/maomada/KubeAtlas/actions/runs/43853451778)
tested merge source `448fcf169a0af7753063fef04a5d77119129d6bd` for PR head
`2d7fd52b91cec6244014716838548fe070e48565`: 1,981 files, 108 findings and zero
scanner errors. Its original findings SHA-256 is
`d6dc05af77b410de29533f1fc4f1ebd67beb1d2fa17cb7dd03e8b30cfd003c84`.
All production Git blobs equal that tested source. The source-tree SHA-256 is
`6b039f4e89c85ccb464ba142214c376b45ee61a0bc84737322d4830c53266c41`.
The raw report is retained unchanged; its workflow failed on the old policy
binding. The refreshed policy evaluates 105 reviewed exceptions and three
separately accepted risks, with zero unresolved findings. Its new Actions run
must still validate the final candidate and gate implementation.

## New finding review

The fresh scan has 87 exact previous identities, 14 moved identities and seven
new findings (SAST-113 through SAST-119). No rule, file or finding is suppressed.
The table records the review and fixes against that complete report.

| Source | Source-to-sink review | Current disposition |
| --- | --- | --- |
| `internal/hub/http_client.go:162` | The incoming request is cloned and its URL independently copied before the outbound Host is changed. | SAST-113: false positive, bound to this exact clone/copy path. |
| `internal/hub/http_client.go:473` | SHA-1 computes the WebSocket protocol's Accept challenge using its fixed GUID; it is not used for passwords or application signatures. | SAST-114: false positive within that protocol-only scope. |
| `internal/hub/hub.go:234` | The agent route rejects any browser Origin, validates one bearer credential and its cluster, and supplies an explicit no-Origin `CheckOrigin` function. | SAST-115: false positive for the missing-origin-check rule; not general Hub authorization acceptance. |
| `internal/hubartifacts/catalog.go:365` | Entry reads now use CopyN. The total expanded-size check runs before addition, rejecting oversized headers without int64 overflow. | Fixed and absent from the complete scan; normal, truncated and MaxInt64-declared-entry regressions pass. |
| `internal/hubartifacts/prepare.go:85` | The Helm executable is selected by a local artifact-preparation CLI; arguments are separate and no shell or HTTP-selected command is used. | SAST-116: false positive with a trusted local operator/environment boundary. |
| `internal/portal/browser_proxy.go:267` | Director is replaced with Rewrite. Routing is fixed after hop-by-hop header removal, while principal injection remains in the internal transport. | Fixed and absent from the complete scan; Connection-header identity and routing regression passes through a real loopback Hub/Agent tunnel. |
| `internal/portal/browser_proxy.go:272` | The proxy now explicitly copies the outbound URL before rewriting it. | Fixed and absent from the complete scan; original-URL alias and raw-query regressions pass. |
| `internal/portal/oidc.go:310,354` | Cookies explicitly use `cookieSecureForRequest`; an explicitly configured non-loopback HTTP-IP origin may make Secure false. | Maintainer chose to retain this explicit mode on 2026-11-16; record as accepted risk, not a false positive or encryption mitigation. |
| `web/src/portal/portalNavigation.ts:2` | Call sites use local constant paths or an authenticated logout URL constructed from a validated IdP endpoint. | SAST-119: false positive with server-selected logout destinations; not approval for arbitrary future URL callers. |

## Existing records

Thirteen migrated identities retain the same matched code: SAST-003, 021, 037,
051, 052, 057 and 069–075. Each still requires source-to-sink revalidation;
matching text alone is insufficient. In particular, CodeViewer still uses
fixed themes and Shiki's escaped `codeToHtml` output with the new highlighter.
The dependency source review traced Shiki 4.5.0 token contents to HAST text nodes
and hast-util-to-html 9.0.5 entity escaping, with no custom raw HTML transformer.
The fallback escapes angle brackets before inserting fixed markup; search
highlighting uses Text nodes and textContent. This is static dependency/source
review, not a fresh browser payload test.

SAST-003 and SAST-021 still match error-message strings, not socket creation.
The legacy user-token startup branch is unreachable through the current CLI:
legacy Cloud configuration is rejected before it and Hub startup permits only
impersonation. Restoring legacy user-token support would require a new strict
tunnel review; this cookie risk decision does not authorize token transmission.

SAST-038 requires a new decision together with the two OIDC-cookie findings:
its old evidence restricted insecure cookies to an explicit loopback setting.
The HTTP-IP behavior is a broader boundary. On 2026-11-16 the maintainer chose
“保留显式配置的内网 HTTP-IP 模式” in the current Codex task. This covers only the
explicitly configured authority and does not waive arbitrary HTTP destinations,
host spoofing, new findings or later source changes. Cookies on that HTTP path
can be captured or modified by a network attacker; HttpOnly and SameSite do not
provide transport encryption. HTTPS remains the secure transport mode.

Policy schema v2 records these cases as `accepted-risk`, requires a separately
typed maintainer decision, scoped residual risk and conditions, and exposes
them in `acceptedRisks` rather than the false-positive/mitigated list. The
original raw findings remain present. Unknown, unapproved, malformed, stale or
source/rule-mismatched entries still fail the gate. Schema v1 remains supported
without changing its receipt shape.

The old SAST-112 source in `internal/portal/agent_hub.go` was removed with the
legacy Fleet path. Preserve its historical review; a new Hub origin finding
must receive its own identity and review rather than inherit that approval.

The source review was performed by Codex and the independent
`actions_cost_audit` reviewer. New false-positive records name that delegated
review rather than inventing human approval. The three cookie records name the
maintainer's separate explicit decision. Other historical approvals remain
unchanged and retain their original scope. The independent review also required
strict nonblank string validation for the risk acceptor and reference; malformed
identities, references, scope and conditions are covered by rejection tests.

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
