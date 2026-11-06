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
