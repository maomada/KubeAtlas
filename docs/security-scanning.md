# Standalone security scanning

`Security / SAST (Semgrep CE)` runs on main pushes, pull requests and weekly.
It does not require GitHub Code Security, a Semgrep account, or a paid service.
It uses the local Community Edition engine, with metrics/version checks disabled
and no Semgrep platform token. Source is processed on the GitHub Actions runner;
findings are private Actions artifacts, not GitHub code-scanning uploads.

The engine version is pinned in `.github/security-requirements.txt`. The rules
commit and each selected file's SHA-256 are pinned in
`.github/security-rules.lock.json`. The selection is **all security-category
rules** under the upstream Go, JavaScript and TypeScript trees at that commit,
including audit/INFO rules. No per-finding baseline or inline `nosemgrep` bypass
is accepted. Upstream rule patterns may have their own scope and sanitizers.

The [Semgrep Rules License](https://semgrep.dev/legal/rules-license) permits
internal use, not redistribution. Rules are downloaded at the fixed commit
for CI/local scanning and copied unchanged into a temporary config directory,
with their notices retained. They are not vendored or included in artifacts.
Updating the lock requires reviewing the upstream license, rules and findings;
do not remove rules or targets just to obtain a passing result.

## Scope and limitations

The full tracked production Go/JS/JSX/TS/TSX/MJS/CJS sources under `cmd`,
`internal`, `pkg`, `web/src` and `packages` are selected, not only changed lines.
Both Go modules and both frontend workspaces are included. Unit/E2E fixture
code, `cmd/testserver`, `testdata`, dependencies, generated Wails bindings,
type declarations and built `dist` assets are outside this SAST scope. The
selector is in `scripts/security_scan.py`; normal tests still run in CI.

All unreviewed findings block, irrespective of severity. Exact-source reviewed
false positives and verified mitigations are separately recorded under
`docs/security-dispositions.md`; the original scan is never filtered.
Scanner/config/parse errors,
missing or changed rules, incomplete file coverage, and empty scans also block.
The job exercises a clean canary and deliberately unsafe Go/JS/TS fixtures;
these are synthetic detection tests, not real-cluster or product acceptance.
JSON findings and exact source/rules provenance remain available on failure.

This is **not CodeQL-equivalent coverage**: Community Edition lacks CodeQL's
whole-program analysis and the paid Semgrep cross-file engine. This gate does
not scan dependencies, secrets/history, runtime RBAC, or Helm configuration.
Dependabot and existing tests remain separate. Passing means complete scanning
and no unresolved findings from these pinned rules on this scope, not that raw
findings are absent or that the application is vulnerability-free.

## Local reproduction

Use an isolated Python 3.12 virtual environment and install
`python3 -m pip install -r .github/security-requirements.txt`. Clone
`https://github.com/semgrep/semgrep-rules` outside this repository and check out
the exact commit in the lock. Then run:

```sh
python3 -m unittest discover -s scripts -p 'test_security*.py'
python3 scripts/security_scan.py --rules-source /path/to/semgrep-rules --output /path/to/evidence --self-test
python3 scripts/security_scan.py --rules-source /path/to/semgrep-rules --output /path/to/evidence
```

## Migration state

Release source selection requires successful latest main-push `ci.yml` and
`security.yml` runs for the exact selected SHA. Missing, failed, skipped,
cancelled or incomplete runs block release; a PR merge run is not main-push
evidence. Scanner errors, incomplete coverage and unreviewed findings fail the
Security workflow before source selection can accept it.

CodeQL analysis configuration and historical eligibility failures remain intact.
Its workflow is manual-only to avoid repeated ineligible builds and analyses;
confirm Code Security eligibility before dispatching it. CodeQL
is no longer a required release evidence source; that does not reclassify its
failed runs as successful or imply equivalent coverage. GitHub branch rules
are separate: this private account cannot enforce its current rulesets, and a
workflow file alone does not enforce merge policy. Publication approval remains
independently required; this migration does not configure or bypass it.
