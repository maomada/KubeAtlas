# Fleet source audit settings

`get_cluster_audit` and the per-resource `auditSummary` use the same
`settings.EffectiveAudit()` policy and `audit.ApplySettings` filter. Operator
settings take precedence over local preferences; absent overrides retain the
defaults, including `kube-system` and `*-system` (for example, `hero-system`).
An explicit empty policy removes these settings exclusions.

Namespace matching is the existing exact/prefix/suffix behavior, not globbing:
`dev-*` matches a prefix, `*-system` matches a suffix, and `*` matches everything
(including the empty namespace of cluster-scoped subjects). Interior asterisks
are literal; surrounding asterisks do not implement substring matching.
Disabled checks and ignored namespaces leave both findings and evaluation counts.

## Response scope

Both responses include `scope`:

- `status: checked` means the scan ran under the reported settings. It does not
  mean every check ran or passed; `missingInputs` still reports evidence gaps.
- An ignored resource instead has a non-null `auditSummary`, `count: 0`, and
  `scope.status: skipped`, with reason `ignored_namespace`. It has no findings
  or highest severity. This is not a clean bill of health.
- `ignoredNamespacePatterns` lists effective exclusion patterns.
- `skippedNamespaces` identifies excluded namespaces from the caller-visible
  scan or explicit authorized selection. It is not a full namespace inventory.
- `disabledChecks` lists effective disabled check IDs, with reason
  `disabled_checks`; zero findings with disabled checks do not prove compliance.
- A namespace-denied aggregate has status `skipped` and reason
  `no_visible_namespaces`, without enumerating hidden namespaces.

An aggregate scan stays `checked` even when a selected namespace is ignored:
cluster-scoped subjects can still be evaluated under their exact grants. Its
`skippedNamespaces` explains the namespaced exclusions. Settings are filters,
not authorization: existing namespace, Secret and cluster-resource gates remain
in force before aggregate findings/counts are exposed.

Aggregate severity counts, resources and total count reflect visible findings
after settings, severity and category filtering, before the finding limit.
Category counts remain after severity filtering and before category filtering.
Per-resource rollups retain the canonical Checks severity ladder; returned raw
detail findings keep audit-engine severity. Findings are static posture, not
evidence of an active outage.

## Local regression checks

The tests use fake Kubernetes clients, not a real cluster or Actions evidence.
They compare actual resource findings against namespace aggregate findings,
cover default exclusions and disabled checks on both paths, preserve namespace
RBAC, and pin the old namespace-matching semantics and explicit skipped JSON.

```sh
go test ./internal/auditcontext ./internal/mcp -run 'Audit|SummarizeResource'
(cd pkg && go test ./audit ./resourcecontext)
go vet ./internal/auditcontext
(cd pkg && go vet ./audit ./resourcecontext)
```
