# PR #19 security revalidation

[Security run 44519547365](https://github.com/maomada/KubeAtlas/actions/runs/44519547365)
scanned 1,994 production files with Semgrep 1.180.0 and the unchanged rules lock.
Its tested merge source `e8730a50645adf47959b2f62724e51f4c4e85bd8` has the
same Git tree as reviewed head `c97dbfbd76733ce78558f249d1137789d1c2425b`.
The original report contains 112 findings and no scanner errors. The raw
report SHA-256 is
`a84a7bdc4294340fe3d8961e0e4c641e3819870ba58062d0f77c2c8f4f1f6d89`.
The reviewed production-source SHA-256 is
`6546ef0a437e5c6c99039953a0413e79a56572f13f96480dbf5231ee982dd530`.

The 108 prior records all match a finding in the new report. Twenty-one
locations moved. Of the prior records, 49 reside in byte-identical files and
58 retain the same matched source in changed files. The remaining source
change is SAST-103. The compatibility pattern adds a fixed legacy marker
spelling, and the replacement index still comes from a numeric capture and
a valid evidence location. It cannot inject regex syntax.

Independent review traced the changed Go and JS/TS callers and guards,
including the preserved executable, escaping, identity, and transport
boundaries. The three prior HTTP-IP risk acceptances retain the original
maintainer approval and authority conditions. The current Portal adds
legacy names for the same session and state values and rejects duplicate
or mixed credentials. The new deletion headers contain no credential.

| Finding | Technical decision and scope |
| --- | --- |
| SAST-120 | The operator-pinned OpenCost gzip archive is limited to 8 MiB on input and 32 MiB expanded, with a sentinel byte to detect excess and integrity errors rejected before loading. A valid tar fixture exceeding the expansion budget is rejected. This mitigates the reported unbounded expansion path. |
| SAST-121 | The legacy OIDC login header expires the old state cookie with an empty value and `MaxAge=-1`. It retains the request's existing Secure policy. The deletion header does not issue OIDC state. |
| SAST-122 | The legacy OIDC callback header expires the old state cookie with an empty value and `MaxAge=-1`. It sends no transaction state or token. |
| SAST-123 | The legacy Portal session header expires the old cookie with an empty value and `MaxAge=-1`. It sends no session value. |

SAST-121 through SAST-123 are false positives for credential exposure in the
specific deletion writes. The actual session and OIDC state writes remain
covered by SAST-038, SAST-117, and SAST-118. Codex reviewed the findings with
independent archive and Go review from `release_audit`, browser and regex
review from `source_gap`, and Portal cookie review from
`actions_validation_audit`. The refreshed policy preserves the scanner,
targets, and matching rules. With this report it evaluates 109 technical
dispositions, three separately recorded accepted risks, and zero unresolved
findings. The subsequent Actions run checks the final PR head.
