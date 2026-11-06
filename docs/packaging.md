# KubeAtlas binary packages

The full GoReleaser build validates 15 artifacts for two independent
components:

- `kubeatlas` — Linux and macOS archives for amd64 and arm64, a Windows amd64
  archive, and Linux amd64/arm64 DEB and RPM packages.
- `kubeatlas-portal` — Linux archives and Linux amd64/arm64 DEB and RPM
  packages.

The hosted GitHub Release publication profile, when authorized, contains only
eight Linux DEB/RPM packages (four for each component) and the KubeAtlas
Windows amd64 ZIP. This hosted `local` profile does not publish Linux or macOS
tar archives, macOS packages, or a Portal Windows binary. The full
15-artifact build remains validation and diagnostic evidence.

Packages also install `LICENSE`, `NOTICE`, `README.md`, and this document
under `/usr/share/doc/<component>`. Installation does not create users,
configure kubeconfig files, install services, or start processes. Uninstalling
removes only files owned by the package. Packages never auto-start a service.

## Install and uninstall

Install local DEB packages:

```sh
sudo apt install ./kubeatlas_VERSION_linux_amd64.deb
sudo apt install ./kubeatlas-portal_VERSION_linux_amd64.deb
```

Install local RPM packages:

```sh
sudo rpm -i ./kubeatlas_VERSION_linux_amd64.rpm
sudo rpm -i ./kubeatlas-portal_VERSION_linux_amd64.rpm
```

For a locally built archive that is not part of the GitHub Release selection,
unpack it and install the requested binary manually:

```sh
tar -xzf kubeatlas_vVERSION_linux_amd64.tar.gz
sudo install -m 0755 kubeatlas /usr/local/bin/kubeatlas

tar -xzf kubeatlas-portal_vVERSION_linux_amd64.tar.gz
sudo install -m 0755 kubeatlas-portal /usr/local/bin/kubeatlas-portal
```

Use the matching architecture and version in each filename. Package removal:

```sh
sudo apt remove kubeatlas kubeatlas-portal
# or
sudo rpm -e kubeatlas kubeatlas-portal
```

For manually installed archives, remove only the copied binary when uninstalling.
Run binaries directly; no service is installed or started. `kubeatlas --version` and
`kubeatlas-portal --version` report the exact selected release version. Every
release includes `binary-manifest.json`, which binds each filename and SHA-256
checksum to its component, platform, architecture, version, source commit, and
build evidence URLs.

GitHub Release binaries are a separate distribution channel from KubeAtlas
GHCR images and charts. The manually dispatched `Release Binaries` workflow
requires successful main CI and Semgrep CE for the selected source SHA, a
successful dry run including native Windows ZIP smoke, and a valid offline
Ed25519 signature over the exact `release-approval.json` bytes. The approval
binds the workflow revision, source/version, dry-run run, selected targets, and
artifact hashes and expires within seven days. The workflow revalidates policy,
artifacts, and tag/release collisions before writes. The `ghcr-publish`
environment remains defense in depth; its name alone does not enforce review.

Only the trusted workflow checkout's `.github/release-signing-public.pem` is
used for verification. The private key stays outside GitHub and this
repository. Until the authorized production public key is provisioned, the
workflow blocks approval creation and publication. No release or package is
published by the implementation change.

### Local build and acceptance bundle

The separate local path is documented in [CI/CD](ci-cd.md#separate-local-release-path).
For the scoped 0.1.0 local release, the fixed `local-linux-amd64` profile builds
and validates the full binary matrix once as diagnostic evidence, then selects
only four Linux amd64 DEB/RPM packages (KubeAtlas and Portal),
`binary-manifest.json`, and `checksums.txt` (six binary-publication files).
The local bundle also contains both linux/amd64-only images and both Helm
charts staged with Linux amd64 node selectors. The Windows ZIP and all arm64
distributions are deferred, unpublished, and unaccepted in this profile; no
native acceptance is claimed for them. Hosted multiarch defaults are unchanged.

During staging, each generated unsigned RPM is copied before correcting its
`LONGARCHIVESIZE` field only when it matches installed `SIZE` but differs from
the bounded measured gzip/CPIO payload size. This handles the known rpmpack
metadata issue ([upstream issue #60](https://github.com/google/rpmpack/issues/60));
the raw GoReleaser `dist` RPMs remain unchanged. RPM digests are checked before
and after correction, and the corrected copy must still pass native
`rpm2cpio`/`cpio`, metadata, file-list, and ELF validation. This is content
validation only: native package install, version, uninstall, and no-autostart
acceptance remain required.

This is separate from the unchanged hosted Actions `local` profile, which
retains the eight Linux packages plus the KubeAtlas Windows ZIP (nine assets,
11 files with manifest and checksums) and still requires native Windows smoke.
That hosted profile is not authorized for the scoped 0.1.0 local publication.
Local approval and evidence use a local-only schema and cannot substitute for
Actions approval or run provenance. Candidate builds are not releasable;
offline approval requires a fresh clean main source/tooling commit. The
remaining real-cluster gate and all four isolated native amd64 package install, version,
uninstall, and no-autostart gates remain operator-attested requirements. They
must not be inferred from cross-builds, emulation, fixtures, or package inspection. No
actual release publication is claimed.
