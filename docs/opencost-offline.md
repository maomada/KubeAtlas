# Prepare OpenCost for offline installation

The KubeAtlas release supplies the Hub and Agent images and charts. To enable
one-click OpenCost installation, the Hub operator prepares and adds the
OpenCost chart and exporter image to the Hub's read-only Agent catalog.
Enrollment prepares a cluster-specific package. OpenCost deploys once an
authorized user confirms **Install OpenCost** on the cluster's Cost page.

The target cluster needs a working Prometheus Service that can scrape the
exporter through a compatible Prometheus Operator `ServiceMonitor` or an
existing annotation-based scrape configuration. The bootstrap host needs
`kubectl` access to the selected context and Python 3. Nodes pull the
exporter from the same origin as the Agent, so they need the same registry
access and certificate trust. [Hub Agent onboarding](hub-agent-onboarding.md)

## Stage the chart and exporter

Run these commands in the source checkout for the selected KubeAtlas release
on a staging host with internet access, Python 3 with PyYAML, Skopeo, and jq.
The packer accepts upstream chart `2.5.34`. Use the exporter `1.121.4` digest
listed in its values. The public image includes Linux amd64 and arm64. A new
chart and image pair requires updating the packer's pinned inputs and testing
the resulting package.
[OpenCost chart 2.5.34](https://github.com/opencost/opencost-helm-chart/releases/tag/opencost-2.5.34)

```bash
set -euo pipefail
umask 077
stage="$(mktemp -d)"
chart_version=2.5.34
chart_sha256=9ba42bb2ae4df6854d51e1d08fa45f14aed49781b8e597e00c0d789c271aacee
exporter_source_digest=sha256:daa04ae48f17b830de941c3602563c5a01fabb13522ede1ba057f09e96613aaa

curl --fail --silent --show-error --location --proto '=https' \
  --output "$stage/upstream-opencost.tgz" \
  "https://github.com/opencost/opencost-helm-chart/releases/download/opencost-$chart_version/opencost-$chart_version.tgz"
printf '%s  %s\n' "$chart_sha256" "$stage/upstream-opencost.tgz" | sha256sum --check

python3 scripts/prepare-opencost-base.py \
  --input-chart "$stage/upstream-opencost.tgz" \
  --output-chart "$stage/opencost.tgz"

skopeo copy --all --format oci \
  "docker://ghcr.io/opencost/opencost@$exporter_source_digest" \
  "oci:$stage/opencost-oci:exporter"
exporter_digest="$(jq -er '.manifests[] | select(.annotations["org.opencontainers.image.ref.name"] == "exporter") | .digest' \
  "$stage/opencost-oci/index.json")"
(cd "$stage" && sha256sum opencost.tgz > SHA256SUMS)
printf 'Upstream exporter digest %s\nLocal exporter digest %s\nStaging directory %s\n' \
  "$exporter_source_digest" "$exporter_digest" "$stage"
```

The base packer runs locally and normalizes the pinned chart for exporter-only
installation. The catalog needs the digest of the descriptor in the local OCI
layout. The descriptor can differ from the upstream digest when Skopeo
converts the manifest list to an OCI index. Record both digests and the
prepared chart's checksum with the release evidence.
[Skopeo copy](https://github.com/containers/skopeo/blob/main/docs/skopeo-copy.1.md)

## Add the assets to the Hub catalog

Transfer `opencost.tgz`, `SHA256SUMS`, the entire `opencost-oci` directory, and
the recorded digests to the host that prepares the catalog. Use the product
release's exact source checkout, version, and verified Agent OCI layout and
digest. The Actions dry-run KubeAtlas image archive contains that layout. Once
the image is public on GHCR, use `skopeo copy --all --preserve-digests` to copy
it by its recorded digest into a local OCI layout.
[KubeAtlas release process](ci-cd.md)

Set `KUBEATLAS_VERSION` to the Hub and Agent build version,
`KUBEATLAS_SOURCE_SHA` to the full release SHA, `KUBEATLAS_AGENT_LAYOUT` to the
verified Agent layout directory, and `KUBEATLAS_AGENT_DIGEST` to its selected
image digest. Set `stage` and `exporter_digest` on the destination host from the
previous step. Use a new output directory for the catalog.

```bash
set -euo pipefail
test "$(git rev-parse HEAD)" = "$KUBEATLAS_SOURCE_SHA"
(cd "$stage" && sha256sum --check SHA256SUMS)
install -d -m 700 "$HOME/.local/share/kubeatlas/hub-bundles"
go run ./cmd/prepare-hub-artifacts \
  --chart deploy/helm/kubeatlas \
  --layout "$KUBEATLAS_AGENT_LAYOUT" \
  --installer scripts/install-hub-agent.sh \
  --bootstrap scripts/bootstrap-hub-agent.sh \
  --version "$KUBEATLAS_VERSION" \
  --chart-version "$KUBEATLAS_VERSION" \
  --revision "$KUBEATLAS_SOURCE_SHA" \
  --image-digest "$KUBEATLAS_AGENT_DIGEST" \
  --opencost-chart "$stage/opencost.tgz" \
  --opencost-layout "$stage/opencost-oci" \
  --opencost-digest "$exporter_digest" \
  --output "$HOME/.local/share/kubeatlas/hub-bundles/$KUBEATLAS_VERSION"
```

Mount the completed catalog read-only into the matching Hub version. The Hub
serves the base chart from `/downloads/agent/<version>/opencost.tgz` and the
exporter by digest from `/v2/kubeatlas/opencost/`. The catalog validates the
chart and serves only verified image content. The exporter layout must cover
every Linux architecture present in the Agent layout.
[Hub catalog configuration](hub-agent-onboarding.md#prepare-a-private-bundle)

The signed KubeAtlas release manifest covers the product build. The
operator-supplied chart, exporter, and assembled catalog keep their own
provenance and checksum records.

## Check preparation and installation

The Agent installer automatically runs the Python 3 helper embedded in its
verified Helm chart. The helper uses the registration host's kubeconfig to
query Prometheus via `kubectl` and produces the chart for the selected cluster.
It selects an existing Prometheus query endpoint and configures the matching
scrape method. For troubleshooting, use the helper from the same KubeAtlas
checkout with the exact inputs the installer uses. Set `CONTEXT` to the target
context and `HUB_AUTHORITY` to the chosen Hub host with an explicit port if it
has one, such as `hub.example.com` or `hub.example.com:8443`.

```bash
python3 deploy/helm/kubeatlas/files/prepare-opencost.py \
  --context "$CONTEXT" \
  --base "$stage/opencost.tgz" \
  --output "$stage/opencost-test.tgz" \
  --registry "$HUB_AUTHORITY" \
  --image-digest "$exporter_digest"
```

The helper reports a JSON `ready` value and `reason`. A missing, unreachable,
or ambiguous Prometheus setup leaves the OpenCost package unavailable while
allowing Agent enrollment to continue. Correct the Prometheus setup and
prepare the Agent package again before installing OpenCost.

On a test cluster, confirm that the Agent registers and the cost page offers
installation while the `opencost` release is still absent. After you confirm
installation, verify that the exporter image pulls, the Prometheus target is
healthy, and the Cost page shows data after Prometheus has collected samples.
