# Deploy KubeAtlas with an AI agent

Share this [raw Markdown runbook](https://raw.githubusercontent.com/maomada/KubeAtlas/main/docs/deploy-with-ai.md) with your AI agent and tell it which Linux host and Kubernetes context to use. Choose **standalone** for a local console with an existing kubeconfig, or **Hub + Agent** for shared access to one or more clusters.

> Deploy KubeAtlas using this runbook. Target host: `<host>`. Mode: `<standalone or Hub + Agent>`. For Hub, use HTTPS at `<hostname>` and enroll `<Kubernetes context>`. Tell me the version, URL, and what you verified. Let me enter credentials and approve the pairing in the Hub UI.

## 1. Inspect the target and install the package

Identify the distribution, host architecture, and any existing KubeAtlas service. For cluster work, inspect the intended kubeconfig and current context with `kubectl config current-context`. On an upgrade, first identify the current version, state file, service configuration, and catalog. Take a private backup before replacing them. [Hub state and recovery](hub-state.md)

Choose a stable tag from [GitHub Releases](https://github.com/maomada/KubeAtlas/releases/latest), then use that version throughout. Run this in Bash on the Linux host. Change `component` to `kubeatlas-portal` for Hub and `format` to `rpm` for an RPM-based system.

```bash
set -euo pipefail
version=0.1.1
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

## 4. Prepare Agent artifacts and connect a cluster

The Hub uses a read-only Agent catalog for new enrollments. Its version must match the running Portal. The GitHub DEB/RPM release is separate from OCI publication. If you already have a verified OCI layout for the release, use it with the [catalog preparation helper](hub-agent-onboarding.md#prepare-a-private-bundle), passing the release version and exact source revision. Otherwise build the image from the selected tag on a staging host with internet access, Git, Docker Buildx with OCI export and multi-platform support, Go, Helm 3, Python 3, and jq.

```bash
set -euo pipefail
version=0.1.1  # Use the exact installed Portal version.
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

Transfer the completed catalog to a new versioned directory on Hub, such as `/srv/kubeatlas/catalogs/0.1.1`. Make it readable by the service account and writable only by the operator. Set `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR` in the service environment file and restart the Hub. For an upgrade, retain the preceding catalog and configure the [previous catalog options](hub-agent-onboarding.md#prepare-a-private-bundle) for nodes pulling its image digests.

1. In the Hub UI, the platform owner saves the chosen HTTPS root origin in **Hub connection addresses**. Confirm the registration host and every eligible Kubernetes node can reach the origin and trust its certificate.
2. In the correct organization, select **Clusters → Connect cluster** (`集群 → 连接集群`). On the registration host, verify the intended kubeconfig context and run the generated Bash command. The command displays the context and creates a short-lived approval code. The operator approves the matching code in Hub.
3. Verify the cluster is **Connected**. Read nodes or namespaces through Hub and record the Hub version, Agent version, and cluster ID. If Helm times out, inspect Pod status and warning events in the generated Agent namespace before retrying. Check node-side certificate trust and registry reachability for image pull failures. [Registration prerequisites and recovery](hub-agent-onboarding.md#bootstrap-a-new-cluster)
