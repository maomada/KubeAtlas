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

To sign on the authorized offline machine, download `release-approval.json`
from the dry-run's `release-approval` artifact, inspect it, and run:

```sh
openssl pkeyutl -sign -rawin \
  -inkey /secure/path/release-signing-private.pem \
  -in release-approval.json -out release-approval.sig
openssl base64 -A -in release-approval.sig
```

Provide the exact dry-run run ID and the resulting single-line base64 signature
as `build_run_id` and `approval_signature` in a separate `publish=true`
dispatch. A dry-run must omit both inputs. Do not place the private key in this
repository, workflow inputs, runner environment, or logs. Only
`.github/release-signing-public.pem` from the trusted workflow checkout is used
to verify approvals; an absent or invalid production key blocks approval
creation/publication. No production public key is currently provisioned, so
the workflows remain fail-closed until the authorized public key is installed.

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

The full GoReleaser build and validation still cover 15 artifacts. A separate
strict `local` publication profile stages only the eight Linux DEB/RPM packages
for KubeAtlas and Portal plus the KubeAtlas Windows amd64 ZIP. Its schema-2
manifest and checksum file make an 11-file publication artifact; the full
15-file `kubeatlas-binary-release` artifact is diagnostic evidence only. No
macOS or Linux tar archive is published. The native Windows smoke must pass
before the dry-run emits an approval.

With `publish=true`, the publisher downloads the signed approval and
`kubeatlas-binary-publication` from the specified dry-run run, never the
diagnostic artifact. It verifies the signature, run provenance, and inventory
before setup, then reruns source policy and local-profile validation. After
tag/release collision checks it verifies approval again immediately before
creating the tag. The protected `ghcr-publish` environment is retained as
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
