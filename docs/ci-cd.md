# CI/CD and owned GHCR distribution

Main and pull-request CI build and test only. This repository does not claim
that any image or chart has already been published. A maintainer may dispatch
**Release GHCR** from `maomada/KubeAtlas` on `main` with a full lowercase 40-digit
source SHA and a strict SemVer version (no `v` prefix or build metadata).
The policy requires that SHA to be an ancestor of `main` and that the latest
main-push CI and Semgrep CE runs for that exact SHA both succeeded.

The required workflow files are `ci.yml` and `security.yml`. Missing, failed,
cancelled, skipped or incomplete latest runs block release; a successful PR
merge run does not substitute for a main-push run. CodeQL configuration and
its prior eligibility failures are retained, but no longer satisfy or block
this release source-selection check. Semgrep CE is not CodeQL-equivalent
coverage; see [Standalone security scanning](security-scanning.md).

Both release workflows use an offline Ed25519 approval in addition to the
current source, ancestry, CI, Semgrep CE, artifact, and collision checks. A
`publish=false` dispatch validates and builds immutable dry-run artifacts,
then emits `release-approval.json`; it does not publish. Review the release
manifest, exact artifact inventory, checksums, build metadata, and CI/Semgrep CE
evidence before signing. Binary approval additionally requires the native
`windows-latest` smoke of the actual Windows ZIP; that job checks its manifest
hash, safely extracts it, and runs `kubeatlas.exe --version` and `--help` with
checked exit codes. It does not configure kubeconfig or claim a cluster test.

The approval binds the repository, action, channel, selected source and version,
workflow path and SHA, dry-run run ID and attempt, CI/Semgrep CE run URLs,
targets, key fingerprint, expiry, and every artifact's path, size, and SHA-256.
The detached signature covers the exact JSON bytes. Do not edit or reformat the
downloaded manifest before signing. The approval expires within seven days;
publication rejects workflow reruns and verifies the signed dry-run run using a
read-only GitHub Actions API query. The publication workflow downloads only the
fixed artifact names from that exact run and never rebuilds after signing.

`approvalId` is an audit UUID, not a consumed nonce. The same signed bytes may
be used in another publish dispatch before expiry if no target writes have
occurred. The publisher still rejects workflow reruns, and its collision checks
block successful, partial, or unknown prior writes from being overwritten.
Reconcile uncertain state and use a fresh version rather than retrying against
existing targets. A changed workflow SHA or public key, an expired approval, or
lost dry-run artifacts requires a fresh dry run and signature.

## CI compute controls

CodeQL is manual-only while this private repository lacks Code Security
eligibility. Its analysis configuration and failure history are retained; do
not dispatch it until eligibility is confirmed. Semgrep still runs on main
pushes, pull requests (including drafts) and its weekly schedule.

Keep work-in-progress PRs as drafts and batch related fixes before pushing.
Drafts retain unit tests, installer tests, workflow lint, release-policy tests
and Semgrep, but defer multi-platform container builds and the full binary
package matrix. Marking a PR ready triggers those builds for the current diff;
check that exact revision before merging. A skipped draft job is not acceptance
evidence. Superseded PR package-validation runs are cancelled; main-push runs
remain independent. Package validation has a 60-minute fail-closed timeout.

Changes to CI orchestration still select all CI jobs. Other `.github` changes
no longer select unrelated application builds solely by directory; workflow
lint and release-policy tests remain unconditional. Existing source/dependency
filters still apply. Main CI still runs all jobs; main package-validation path
filters, the full 15-artifact release build, native Windows smoke, exact-SHA
main-push CI/Semgrep evidence, signatures and publication gates are unchanged.

These controls reduce future repetition; they do not erase accrued charges,
restore an exhausted Actions allowance, or change billing settings. Do not
automatically retry account billing failures or increase spending limits.

## Offline signing key custody

Generate the production key only on the authorized user-controlled signing
machine, outside the repository and CI. This separates signing from the
publisher; it does not imply that a LocalHands-connected machine is air-gapped.
Run these commands only on that machine, never in this repository or CI. They
create a new dedicated user-owned directory and fail if that directory already
exists; do not reuse a directory or overwrite an existing key:

```bash
set -euo pipefail
umask 077
key_dir="$HOME/.local/share/kubeatlas-release-signing"
if [ -e "$key_dir" ] || [ -L "$key_dir" ]; then
  printf 'Refusing to reuse existing key directory: %s\n' "$key_dir" >&2
  exit 1
fi
mkdir -m 700 "$key_dir"
private_key="$key_dir/release-signing-private.pem"
public_key="$key_dir/release-signing-public.pem"
if [ -e "$private_key" ] || [ -L "$private_key" ] || \
   [ -e "$public_key" ] || [ -L "$public_key" ]; then
  printf 'Refusing to overwrite an existing signing key\n' >&2
  exit 1
fi
openssl genpkey -algorithm Ed25519 -out "$private_key"
openssl pkey -in "$private_key" -pubout -out "$public_key"
chmod 600 "$private_key"
chmod 644 "$public_key"
openssl pkey -pubin -in "$public_key" -outform DER | openssl dgst -sha256
```

The final command prints the DER SubjectPublicKeyInfo SHA-256 fingerprint.
After the public key is added to trusted `main`, compare its 64-digit
fingerprint with `keyFingerprint` in a dry-run approval. Provision only
`.github/release-signing-public.pem` through a reviewed commit; the workflow
loads it from the trusted workflow checkout. Keep the private key and an
encrypted, offline backup under user control with restricted access. Never put
the private key in Git, CI, artifacts, workflow inputs, chat, or logs. A committed
public key does not authorize a release by itself; each exact dry run still
requires a separately reviewed signature.

To approve a reviewed dry run, download `release-approval.json` from its
`release-approval` artifact and inspect the exact targets, artifact hashes,
source, version, workflow revision, evidence URLs, fingerprint, and expiry.
Then sign and locally verify the unchanged manifest bytes. The following
`pkeyutl -rawin` commands require OpenSSL 3 or newer:

```bash
key_dir="$HOME/.local/share/kubeatlas-release-signing"
private_key="$key_dir/release-signing-private.pem"
public_key="$key_dir/release-signing-public.pem"
openssl pkeyutl -sign -rawin \
  -inkey "$private_key" \
  -in release-approval.json -out release-approval.sig
openssl pkeyutl -verify -rawin -pubin \
  -inkey "$public_key" \
  -in release-approval.json -sigfile release-approval.sig
openssl base64 -A -in release-approval.sig
```

On a signing machine with older OpenSSL, an existing Python `cryptography`
installation with Ed25519 support can sign the same bytes without replacing
the system OpenSSL. This is an alternative to the commands above, not a second
signature format; CI still verifies the detached signature with OpenSSL. Do not
upload the private key or move signing to CI to work around local tooling:

```bash
python3 - <<'PY'
import base64
from pathlib import Path
from cryptography.hazmat.primitives import serialization
from cryptography.hazmat.primitives.asymmetric.ed25519 import Ed25519PrivateKey, Ed25519PublicKey

key_dir = Path.home() / '.local/share/kubeatlas-release-signing'
with Path('release-approval.json').open('rb') as source:
    manifest = source.read(64 * 1024 + 1)
if not manifest or len(manifest) > 64 * 1024:
    raise ValueError('Approval manifest is empty or exceeds 64 KiB')
private_key = serialization.load_pem_private_key(
    (key_dir / 'release-signing-private.pem').read_bytes(), password=None)
public_key = serialization.load_pem_public_key(
    (key_dir / 'release-signing-public.pem').read_bytes())
if not isinstance(private_key, Ed25519PrivateKey) or not isinstance(public_key, Ed25519PublicKey):
    raise ValueError('Both keys must be Ed25519')
signature = private_key.sign(manifest)
public_key.verify(signature, manifest)
with Path('release-approval.sig').open('xb') as output:
    output.write(signature)
print(base64.b64encode(signature).decode('ascii'))
PY
```

The fallback refuses to replace an existing signature. Both signing methods
assume the manifest has already been reviewed; a successful cryptographic
self-test is not real-artifact acceptance or release authorization.

Provide the exact dry-run run ID and the resulting single-line base64 signature
as `build_run_id` and `approval_signature` in a separate `publish=true`
dispatch. A dry-run must omit both inputs. Only
`.github/release-signing-public.pem` from the trusted workflow checkout is used
to verify approvals; an absent or invalid production key blocks approval
creation/publication.

The approval signature is an application-level gate, not a claim that the
`ghcr-publish` environment enforces review. Repository/workflow administrators
can alter trusted workflow code or key configuration, and registry/release
administrators can make independent changes; those administrators remain
trusted. Keep deployment-branch restrictions and any environment protection
as defense in depth, but do not treat the environment name as signature
verification.

The publish job uses the `ghcr-publish` environment and `GITHUB_TOKEN` with
`packages:write`; builds have no registry login or package-write permission.
The environment name alone does **not** enforce approval. GHCR can
automatically associate newly created packages with this repository when the
first publication uses `GITHUB_TOKEN`. Existing packages must grant this
repository Actions access. Review package visibility and access policy before
any authorized publication; no registry publication is part of this change.

Images:

- `ghcr.io/maomada/kubeatlas:<version>`
- `ghcr.io/maomada/kubeatlas-portal:<version>`

Helm OCI charts:

- `oci://ghcr.io/maomada/charts/kubeatlas --version <version>`
- `oci://ghcr.io/maomada/charts/kubeatlas-portal --version <version>`

For example, after a version is actually published:

```sh
helm upgrade --install kubeatlas oci://ghcr.io/maomada/charts/kubeatlas \
  --version "$VERSION" --namespace kubeatlas --create-namespace
helm upgrade --install kubeatlas-portal oci://ghcr.io/maomada/charts/kubeatlas-portal \
  --version "$VERSION" --namespace kubeatlas-portal --create-namespace \
  --set existingSecret=portal-auth
```

Use a secret manager or existing Kubernetes Secret for credentials. Do not put
registry credentials in chart values, runtime configuration, command arguments,
or this repository. The KubeAtlas chart supports an `imagePullSecrets` value. The
Portal chart does not; its Deployment uses the namespace default ServiceAccount.
For private GHCR images used by Portal, have the cluster operator provision that
ServiceAccount's `imagePullSecrets` before installation. Do not assume packages
are public.

The signature and exact artifact inventory are verified before registry login.
After source-policy, artifact, and registry-collision checks, the signature,
dry-run provenance, and inventory are verified again immediately before the
first push. The workflow inspects all four versioned refs and blocks if one
exists or if absence cannot be established. Images are copied from the exact
archives built earlier; charts are pushed only after both image copies succeed.
The release manifest records source SHA, version, CI/Semgrep CE evidence, actor,
workflow run, archive checksums, build digests, and final registry digests where
known. OCI/chart dry-run artifacts are retained for 14 days; approval and
provenance artifacts are retained for 90 days, but the signed approval itself
is valid for at most seven days.

A registry can fail after some artifacts have been written. The workflow does
not delete, retry, or overwrite artifacts. If a push is partial or its result
is unknown, reconcile registry state and use a **fresh version**; do not rerun
the same version. Roll back by selecting a previously known-good image tag or
Helm revision (`helm history` / `helm rollback`) and verify the matching
manifest digest.

The CLI installers and direct update checks now use `maomada/KubeAtlas`; they do
not fall back to the upstream release relay. Installer scripts fail closed
when the owned release lacks the expected CLI assets. GHCR images do not
provide those assets, and no package-manager update command is advertised until
an owned distribution path exists. The Go module path and npm package names
retain their historical compatibility identifiers; those identifiers do not
publish or install this repository's CLI releases.

Upstream Cloud connectivity and usage reporting are disabled in KubeAtlas.
Legacy environment settings cannot enable them, and the Helm charts reject
enabling those features. See [Usage Stats](usage-stats.md) and
[Authentication](authentication.md#upstream-cloud-mode) for the retained
compatibility boundaries. The independent Portal is not the upstream Cloud.

## Binary archives and packages

The separate `Release Binaries` workflow is manual-only. It accepts the
full `source_sha`, strict SemVer `version`, and `publish=false` dry-run inputs.
It checks main ancestry and exact-SHA CI and Semgrep CE evidence before building.
Builds use trusted release tooling checked out separately from the selected
source, have read-only permissions, and never log in to a registry.

The hosted Actions workflow still validates the full 15-artifact GoReleaser
matrix and uses its strict `local` publication profile: eight Linux DEB/RPM
packages plus the KubeAtlas Windows amd64 ZIP, with a schema-2 manifest and
checksums (11 files). The full 15-file `kubeatlas-binary-release` artifact is
diagnostic evidence only. No macOS or Linux tar archive is published, and
native Windows smoke remains required by that hosted profile. This unchanged
hosted contract is distinct from the `local-linux` profile below and is not
authorized for the scoped 0.1.0 local publication.

With `publish=true`, the publisher downloads the signed approval and
`kubeatlas-binary-publication` from the specified dry-run run, never the
diagnostic artifact. It verifies the signature, run provenance, and inventory
before setup, then reruns source policy and local-profile validation. After
tag/release collision checks it verifies approval again immediately before
creating the tag. The `ghcr-publish` environment is retained as
defense in depth, not as the approval mechanism. The publisher fails closed
unless both versioned tag and GitHub Release endpoints return verified HTTP 404
responses. It creates a draft release targeting the exact source SHA and
publishes only after every upload succeeds. It never overwrites, deletes,
retries, or creates a mutable `latest` tag. Partial state remains for
reconciliation and requires a fresh version.

Run the non-publishing local package dry run with:

```sh
KUBEATLAS_RELEASE_VERSION=1.2.3 make release-binaries-dry
```

See [Binary packages](packaging.md) for install/uninstall behavior, supported
architectures, no-autostart guarantees, and source/version provenance. GHCR
images/charts and GitHub Release binaries are separate authorization paths.
This implementation does not dispatch a release workflow or publish any
artifact.

## Separate local release path

The hosted Actions contract above remains unchanged and fail-closed. The local
path uses its own `kubeatlas-local-release/v1` manifest and `origin: local`;
neither local receipts nor signatures are valid Actions evidence, and Actions
manifests are not accepted by the local verifier. Do not change
`release_policy.py` or `release_approval.py` to bridge the two modes.

Local build/check operations never publish. Run them on a clean checkout whose
full `HEAD` equals the requested source SHA. The bundle must be outside the
checkout, and a build is performed once only:

```sh
export SOURCE_SHA="$(git rev-parse HEAD)"
export BUNDLE="$HOME/.local/state/kubeatlas-release/0.1.0-$SOURCE_SHA"
export RULES_SOURCE="$HOME/semgrep-rules-reference"
PYTHONDONTWRITEBYTECODE=1 python3 scripts/release_local.py build \
  --source-sha "$SOURCE_SHA" --bundle "$BUNDLE"
PYTHONDONTWRITEBYTECODE=1 python3 scripts/release_local.py check \
  --source-sha "$SOURCE_SHA" --bundle "$BUNDLE" --rules-source "$RULES_SOURCE"
```

The scoped 0.1.0 local profile is fixed to `local-linux`. The build still
validates the full 15-artifact GoReleaser matrix, but stages only eight Linux
DEB/RPM packages (KubeAtlas and Portal, amd64 and arm64), `binary-manifest.json`,
and `checksums.txt`. The bundle also contains the two multiarch OCI archives
and two Helm charts. The Windows ZIP is deferred: it is not published or
accepted by this local profile, and no Windows acceptance is claimed. Fleet,
macOS, and Portal Windows distributions are excluded. `LICENSE` and `NOTICE`
remain in the binary packages. This does not change the hosted Actions
`local` profile described above. A candidate branch is not releasable:
approval preparation and verification require that source and tooling are the
same clean commit as a fresh read-only GitHub `main` query.

Prerequisites are the pinned Go/GoReleaser, Node, Helm 4.2.3, Helm 3.19.0,
helm-unittest 1.1.0, helmfile, Docker Buildx, skopeo, ShellCheck, actionlint,
and Semgrep 1.180.0 plus the exact locked rule checkout. Build preflight also
requires Make, npm/npx, and the package inspection tools `dpkg-deb`, `rpm`,
`rpm2cpio`, and `cpio` before creating a bundle. The automated checks
also require an explicitly configured isolated PostgreSQL test DSN in
`KUBEATLAS_TEST_POSTGRES_DSN`; the script forces
`KUBEATLAS_REQUIRE_POSTGRES_TESTS=1`. Missing tools, services, external
acceptance, or receipts block preparation. No build/publish tools are
installed automatically. Settings E2E is a fixture test, not a real cluster
acceptance gate.

External host reports are imported only after the operator has run the
required scenarios. For example:

```sh
python3 scripts/release_local.py import-acceptance \
  --bundle "$BUNDLE" --report /path/to/actual-host-report.json \
  --evidence-dir /path/to/actual-host-evidence
```

The versioned report schema is `kubeatlas-local-acceptance/v1`. It binds the
gate, source/tooling SHA, build UUID, artifact-inventory digest, runtime
platform, context, component/environment versions, command records and exit
status, scenario outcomes, limitations, artifact hashes, and report/log
evidence hashes. Every required scenario is listed separately as `passed`,
`untested`, or `failed`; untested or failed outcomes block approval. The fixed
local-linux contract retains 19 gates: build, Linux CI, frontend, settings E2E,
Helm, release security, POSIX installers, Semgrep self-test and full scan,
OCI/package validation, real-cluster acceptance, and eight per-package
installation gates. Each Linux package gate requires isolated install,
version, uninstall, and no-autostart evidence with explicit architecture and
distribution details. Native Windows is not an active gate for this profile;
Windows acceptance reports are rejected by its importer. Real-cluster
evidence must report the Kubernetes context and version and the main and Portal
flows, including read-only/security boundaries. Emulation is reported
explicitly and cannot substitute for native execution where a gate requires
it. An operator-imported report is **operator-attested evidence reviewed by
the offline signer**, not cryptographically independent host attestation.
Test-only fixtures cannot satisfy these real acceptance gates. Pod-metrics
availability and Hubble plaintext may be reported as limitations; they are not
invented pass criteria.

Use this non-passing JSON template as the report shape. Replace placeholders
with observed values and list every required scenario; do not mark a scenario
passed without its actual command and evidence:

```json
{
  "schema": "kubeatlas-local-acceptance/v1",
  "origin": "operator-attested",
  "repository": "maomada/KubeAtlas",
  "gate": "real-cluster",
  "version": "0.1.0",
  "sourceSha": "<full source SHA>",
  "toolingSha": "<same clean tooling checkout SHA>",
  "buildId": "<build UUID from the bundle>",
  "artifactInventorySha256": "<bundle inventory digest>",
  "executionPlatform": {
    "system": "Linux",
    "machine": "x86_64",
    "emulation": false,
    "isolation": "host"
  },
  "issuedAt": "<UTC RFC3339 timestamp>",
  "completedAt": "<UTC RFC3339 timestamp>",
  "commands": [],
  "exitStatus": 1,
  "result": "untested",
  "context": "<actual Kubernetes context>",
  "componentVersions": {
    "kubeatlas": "0.1.0",
    "kubeatlas-portal": "0.1.0",
    "kubernetes": "<observed version>"
  },
  "limitations": ["<observed limitation, if any>"],
  "scenarios": [
    {"name": "main_readonly_api", "status": "untested", "details": "Not run."},
    {"name": "main_readonly_mcp", "status": "untested", "details": "Not run."}
  ],
  "artifactHashes": {},
  "deployedImageDigests": {},
  "executables": {},
  "toolVersions": {"kubectl": "<observed version>"},
  "evidence": []
}
```

After every required receipt passes, prepare creates only an unsigned
`approval.json`; it never signs:

```sh
python3 scripts/release_local.py prepare --source-sha "$SOURCE_SHA" --bundle "$BUNDLE"
```

Transfer that manifest and its evidence to the authorized offline signing
machine. Use the existing offline Ed25519 custody/signing procedure and the
public key in `.github/release-signing-public.pem`; keep the production private
key off this VM and out of the repository and logs. Return the detached
`approval.sig` without replacing existing evidence. The approval is bound to
the exact fixed targets and per-channel file hashes and expires within seven
days. Revalidate without rebuilding:

```sh
python3 scripts/release_local.py verify --source-sha "$SOURCE_SHA" --bundle "$BUNDLE"
```

Publication is a separate, explicitly authorized operation. It requires a
fresh main query and GitHub credentials via `GH_TOKEN` or `GITHUB_TOKEN`, plus
a mode-0600-or-stricter preauthenticated GHCR authfile (PAT classic with
`write:packages`, or an existing authfile):

```sh
python3 scripts/release_local.py publish --source-sha "$SOURCE_SHA" \
  --bundle "$BUNDLE" --authfile "$HOME/.config/containers/auth.json"
```

The publisher preflights every fixed GHCR target and the GitHub tag/release
before its first write. A durable version-scoped journal is created outside
the bundle; any prior attempt or uncertain outcome blocks retries. No
overwrite, delete, rollback, cleanup, or automatic retry is performed.
Registry/GitHub writes are not globally atomic across machines, so use one
publisher and reconcile partial publication manually before making any new
release decision. The audit bundle remains separate from the published binary
whitelist; retain it immutably with its signed approval. Do not log tokens,
authfile contents, kubeconfigs, or environment dumps.

Hosted Actions may remain unavailable because of account billing/spending
limits; this is a cost/availability caveat, not a reason to weaken their
validators. The local path does not dispatch workflows and does not create an
Actions run ID.
