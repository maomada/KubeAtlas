# Standalone SAST dispositions

The pinned Semgrep scan always retains its original `findings.json`, including
reviewed false positives. Scanner errors, incomplete coverage, inconsistent exit
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

The November 3 approval covers the documented 98 candidates at source
`7e6bed921fece69b38e8ef6a0f4a26154557ba63`. Of these, 97 have active dispositions;
SAST-110 remains on hold pending native Worker evidence. The source-bound review
records are not approval for other findings, future code, merging or publishing.
The user delegated subsequent false-positive review to Devin: each future decision
must still record its own evidence, reviewer and exact source/rule binding rather
than automatically importing all new findings as approved.

This CI decision is not a GitHub-enforced merge restriction. The repository's
current private-account ruleset page says rules will not be enforced. No GitHub
settings, CodeQL configuration or release policy are changed here. Semgrep CE is
not equivalent coverage to CodeQL, and publication remains independently gated.
