# AI engineering workflow

This is the development contract for `maomada/KubeAtlas`, not release approval.
Use one coordinator, one writer per owned path, an independent reviewer, and a
QA owner. Small changes can use one implementation session and one independent
review; five agents are not a default requirement. Keep at most two active work
units unless the maintainer approves a different budget.

## Task and branch contract

Before implementation, fill in [the task contract](engineering-task.example.yaml):
scope, exclusions, full base SHA, paths, dependencies, exact commands, evidence
kinds, resource ownership, and permissions. The example grants no permissions.
Do not store credentials, kubeconfig contents, or private keys in it.

Use a separate clone/worktree and one writer per branch. Changes to shared
contracts belong to the coordinator, not concurrent writers. A temporary remote
branch is an explicit exception to the maintainer's `main`/`fleet-manager`
branch policy; record its authorization and eventual retirement decision.
Do not delete branches or assume `fleet-manager` remains a required long-lived
track without a maintainer decision. Do not create a stack unless requested.

The coordinator freezes interfaces before parallel work. Handoffs name the
exact branch and SHA, owned paths, decisions, checks already run, failures,
running processes to reuse, resource ledger, and next owner. Reuse completed
evidence only for its exact tested commit and artifacts. A changed candidate
invalidates its acceptance, not the retained history of earlier failures.

## Lifecycle

Scope/permissions → design/path ownership → implementation/focused checks →
candidate SHA freeze → independent review → risk-appropriate QA → gate summary
→ authorized merge → final-main verification. Publishing is a separate stage.

Record reviewer and QA identities and reviewed/tested SHAs. The writer cannot
provide their own independent approval. An unresolved finding or external
blocker stays visible; after two unsuccessful repair rounds, escalate the
diagnosis rather than removing assertions or repeatedly running expensive jobs.
Batch related fixes, and run focused checks first and the final required set
once. Preserve failing logs as well as passing evidence.

## Gate plan and evidence

`scripts/engineering_gate.py` has two layers:

* Workflow terminal jobs evaluate every expected job, not just workflow color.
* `aggregate` validates downloaded CI, Security and Package validation receipts
  against a separately reviewed, frozen plan.

All three workflows must supply a receipt. Main selects the full job set;
PRs can omit only jobs deselected by the path plan. Deferred draft heavy jobs
are **not** merge-ready evidence: their terminal gate fails until they execute
successfully. Missing selectors, jobs, receipts, wrong SHA/run/attempt, failed,
cancelled, blocked or unexpected skipped results block the gate. Documentation
changes that are package inputs still require package checks.

The receipt records the PR head SHA separately from the tested merge SHA
(`github.sha`). A base-branch update requires evidence for the new merge
candidate. Each receipt includes origin, event, run and attempt. Never relabel
local results as Actions. Local checks should record exact HEAD/tree, command,
exit status and environment separately; the existing local release verifier
remains authoritative for local **release** acceptance.

Before using the offline aggregate, the coordinator must obtain receipts from
the latest matching workflow run/attempt using GitHub's authenticated API,
verify workflow/event/repository/PR identity and current head/merge SHA, and
freeze those run IDs and job selectors in the plan. Do not derive requirements
from a submitted receipt, accept an old successful attempt over a newer failure,
or execute code from downloaded artifacts. Recheck latest runs and candidate
identity immediately before merging. Downloaded JSON is not a cryptographic
attestation; the offline validator does not query GitHub or authenticate files.

The plan uses schema `kubeatlas-engineering-plan/v1`, repository
`maomada/KubeAtlas`, origin `github-actions`, profile `pr-ready` or `main`,
full `head_sha` and `tested_sha`, and exactly three `gates`: `ci`, `security`,
`packages`. Each gate has string `run_id`, string `run_attempt`, and
`required_jobs` mapping every job ID to a boolean. CI IDs are defined in
`CI_JOBS` in the validator; Security has `sast`; Packages has `changes` and
`validate`. The workflow detector outputs determine booleans, not observed
success/skip states. All unconditional jobs remain required.

```sh
python3 scripts/engineering_gate.py aggregate --plan reviewed-plan.json \
  ci.json security.json packages.json
```

This establishes an executable fail-closed contract, not a claim that GitHub
branch protection or rulesets enforce it. The maintainer must verify repository
permissions/plan eligibility and configure required checks separately. The
cross-workflow collection is operator-driven in this first implementation;
there is no privileged `workflow_run` executing untrusted PR code. Existing
standalone Semgrep and release approval checks are not weakened or replaced.

## QA and Kubernetes safety

Keep `static`, `unit`, `mock-api`, `real-api-kind`, `real-external-cluster`,
`native-container`, `native-host`, and `real-browser` evidence separate.
Kind uses a real Kubernetes API but does not prove an external production
cluster. A container install is not a host boot/systemd test. Semgrep is not
CodeQL. Administrative setup is not demonstrated product capability.

Cluster writes need an approved context/resource scope and an owner ledger.
Declining a prompt or failing preflight must produce zero cluster mutations.
Create, never adopt, fixture resources; clean only recorded owned UIDs with
server-side deletion preconditions. Preserve cleanup errors and uncertain
creation responses for operator reconciliation, not broad deletion retries.
Pin the kubeconfig/context for both kubectl and the tested process. Do not
print or commit credentials. Run RBAC smoke only on an authorized disposable
cluster; its negative-user cases are not safe on arbitrary existing RBAC.

Use the RBAC harness with an explicit context:

```bash
KUBEATLAS_TEST_CONTEXT=<disposable-context> scripts/test-rbac.sh
```

`--help` is safe before cluster access. `--yes` skips only the confirmation;
`--skip-build` skips building but marks binary provenance unknown. The default
binary is `./kubeatlas`; `KUBEATLAS_BIN` may select an explicit test binary.
The script prints its binary SHA-256, version, checkout SHA/state, selected
context, and external evidence directory. It snapshots the selected context
into a private kubeconfig and passes that same frozen file and context to
kubectl and KubeAtlas. It refuses to start if its loopback port is occupied
and rechecks the child process after HTTP readiness. Run-unique namespaces,
ClusterRoles, and bindings use create-only API operations. The fixed CRD name
is checked for collision and created only when absent.

Each successful create is durably recorded in an external, non-secret JSONL
ledger with kind, API version, namespace, name, UID, and the run-owner label.
Cleanup walks only successful ledger entries in reverse order, rechecks the
live UID and owner label using structured `kubectl get` results, and sends
server-side delete preconditions for UID and resourceVersion. A successful
delete request is followed by `kubectl wait --for=delete --timeout=30s` for that
exact resource to become absent; timeout or lookup failure makes cleanup fail,
with no destructive retry.
Uncertain create responses are never inferred to be owned or deleted; the
ledger is retained for manual reconciliation. Cleanup failure makes the
harness fail, even if the behavioral assertions passed.
Evidence defaults to `~/.kubeatlas/test-runs` (override with
`KUBEATLAS_RBAC_EVIDENCE_DIR`); private kubeconfig material is removed on
exit. A custom `KUBEATLAS_BIN` outside the checkout's `./kubeatlas` output
reports provenance as unknown, even when the script runs `make build`. These
safeguards do not make the harness suitable for a shared cluster.

`scripts/visual-test-start.sh` starts `./kubeatlas --no-browser`, checks for an
active recorded instance and a free local port, and fails on early child exit
or HTTP readiness timeout. `--skip-build` reports binary provenance as
unknown; full isolation for parallel visual servers remains follow-up work.

Per-run Kind leases, parallel frontend build ordering, fully isolated browser
servers, and real Portal/Fleet two-cluster QA remain follow-up work, not claimed
as implemented by the P0 safety fixes. See the relevant demo README before use.

## Merge and publication

Record merge authorization independently from branch creation. Never bypass
security gates, buy quota, or retry a billing-blocked run to get a green badge.
If a hosted run cannot start, report it as blocked; a local pass does not change
that result. Do not claim final-main acceptance from a PR merge simulation.

Publishing additionally needs exact artifact/version/action approval, complete
active release gates, native/real-cluster/browser acceptance and offline signing
under [the release policy](ci-cd.md). Production private keys remain on the
user-controlled signing machine. Fleet code on main does not itself authorize
Fleet distribution or prove multi-cluster readiness.
