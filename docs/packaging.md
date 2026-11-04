# KubeAtlas binary packages

The binary distribution contains two independent components:

- `kubeatlas` — Linux and macOS archives for amd64 and arm64, plus a Windows
  amd64 archive. Linux amd64 and arm64 DEB and RPM packages install
  `/usr/bin/kubeatlas`.
- `kubeatlas-portal` — Linux archives and DEB/RPM packages for amd64 and arm64.
  Packages install `/usr/bin/kubeatlas-portal`.

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

For an archive, unpack it and install the requested binary manually:

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
GHCR images and charts. A future binary release requires the manually
dispatched `Release Binaries` workflow, successful main CI and CodeQL for the
selected source SHA, and approval of the protected `ghcr-publish` environment.
The environment is shared with the broader distribution controls; it does not
grant builds registry credentials. `publish=false` performs a no-write dry run.
