# Standalone SAST dispositions

The pinned Semgrep scan always retains its original `findings.json`, including
reviewed false positives and mitigated findings. Scanner errors, incomplete coverage, inconsistent exit
codes and missing reports fail before any disposition is considered. The unsafe
Go/JS/TS canary scan never applies production dispositions.

`.github/security-dispositions.json` records each finding's exact rule, relative
path and start/end position, its evidence and approval. The entire production
source tree, engine version and rules lock are bound to the review. A source or
rule change requires a new review; editing an unrelated Python test does not
change the production-source fingerprint. Duplicate, missing, malformed or stale
records fail closed. No global rule exclusions or `nosemgrep` directives are used.

The gate emits a separate `dispositions.json` with raw counts, approved matches,
unresolved findings, source SHA, raw-report hash and policy hash. Only a complete
scan with no unresolved findings passes. A `hold` remains blocking even when the
user has approved the candidate's classification. User approval does not turn
missing technical evidence into a verified safeguard.

The original approval covers the documented 98 candidates at source
`7e6bed921fece69b38e8ef6a0f4a26154557ba63`; SAST-110 was held pending evidence.
The follow-up source review at `7c2a68f379f94a31351d8ad932ddc4b9704dfc8b`
retains 101 raw findings: 98 false positives and 3 explicitly mitigated findings.
`mitigated` requires the same approval/evidence binding and is never relabeled
as an originally safe implementation. The source-bound review
records are not approval for other findings, future code, merging or publishing.
The user delegated subsequent false-positive review to Devin: each future decision
must still record its own evidence, reviewer and exact source/rule binding rather
than automatically importing all new findings as approved.

Schema v2 also supports `accepted-risk` after an explicit maintainer decision.
This is separate from false positives and verified mitigations: the gate emits
these findings in `acceptedRisks`, while the raw report remains unchanged. Each
record requires `approval.kind: maintainer-risk-acceptance`, a matching named
acceptor, an affirmative decision, its exact scope, residual risk and nonempty
conditions. Generic prior approval cannot be reused for this category. A new
finding, stale location, changed production source or changed scanner/rules still
blocks until reviewed. Schema v1 retains its existing decision output.

For PR #17, the maintainer explicitly chose to retain the configured intranet
HTTP-IP login mode on 2026-11-16. SAST-038, SAST-117 and SAST-118 record its session
and OIDC-cookie transport risk. Cookies on this HTTP path can be observed or
modified on the network; HttpOnly and SameSite do not encrypt them. The decision
is scoped to the configured authority and does not approve other HTTP endpoints
or future changes. See [the source review](security-revalidation-pr17.md).

[PR #19 revalidation](security-revalidation-pr19.md) records the first-release
source mapping, the OpenCost archive bound, and the legacy cookie cleanup.
[PR #22 revalidation](security-revalidation-pr22.md) records the source binding
for the branding changes and the one relocated finding.

## Remaining-finding decisions

| ID | Decision and scope |
| --- | --- |
| SAST-060 | Mitigated: ZIP aggregate extracted bytes capped at 1 GiB with declared and actual-byte checks. Synthetic limit/traversal tests are not real updater acceptance. |
| SAST-061 | Mitigated: parsed HTTP(S)/relative links only, no credentials; native browser rejects dangerous links without navigation/callback. Explicit cross-origin cluster selection remains intentional; host supplies trusted destinations. |
| SAST-066 | Fixed and absent from current raw scan: constant sanitized resource warning. |
| SAST-081 | False positive: literal-only pattern is HTML-escaped and regex-metacharacter-escaped before compilation; arbitrary regex uses Worker ranges. |
| SAST-106 | Fixed and absent from current raw scan: constant sanitized Helm warning. |
| SAST-110 | Mitigated, not safe-regex classification: all user-regex compilation/execution isolated in terminated Workers; native browser evidence resolves the previous technical hold. |

Native Chrome evidence uses production-bundled components and fixture inputs,
not a real cluster. It covers module Worker loading, invalid regex, catastrophic
timeout with responsive UI, query/Escape/API cancellation and recovery, streaming
deadline, range-limit errors, HTML escaping, `worker-src 'self'` success and
`worker-src 'none'` explicit rejection without fallback. Tested source is
`7c2a68f379f94a31351d8ad932ddc4b9704dfc8b`; emitted Worker SHA-256 is
`8557869ab62fc96f61787d8fa79287b4180088c8e1eac63413edea4a0524b408`.
[Recording and review](https://app.devin.ai/sessions/a83667070d6a45518af95192c1780a37?testRecording=63c031fe-94cc-4978-a927-f1577df0fd51).

Unchanged original entries retain their original approval and evidence plus a
revalidation SHA. The prior source/report fingerprints and SAST-110 hold are
retained in the policy history. No old raw finding is deleted from its report.

This CI decision is not a GitHub-enforced merge restriction. The repository's
current private-account ruleset page says rules will not be enforced. No GitHub
settings or CodeQL configuration are changed by dispositions. Semgrep CE is
not equivalent coverage to CodeQL, and publication remains independently gated.

## Portal Fleet integration review

Source `25828e1f1e87df69726ca76374e4e617e587a64b` retains the 101 previously
reviewed raw findings and adds SAST-112, the Gorilla missing-origin-check audit.
The pinned Gorilla Upgrader supplies a same-host check when `CheckOrigin` is nil;
it does not accept arbitrary browser origins. The reachable source-channel route
also rejects cross-site Fetch Metadata and requires a cluster registration bearer
token before upgrading. An absent Origin remains valid for outbound Go clients.
`TestFleetChannelOriginAndBearerBoundary` covers these handshake boundaries.
SAST-112 is classified as a false positive for this rule, not a general security
approval of the Fleet feature.

Existing finding files are unchanged from main except the CLI entrypoint, Portal
handler and server route file. Their four existing sinks and relevant guards are
unchanged; only finding positions moved. Their prior evidence and approvals are
retained, with explicit source revalidation and previous positions. The review
history preserves prior fingerprints. No scanner rule, exclusion or gate was
relaxed; the source-tree fingerprint still requires review after later changes.

## PR #16 scoped startup and MCP-host review

The exact PR branch head is `0de3ec82a2913ffd441aaad1bc14a43363d2026a`;
its full Git tree is identical to the tested merge source
`a377fcea3a13b030f53ebe7afc52414bdf3ffc73`. The reviewed tree contains 1,953
production SAST targets and has source-tree SHA-256
`8db54c8dc88ab3aaa89427b7900d7aec9c161eb2a4ec180f790f2cdb7d79092d`. The
preserved [Security run 43839128360](https://github.com/maomada/KubeAtlas/actions/runs/43839128360)
used Semgrep `1.180.0`, rules commit
`16b9e6d45f10649f6a531ec4838ed9924372c3bc`, and the unchanged rules-lock
SHA-256 `296024fcf2db65e68a0d7410d2723611ddf1d4aae917a0cd82b5bc57ebcd6f5d`.
Its raw report has 102 findings, no scanner errors, and SHA-256
`5dec9a163cf753fabb0444e5b5f89743c190675cea023cf8271398346e30b5cc`; the
artifact and prior policy/report fingerprints remain in review history.

The four production changes were reviewed together: `cmd/explorer/main.go`,
`cmd/kubeatlas-portal/main.go`, `internal/fleetsync/handler.go`, and
`internal/portal/handler.go`. The startup change moves `StartFleetSync` after
`InitializeCluster`; `TestFleetSyncStartsAfterClusterInitialization` enforces
that order. The Portal configuration accepts exact external authorities only
for `/mcp-fleet` behind a trusted proxy. Parsing rejects wildcards, schemes,
paths, user information, query/fragment, whitespace and invalid ports; request
trust compares the actual `Host`, not `X-Forwarded-Host`. The Portal route
requires a viewer session and rejects `Sec-Fetch-Site: same-site` and
`cross-site` before dispatch, while the MCP handler retains cross-origin
protection and the request-size limit.
`TestFleetMCPAllowedHostsValidation` and
`TestPortalFleetMCPProxyHostProtection` cover invalid authorities, exact
host/port matching, forwarded-host spoofing, cross-origin/site requests and
missing sessions. TLS and proxy isolation remain operator responsibilities.

Two existing findings are specifically revalidated:

| ID | Candidate identity | Source-to-sink evidence |
| --- | --- | --- |
| SAST-003 | `cmd/explorer/main.go:279` | The `ws://` match is in a fatal-error string, not a dial argument. Before user-token tunnel startup, the non-enrollment path rejects insecure-skip-verify and requires `cloud.ValidateWebSocketURL`; enrollment validates the Hub-returned endpoint separately. That validator allows plaintext `ws` only for loopback hosts. The startup-order change does not modify this guard. |
| SAST-037 | `internal/portal/handler.go:190` | The finding moved from line 184 because of the added configuration field and constructor setup; the `path.Clean` expression is unchanged. The route first requires `/assets/`, then rejects a cleaned name outside `assets/`, and `serveFile` reads through `fs.FS.Open`. The sole production caller supplies `fs.Sub(portalui.FS, "dist")`, backed by `//go:embed`; the value is not joined to an OS path. The existing false-positive classification and approval remain unchanged. |

The independent source revalidation and diff review are attributed to
`maomada` in the [coordinator session](https://app.devin.ai/sessions/a83667070d6a45518af95192c1780a37).
The automated Devin Review’s “No Issues Found” receipt (`5917635382`) and human
approvals (`5918089792`, `5918102972`) remain separate records for the exact
PR head. These are code-review receipts, not a blanket approval of unreviewed
future findings or a change to the disposition approval history.
All other findings in byte-identical production files retain their existing
evidence and approvals. No scanner rule, suppression, or gate was changed.
