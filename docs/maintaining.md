# Maintaining KubeAtlas

## Triage and review

- Use Discussions for setup and usage questions. Move reproducible bugs and
  actionable feature requests into Issues so the work can be tracked.
- Review `needs-triage` issues for duplicates, an affected version or commit,
  and enough information to act. Add a relevant `area:` label and remove
  `needs-triage` once the issue has a clear next step. Use `good first issue`
  for changes with a small, well-defined scope.
- Direct vulnerability reports to the [security policy](../SECURITY.md).
  Enable GitHub private vulnerability reporting when the repository is public.
- Ask for a small PR with a verification summary. Review the diff and its CI
  results, then squash merge. Use the [engineering workflow](engineering-workflow.md)
  for changes that need detailed verification and handoff.
- When repository rulesets are available, require PRs and the `CI required gates`,
  `Security required gates`, and `Package required gates` checks on `main`, and
  block force pushes and branch deletion. Keep changes reviewable by the
  maintainers available to the project.

## Choose what to ship

| Distribution | Contents | How to build and publish |
| --- | --- | --- |
| GHCR | Versioned KubeAtlas (standalone or Agent) and Hub container images, plus the two OCI Helm charts | Run `Release GHCR` |
| GitHub Releases | Linux amd64 and arm64 DEB/RPM packages for both binaries, the standalone Windows amd64 ZIP, and the release manifest and checksums | Run `Release Binaries` when these install targets are ready |
| Hub enrollment bundle | Version-matched Agent bootstrap, installer, chart, and OCI image | Prepare for your Hub deployment using the [onboarding guide](hub-agent-onboarding.md) |

For the first prerelease, prioritize the GHCR images and charts. Check their
matching versions and exercise Hub startup and Agent enrollment in a clean test
environment. Treat optional offline integrations as separately validated
distribution inputs. When planning binary releases, let `Release Binaries`
create the version tag and GitHub Release, which must both be free when it starts.

## Cut a release

1. Select the full SHA of a committed revision on `main`. Bring intended
   deployment changes into `main` through review before selecting the source.
   Record the same-SHA main-push CI, security, and package results.
2. Choose an unused SemVer version without a `v` prefix or build metadata.
   Dispatch the relevant release workflow with `publish=false`. Inspect the
   exact artifact inventory, SHA-256 hashes, and installation and upgrade
   results for the targets you plan to support.
3. Sign the exact dry-run approval file with the maintainer's offline Ed25519 key,
   then publish using the signed approval. Follow the [release policy](ci-cd.md)
   for approval inputs and recovery from partial publication. Keep the private
   key under maintainer control. The binary workflow uploads all assets to a
   draft before publishing the release.
4. Verify the published registry digests and release asset checksums. Record the
   source SHA, matching Hub/Agent and chart versions, supported architectures,
   known limitations, and install and upgrade instructions in the release notes.
   Verify that an installation works with the public access you intend to offer.
   GHCR package visibility is managed separately from repository visibility.

## Before making the repository public

- Review Git history and remote refs, PR and issue attachments, and past
  Actions logs and artifacts for secrets and private deployment data. Keep local
  kubeconfigs, registry credentials, customer inventory, and deploy logs outside Git.
- Retain the [license](../LICENSE) and [upstream attribution](../NOTICE). Check
  third-party licensing and notices for anything included in public artifacts.
- Confirm repository visibility, release access, and package visibility with the
  repository owner. [GitHub's visibility guide](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/managing-repository-settings/setting-repository-visibility)
  describes which repository data becomes public. Refer to the
  [package access guide](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)
  before making a package public.
