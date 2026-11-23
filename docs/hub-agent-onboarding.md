# Hub-hosted Agent onboarding

KubeAtlas Hub supplies the version-matched Agent installer, Helm chart, bootstrap script, and images. After [deploying Hub](deploy-with-ai.md), use **Clusters → Connect cluster** to generate the Agent installation command. Hub validates the local catalog at startup and serves the pinned files and image digests from it.

## Prepare a private bundle

Start with an already approved OCI image layout for the exact source revision and image digest. The preparation command packages the repository chart with the existing Helm executable and copies the image layout, installer, and same-origin bootstrap into a new, private output directory. It does not build, pull, tag, or push an image.

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
  --bootstrap scripts/bootstrap-hub-agent.sh \
  --version "$version" \
  --chart-version "$chart_version" \
  --revision "$revision" \
  --image-digest "$image_digest" \
  --output "$HOME/.local/share/kubeatlas/hub-bundles/$version"
```

Use a clean, committed checkout and build the Portal with the same build version (for example, `make portal VERSION="$version"`). The approved local OCI layout must already contain an image built from that exact source revision, with matching version/revision labels; the helper does not build, pull, tag, or publish it. `--chart-version` is Helm SemVer and defaults to `0.0.0-dev.<revision12>` when omitted; it is independent of the catalog/Agent build version. Helm packages the unchanged source chart with this SemVer and the build version as `appVersion`. A selected single Linux amd64 or arm64 image is valid; an index may contain either or both supported Linux architectures. The helper validates all reachable descriptors and blob hashes. Keep the bundle outside Git, treat it as immutable after preparation, and mount it read-only into the Portal. The artifact manifest must match the running Portal build version.

Complete [Hub DEB and RPM packages](packaging.md) include the matching catalog. Official Hub images with a built-in catalog use `/opt/kubeatlas/agent-catalog`. To supply a private catalog or configure one for an older custom Hub, use `--hub-artifacts-dir` or `KUBEATLAS_PORTAL_HUB_ARTIFACTS_DIR`. The Hub chart supports a private catalog on an existing read-only PVC through `hubArtifacts.enabled=true` and `hubArtifacts.existingClaim`.

To offer OpenCost on new clusters, prepare the catalog with `--opencost-chart`, `--opencost-layout`, and `--opencost-digest` together. Supply an operator-reviewed offline archive with JSON-compatible `opencost/values.yaml` and an embedded exporter chart, plus a local OCI image layout and its pinned image digest. Match the OpenCost layout to all supported Agent platforms. The chart and exporter are served by the Hub from the same origin as the Agent. Nodes already able to pull the Agent from that registry can pull the OpenCost exporter using the same runtime configuration.

Follow the [OpenCost offline preparation guide](opencost-offline.md) to build and verify the operator-managed inputs.

## Retain a previous catalog

For a Hub catalog change, pass `--hub-previous-artifacts-dir` and `--hub-previous-artifacts-version` to retain digest-addressed Agent and OpenCost pulls from the preceding verified catalog. In the Portal Helm chart, configure `hubArtifacts.previous.existingClaim` and `hubArtifacts.previous.version` to mount the previous catalog read-only. New enrollments use the current version.

The [DEB/RPM package upgrade](packaging.md#upgrade-and-migration) and the legacy systemd installer retain the previous catalog automatically. For a container deployment with Agents still using the current image digest, copy `/opt/kubeatlas/agent-catalog` from the existing Hub image to private storage and mount it read-only when deploying the next version.

The Hub exposes only versioned installer/chart/checksum/bootstrap files and the catalog-reachable image manifest and blobs. OCI content is anonymous, immutable, same-origin, and GET/HEAD-only. Tags, upload/delete routes, arbitrary file paths, external redirects, and registry credentials are not supported. Consequently the selected Hub origin must be reachable by the bootstrap client and by every target node that pulls the Agent image.

## Configure LAN and public addresses

In Organization settings, the platform owner can save a LAN origin and a public origin, and active members can read them. For a new Hub, set the initial public HTTPS address with Helm `hubOrigin`, the Hub flag `--hub-origin`, or `KUBEATLAS_PORTAL_HUB_ORIGIN` in the [packaged service configuration](packaging.md#configure-https).

A fresh deployment served over HTTP can use the explicit startup option `--http-ip-origin=http://192.0.2.10:1333` (or `KUBEATLAS_PORTAL_HTTP_IP_ORIGIN`) before a LAN profile is configured. The Portal Helm chart exposes this as `httpIPOrigin`. It accepts only a root HTTP origin with a literal IPv4 or bracketed IPv6 authority and enables non-Secure cookies only for that exact authority; it does not set a connection profile. `--insecure-cookie` remains loopback-only. Never use this option on an untrusted network or to enable an HTTP identity provider. Forwarded headers are ignored; an HTTPS profile for the same authority keeps cookies Secure.

The shared `局域网` / `公网` selector controls onboarding and MCP/OpenCode configuration; the browser origin is never a fallback. The browser stores only the selected profile name, not tokens or other credentials.

Origins must be HTTPS root origins, except loopback development and explicitly configured HTTP literal-IP origins. HTTP DNS names are rejected. HTTPS certificates must be trusted by the installer, Agent, and node runtimes. Userinfo, path prefixes, queries, fragments, wildcards, control characters, backslashes, and shell metacharacters are rejected. Both configured authorities are added to an immutable per-request exact-host policy snapshot; existing same-origin, Fetch Metadata, and trusted tunnel-origin checks remain in force.

Use a certificate whose SAN matches the configured DNS name or IP address. The Agent's optional CA file configures only its connection to the Hub. It does not configure the target node's container runtime; nodes must separately trust the Hub certificate to pull the image. HTTP exposes passwords, enrollment credentials, bearer tokens, and artifact downloads to interception. Use HTTP only on a trusted network, and configure each target node's container runtime separately if an HTTP registry requires an insecure-registry exception; the installer never changes node runtime settings. A TLS-terminating reverse proxy must preserve the selected Host authority and route `/api/`, `/agent`, `/mcp`, `/downloads/agent/`, and `/v2/` to the Portal without path rewriting or redirects; in particular, node image pulls must receive OCI manifest/blob bytes from this same origin.

## Bootstrap a new cluster

Run the generated bootstrap with Bash. Bootstrap requires `curl`, `kubectl`,
`helm`, `sha256sum`, `jq`, `python3`, `mktemp`, `chmod`, `grep`, `awk`, and
`sleep` on `PATH`. The installer also runs with Bash and requires `curl`,
`helm`, `kubectl`, `sha256sum`, `grep`, `awk`, `mktemp`, `stat`, `wc`, `od`,
`tr`, `cat`, `tail`, and `python3`. The installer uses `stat -c`, so it requires GNU
`stat`.

For a Hub using a private CA, export both variables in the shell before running
the generated command:

```bash
export KUBEATLAS_AGENT_HUB_CA_FILE=/absolute/path/to/hub-ca.pem
export CURL_CA_BUNDLE="$KUBEATLAS_AGENT_HUB_CA_FILE"
```

`CURL_CA_BUNDLE` supplies trust for the initial bootstrap download.
`KUBEATLAS_AGENT_HUB_CA_FILE` supplies the same PEM CA for enrollment, artifact
downloads and the Agent tunnel. Configure registry certificate trust on each
eligible node's container runtime separately.

An owner selects the LAN or public profile and approves the CLI's short-lived human code. The generated command downloads the same-origin bootstrap without following redirects, verifies its SHA-256 before execution, and starts enrollment. Bootstrap reads the current kube context and freezes it in a private mode-0600 kubeconfig; it displays the context, API server, and `kube-system` UID, checks tools, identity, required privileges, and resource collisions before cluster mutation, and never adopts or cleans up existing resources. The installer repeats preflight, downloads the pinned installer/chart/image from the same origin without redirects, verifies hashes, and passes the Agent token only by protected file path. Helm success means deployed, not Connected; verify the Agent's actual Connected status in the Portal.

Enrollment requests expire after about ten minutes. The device secret is never shown in the Portal, and the Agent token is returned only to the one-time claimant. Losing the claim response requires a new approved pairing; the Hub cannot recover the token. MCP continues to use organization-scoped bearer credentials.

With an OpenCost bundle configured, the installer checks existing Prometheus Services in the selected cluster. It verifies query access, then matches the exporter to the existing Prometheus Operator ServiceMonitor selectors or a detected annotation-based scraper. The installer stores the prepared offline chart in the Agent namespace. If discovery is inconclusive, Agent registration can proceed and the installer reports the reason. The user deploys OpenCost from the cost page after reviewing the target cluster and confirming installation.

The token is passed to `kubectl` only as a file path and is stored in a Secret mounted into the Agent as a file. It is never included in an argument value, URL, Helm release values, artifact, or generated log. Optional CA material is stored separately. The Agent chart requires the selected Hub origin, cluster ID, pinned image digest, token Secret, proxy authentication, and MCP; legacy `cloud.enabled` remains unavailable.

For an HTTP Hub, bootstrap checks registry trust before creating an enrollment,
and the installer repeats the check before Kubernetes writes. A blocking result
requires proof that the selected cluster has exactly one Docker node, its boot
identity matches this Linux host, and the explicitly selected local Unix Docker
daemon has the node's name and kernel. Docker client host/context overrides are
not used. The exact registry authority must be explicitly insecure or its IP
must fall in a configured insecure-registry CIDR. If the client is remote,
the cluster has multiple nodes, or the runtime/identity/policy is unavailable,
the scripts explain the node-side prerequisite without treating client Docker
settings as evidence for those nodes. This is a read-only preflight, not an image
pull or an automatic runtime change.

On a verified local Docker node with missing HTTP trust, ask its administrator to
preserve the existing daemon settings and allow only the displayed authority.
Validate the daemon's actual configuration before reloading it. Docker 24
supports a SIGHUP reload of insecure registries; for a systemd `docker.service`,
`sudo systemctl kill --kill-who=main --signal=HUP docker.service` sends that
reload signal without restarting the service. Verify the resulting registry
policy with `docker info`, then run the platform-generated command again.
The scripts only print this guidance; they do not edit daemon settings or send
signals. HTTPS still requires each node to trust the registry certificate.

If a claim response is lost, check the Hub's cluster directory before starting
another registration. The one-time claim may already have created a cluster
record. Bootstrap exits and removes its temporary credential files.

If installation fails after creating resources, keep the context, `kube-system`
UID, namespace and Helm release from the recovery output. Verify that identity
with your original kubeconfig and run the printed read-only commands. After a
failed Helm rollout, the installer reports Agent Pod status and recent warning
reasons when the Kubernetes API permits those reads. For an image pull failure,
check the reported node's registry trust, connectivity and architecture.
Repair the existing release after correcting the cause. If a new pairing is
necessary, reconcile the retained installation before approving it.

Hub mode binds `cluster-admin` only to the exact local Hub owner `User` identity returned by the Hub. It also binds the organization groups `kubeatlas:hub:owner`, `kubeatlas:hub:member`, and `kubeatlas:hub:viewer` to Kubernetes `admin`, `edit`, and `view`, respectively, plus a limited infrastructure-read role for get/list/watch on nodes, namespaces, storage classes, PVs, and PVCs—not Secrets or `nodes/proxy`. Those groups do not receive `cluster-admin`. Existing Agent deployments require an operator-controlled chart/Helm rollout to apply the new role bindings; the Hub does not change remote RBAC or Agent token Secrets. The Agent ServiceAccount also has Kubernetes impersonation permissions; possession of its token is highly privileged because Kubernetes cannot scope impersonation to a safe username/group set. Review the chart's RBAC and node image-pull trust before installation.

Helm success means only that resources were deployed. Verify that the cluster reports Connected after the Agent authenticates. Rotation is available only while a cluster's token is active and invalidates the previous token; update the Secret file and restart the Agent. Organization transfers preserve active Agent tokens: the existing Agent reconnects automatically without installation, approval or pairing. The original organization's access and affected MCP credentials are revoked. Already revoked clusters stay revoked after transfer and require a fresh approved pairing to restore connectivity. Losing a claim response also requires a fresh pairing because the token cannot be recovered. There is no automatic Agent upgrade or Hub-side cluster, Secret, or RBAC mutation.
