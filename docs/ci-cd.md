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

The private account currently cannot enforce the intended protected-environment
approval. Do not dispatch publication until its separately reviewed replacement
is implemented and validated. This source-selection migration neither removes
the environment requirement nor claims that a named environment enforces review.

Run once with `publish=false` first. It builds each multi-platform image into
an OCI archive and packages both charts using matching `version`, `appVersion`,
image repositories, and image tags. The dry run proves neither GHCR
permissions nor collision checks nor a successful registry push. Review the
manifest artifact, archive checksums, build digests, chart checksums, and CI /
Semgrep CE links before a separate explicit dispatch with `publish=true`.

The publish job uses the `ghcr-publish` environment and `GITHUB_TOKEN` with
`packages:write`; builds have no registry login or package-write permission.
The environment name alone does **not** enforce approval. A repository
maintainer must configure required reviewers and restrict deployment branches
to `main` in repository settings before enabling publication. GHCR can
automatically associate newly created packages with this repository when the
first publication uses `GITHUB_TOKEN`. Existing packages must grant this
repository Actions access. Review the intended package visibility and access
policy before publishing; neither environment protection nor package
visibility was configured by this change.

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

Before any write, the workflow inspects all four versioned refs and blocks if
one exists or if absence cannot be established. Images are copied from the
exact archives built earlier; charts are pushed only after both image copies
succeed. The release manifest records source SHA, version, CI/Semgrep CE evidence,
actor, workflow run, archive checksums, build digests, and final registry
digests where known.

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

With `publish=true`, the protected `ghcr-publish` environment is required. The
publisher downloads already-validated artifacts rather than rebuilding them,
revalidates policy and hashes, and fails closed unless both versioned tag and
GitHub Release endpoints return verified HTTP 404 responses. It creates a
draft release targeting the exact source SHA, uploads only manifest-validated
artifacts plus `checksums.txt` and `binary-manifest.json`, and publishes only
after every upload succeeds. It never overwrites, deletes, retries, or creates
a mutable `latest` tag. Partial state remains for reconciliation and requires
a fresh version.

Run the non-publishing local package dry run with:

```sh
KUBEATLAS_RELEASE_VERSION=1.2.3 make release-binaries-dry
```

See [Binary packages](packaging.md) for install/uninstall behavior, supported
architectures, no-autostart guarantees, and source/version provenance. GHCR
images/charts and GitHub Release binaries are separate authorization paths;
this change claims no published package or release.
