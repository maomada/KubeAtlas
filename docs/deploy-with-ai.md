# Deploy KubeAtlas with an AI agent

Share this [raw Markdown runbook](https://raw.githubusercontent.com/maomada/KubeAtlas/main/docs/deploy-with-ai.md) with your AI agent and tell it which Linux host and Kubernetes context to use. Choose **standalone** for a local console with an existing kubeconfig, or **Hub + Agent** for shared access to one or more clusters.

> Deploy KubeAtlas using this runbook. Target host: `<host>`. Mode: `<standalone or Hub + Agent>`. For Hub, use HTTPS at `<hostname>` and enroll `<Kubernetes context>`. Tell me the version, URL, and what you verified. Let me enter credentials and approve the pairing in the Hub UI.

## 1. Inspect the target and install the package

Identify the distribution, host architecture, and any existing KubeAtlas service. For cluster work, inspect the intended kubeconfig and current context with `kubectl config current-context`. On an upgrade, first identify the current version, state file, service configuration, and catalog. Take a private backup before replacing them. [Hub state and recovery](hub-state.md)

Choose a stable tag from [GitHub Releases](https://github.com/maomada/KubeAtlas/releases/latest), then use that version throughout. For a shared Hub, check whether the release supplies the Hub installer and Agent bundle before installing the package. Otherwise, run this in Bash on the Linux host. Change `component` to `kubeatlas-portal` for a manual Hub deployment and `format` to `rpm` for an RPM-based system.

```bash
set -euo pipefail
version=0.1.2
component=kubeatlas
format=deb

case "$(uname -m)" in
  x86_64) arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) printf 'Unsupported architecture\n' >&2; exit 1 ;;
esac

umask 077
downloads="$(mktemp -d)"
cd "$downloads"
asset="${component}_${version}_linux_${arch}.${format}"
release="https://github.com/maomada/KubeAtlas/releases/download/v$version"
for file in "$asset" checksums.txt; do
  curl --fail --silent --show-error --location --proto '=https' \
    --output "$file" "$release/$file"
done
awk -v name="$asset" '$2 == name' checksums.txt | sha256sum --strict --check -

case "$format" in
  deb) sudo apt install "./$asset" ;;
  rpm) sudo rpm -U "./$asset" ;;
esac
"$component" --version
```

Check that the reported version matches the selected release. The packages install the binaries under `/usr/bin`. [Package contents and distribution channels](packaging.md)

## 2. Run the standalone console

Use the intended kubeconfig and run as the user who owns it. If the shell already points to the right kubeconfig, the following starts the console on loopback.

```bash
kubectl config current-context
kubeatlas --listen-address 127.0.0.1 --port 9280 --no-browser
```

Open `http://127.0.0.1:9280` on that host. For a remote host, run `ssh -N -L 127.0.0.1:9280:127.0.0.1:9280 user@host` from the workstation and open the same URL there. Confirm the console shows the intended cluster and that you can list a namespace or workload permitted by your Kubernetes credentials.

## 3. Run a shared Hub

### Install from the release bundle

For releases that provide the Hub deployment assets, download the matching `kubeatlas-portal` DEB or RPM, `kubeatlas-hub-agent-bundle_<version>.tar.gz`, `install-hub.py`, and `hub-checksums.txt` into one directory. The bundle contains the Agent images for Linux amd64 and arm64, Helm Chart, and enrollment scripts. The Hub host needs Python 3, systemd, and its native package tools. It can install from these local files.

Configure a trusted HTTPS reverse proxy for the Hub root origin and the listener `127.0.0.1:9290` before running the installer. For a separate proxy host, pass a private listener with `--listen` and restrict it to the proxy. Follow the proxy routing requirements below. Put the local owner username and password into a root-owned JSON file with mode `0600` and the keys `username` and `password`. The password needs at least 16 bytes. Supply the file on the Hub host.

If the proxy uses a private certificate authority, pass its CA file to the installer with `--ca-file`. The registration host and target nodes need their own [CA trust configuration](hub-agent-onboarding.md#configure-lan-and-public-addresses).

```bash
# Use the version and package you downloaded.
version=0.1.2
package="kubeatlas-portal_${version}_linux_amd64.deb"
awk '$2 == "install-hub.py"' hub-checksums.txt | sha256sum --check - &&
  sudo python3 ./install-hub.py install --package "./$package" \
    --checksums ./hub-checksums.txt --catalog-archive "./kubeatlas-hub-agent-bundle_${version}.tar.gz" \
    --origin https://hub.example.com --credentials /root/kubeatlas-hub-credentials.json
```

The installer configures the service, validates the Agent catalog, and checks that Hub and its downloads are reachable. For an installation created by this installer, use its `upgrade` command with the next release's package, archive, and checksum file. It retains the managed login and listener, backs up the state, keeps the preceding catalog for existing image pulls, and rolls back a failed cutover. When an upgrade would replace an older retained catalog, first migrate the Agents that still use it, then pass `--retire-previous-catalog`. Use the manual steps below to maintain installations with their own service configuration. After a successful install, continue at [Connect a cluster](#5-connect-a-cluster).

### Manual Hub setup

Install `kubeatlas-portal` from step 1. For a fresh systemd deployment, create a dedicated service user and group named `kubeatlas`. Create `/etc/kubeatlas/portal.env` through the operator's secret-management process, owned by root with mode `0600`. Set `KUBEATLAS_PORTAL_USERNAME` and `KUBEATLAS_PORTAL_PASSWORD` in the file. The local owner password must be at least 16 bytes. Supply credentials directly on the host, outside the AI conversation, repository, and command line. To use the Hub's MCP endpoint behind a proxy, set `KUBEATLAS_PORTAL_MCP_ALLOWED_HOSTS` to the exact external authority, such as `hub.example.com`.

On the same host as the reverse proxy, save the following as `/etc/systemd/system/kubeatlas-portal.service`. For an existing Hub, keep its service account, state path, and login configuration. Point the existing unit at the newly installed binary.

```ini
[Unit]
Description=KubeAtlas Hub
After=network-online.target
Wants=network-online.target

[Service]
User=kubeatlas
Group=kubeatlas
EnvironmentFile=/etc/kubeatlas/portal.env
StateDirectory=kubeatlas-portal
StateDirectoryMode=0700
UMask=0077
ExecStart=/usr/bin/kubeatlas-portal --listen=127.0.0.1:9290 --hub-state-file=/var/lib/kubeatlas-portal/state.json
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

Run `sudo systemctl daemon-reload` and `sudo systemctl enable --now kubeatlas-portal` for a new installation. Use a trusted TLS reverse proxy for a dedicated root origin such as `https://hub.example.com`. If the proxy runs on a separate host, bind Hub to a private interface and limit access to its listener to that proxy. Forward the whole origin to the listener, preserve the external `Host`, allow the WebSocket upgrade at `/agent`, and preserve streaming responses. Serve `/api/`, `/mcp`, `/downloads/agent/`, and `/v2/` without path rewriting or redirects. Verify `systemctl is-active kubeatlas-portal` and sign in at the HTTPS URL. [Hub configuration](portal.md#deployment-and-security) · [Hub Helm chart](../deploy/helm/kubeatlas-portal/README.md)

## 4. Prepare Agent artifacts manually

The Hub uses a read-only Agent catalog for new enrollments. Its version must match the running Portal. The GitHub DEB/RPM release is separate from OCI publication. If you already have a verified OCI layout for the release, use it with the [catalog preparation helper](hub-agent-onboarding.md#prepare-a-private-bundle), passing the release version and exact source revision. Otherwise build the image from the selected tag on a staging host with internet access, Git, Docker Buildx with OCI export and multi-platform support, Go, Helm 3, Python 3, and jq.

```bash
set -euo pipefail
version=0.1.2  # Use the exact installed Portal version.
umask 077
stage="$(mktemp -d)"
git clone --depth 1 --branch "v$version" \
  https://github.com/maomada/KubeAtlas.git "$stage/source"
cd "$stage/source"
revision="$(git rev-parse HEAD)"

docker buildx build --target full --platform linux/amd64,linux/arm64 \
  --provenance=false --sbom=false \
  --build-arg "VERSION=$version" --build-arg "SOURCE_REVISION=$revision" \
  --output "type=oci,dest=$stage/kubeatlas.oci.tar" .
python3 scripts/validate_oci_archive.py validate --archive "$stage/kubeatlas.oci.tar"
mkdir "$stage/agent-oci"
tar -xf "$stage/kubeatlas.oci.tar" -C "$stage/agent-oci"
digest="$(jq -er '.manifests | select(length == 1) | .[0].digest' "$stage/agent-oci/index.json")"

go run ./cmd/prepare-hub-artifacts \
  --chart deploy/helm/kubeatlas --layout "$stage/agent-oci" \
  --installer scripts/install-hub-agent.sh --bootstrap scripts/bootstrap-hub-agent.sh \
  --version "$version" --chart-version "$version" --revision "$revision" \
  --image-digest "$digest" --output "$stage/catalog"
printf 'Catalog: %s\n' "$stage/catalog"
```

If the operator requested offline OpenCost support, prepare its chart and image and include them when assembling the catalog. Enrollment can detect a compatible existing Prometheus. OpenCost installation starts when a user confirms it on the Cost page. [Offline OpenCost preparation](opencost-offline.md)

Transfer the completed catalog to a new versioned directory on Hub, such as `/srv/kubeatlas/catalogs/0.1.2`. Make it readable by the service account and writable only by the operator. Set `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` in the service environment file and restart the Hub. For an upgrade, retain the preceding catalog and configure the [previous catalog options](hub-agent-onboarding.md#prepare-a-private-bundle) for nodes pulling its image digests.

## 5. Connect a cluster

1. In the Hub UI, the platform owner saves the chosen HTTPS root origin in **Hub connection addresses**. Confirm the registration host and every eligible Kubernetes node can reach the origin and trust its certificate.
2. In the correct organization, select **Clusters → Connect cluster** (`集群 → 连接集群`). On the registration host, verify the intended kubeconfig context and run the generated Bash command. The command displays the context and creates a short-lived approval code. The operator approves the matching code in Hub.
3. Verify the cluster is **Connected**. Read nodes or namespaces through Hub and record the Hub version, Agent version, and cluster ID. If Helm times out, inspect Pod status and warning events in the generated Agent namespace before retrying. Check node-side certificate trust and registry reachability for image pull failures. [Registration prerequisites and recovery](hub-agent-onboarding.md#bootstrap-a-new-cluster)
