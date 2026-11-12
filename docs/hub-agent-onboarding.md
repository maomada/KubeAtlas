# Hub-hosted Agent onboarding

The Portal can host one version-matched Agent installer, Helm chart, and OCI image bundle from a local artifact directory. It never downloads or publishes bundle content at request time. The optional bundle is validated before the Hub starts; a missing bundle leaves Hub and MCP available but disables onboarding, while a configured invalid bundle prevents startup.

## Prepare a private bundle

Start with an already approved OCI image layout for the exact source revision and image digest. The preparation command packages the repository chart with the existing Helm executable and copies the image layout and installer into a new, private output directory. It does not build, pull, tag, or push an image.

```bash
umask 077
install -d -m 700 "$HOME/.local/share/kubeatlas/hub-bundles"
revision="$(git rev-parse HEAD)"
version="$revision"
chart_version="0.0.0-dev.${revision:0:12}"
image_digest='sha256:<approved-image-or-index-digest>'
go run ./cmd/prepare-hub-artifacts \
  --chart deploy/helm/kubeatlas \
  --layout "$HOME/.local/share/kubeatlas/approved-agent.oci" \
  --installer scripts/install-hub-agent.sh \
  --version "$version" \
  --chart-version "$chart_version" \
  --revision "$revision" \
  --image-digest "$image_digest" \
  --output "$HOME/.local/share/kubeatlas/hub-bundles/$version"
```

Use a clean, committed checkout and build the Portal with the same build version (for example, `make portal VERSION="$version"`). The approved local OCI layout must already contain an image built from that exact source revision, with matching version/revision labels; the helper does not build, pull, tag, or publish it. `--chart-version` is Helm SemVer and defaults to `0.0.0-dev.<revision12>` when omitted; it is independent of the catalog/Agent build version. Helm packages the unchanged source chart with this SemVer and the build version as `appVersion`. A selected single Linux amd64 or arm64 image is valid; an index may contain either or both supported Linux architectures. The helper validates all reachable descriptors and blob hashes. Keep the bundle outside Git, treat it as immutable after preparation, and mount it read-only into the Portal. The artifact manifest must match the running Portal build version.

Configure the Portal with `--hub-artifacts-dir` or `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR`. The `kubeatlas-portal` chart supports an existing PVC through `hubArtifacts.enabled=true` and `hubArtifacts.existingClaim`; the mount is read-only. No artifacts are embedded in the Portal image.

The Hub exposes only versioned installer/chart/checksum files and the catalog-reachable image manifest and blobs. OCI content is anonymous, immutable, same-origin, and GET/HEAD-only. Tags, upload/delete routes, arbitrary file paths, external redirects, and registry credentials are not supported. Consequently the selected Hub origin must be reachable by the bootstrap client and by every target node that pulls the Agent image.

## Configure LAN and public addresses

In Organization settings, an owner or administrator can save a LAN origin and a public origin. Active members can read these settings. The shared `局域网` / `公网` selector controls onboarding and MCP/OpenCode configuration; the browser origin is never a fallback. The browser stores only the selected profile name, not tokens or other credentials.

Origins must be HTTPS root origins, with HTTP permitted only for loopback development. IP literals and DNS names are accepted; no DNS lookup or network probe is performed. Userinfo, path prefixes, queries, fragments, wildcards, control characters, backslashes, and shell metacharacters are rejected. Both configured authorities are added to an immutable per-request exact-host policy snapshot; existing same-origin, Fetch Metadata, and trusted tunnel-origin checks remain in force.

Use a certificate whose SAN matches the configured DNS name or IP address. The Agent's optional CA file configures only its connection to the Hub. It does not configure the target node's container runtime; nodes must separately trust the Hub certificate to pull the image. Do not expose the Hub over non-loopback HTTP. A TLS-terminating reverse proxy must preserve the selected Host authority and route `/api/`, `/agent`, `/mcp`, `/downloads/agent/`, and `/v2/` to the Portal without path rewriting or redirects; in particular, node image pulls must receive OCI manifest/blob bytes from this same origin.

## Bootstrap a new cluster

The `/install` page issues a per-cluster token and displays it once. Set an explicit `KUBE_CONTEXT` and run the generated checksum-verifying command; it first creates the private token file and prompts for the token without echoing it. The installer requires an exact matching kube context and preflights access, Hub chart/checksum/image manifest, chart render, namespace, release, and cluster-scoped resources. It refuses to adopt an existing namespace/release/resources and prompts before mutation unless `--yes` is passed. `--dry-run` performs no Kubernetes mutations.

Enrollment remains per-cluster, and MCP continues to use the existing scoped bearer credential flow. This onboarding does not add organization enrollment keys or OAuth.

The token is passed to `kubectl` only as a file path and is stored in a Secret mounted into the Agent as a file. It is never included in an argument value, URL, Helm release values, artifact, or generated log. Optional CA material is stored separately. The Agent chart requires the selected Hub origin, cluster ID, pinned image digest, token Secret, proxy authentication, and MCP; legacy `cloud.enabled` remains unavailable.

Hub mode creates a ClusterRoleBinding for `cluster-admin` to the exact local Hub owner `User` identity returned by the Hub. It does not bind groups or all Hub members. The Agent ServiceAccount also has Kubernetes impersonation permissions; possession of its token is highly privileged because Kubernetes cannot scope impersonation to a safe username/group set. Review the chart's RBAC and node image-pull trust before installation.

Helm success means only that resources were deployed. Verify that the cluster reports Connected after the Agent authenticates. Token rotation invalidates the previous token; update the Secret file and restart the Agent. There is no automatic Agent upgrade or Hub-side cluster mutation.
