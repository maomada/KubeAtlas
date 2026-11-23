#!/usr/bin/env bash
set -Eeuo pipefail

if [[ $# != 4 ]]; then
  echo "Usage: $0 DIST VERSION SOURCE_SHA OCI_VALIDATOR" >&2
  exit 2
fi

dist="$(realpath "$1")"
version="$2"
revision="$3"
oci_validator="$4"
if [[ ! "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ || ! "$revision" =~ ^[0-9a-f]{40}$ ]]; then
  echo "Expected a release version and a full source revision" >&2
  exit 2
fi
case "$(uname -s):$(uname -m)" in
  Linux:x86_64) host_arch=amd64 ;;
  Linux:aarch64) host_arch=arm64 ;;
  *) echo "A native Linux amd64 or arm64 package builder is required" >&2; exit 1 ;;
esac
for tool in docker helm jq nfpm; do
  command -v "$tool" >/dev/null || { echo "Required tool missing: $tool" >&2; exit 1; }
done
for script in scripts/{bootstrap,install}-hub-agent.sh deploy/linux/kubeatlas-portal/*.sh; do
  if LC_ALL=C grep -q $'\r' "$script"; then
    printf 'Package script must use LF line endings: %s\n' "$script" >&2
    exit 1
  fi
done

stage="$(mktemp -d)"
trap 'chmod -R u+w "$stage"; rm -rf -- "$stage"' EXIT
mkdir -p "$stage/context" "$stage/layout" "$stage/portal" "$dist/hub"
cp Dockerfile "$stage/context/"

for arch in amd64 arm64; do
  agent="$stage/context/kubeatlas-$arch"
  portal="$stage/portal/kubeatlas-portal-$arch"
  tar --extract --to-stdout --file "$dist/kubeatlas_v${version}_linux_${arch}.tar.gz" -- kubeatlas > "$agent"
  tar --extract --to-stdout --file "$dist/kubeatlas-portal_v${version}_linux_${arch}.tar.gz" -- kubeatlas-portal > "$portal"
  test -s "$agent"
  test -s "$portal"
  chmod 0755 "$agent" "$portal"
done

docker buildx build --target release --platform linux/amd64,linux/arm64 \
  --provenance=false --sbom=false \
  --build-arg "VERSION=$version" --build-arg "SOURCE_REVISION=$revision" \
  --output "type=oci,dest=$stage/agent.oci.tar" "$stage/context"
python3 "$oci_validator" validate --archive "$stage/agent.oci.tar"
tar -xf "$stage/agent.oci.tar" -C "$stage/layout"
digest="$(jq -er '.manifests | select(length == 1) | .[0].digest' "$stage/layout/index.json")"
archive="$dist/hub/kubeatlas-hub-agent-bundle_${version}.tar.gz"
go run ./cmd/prepare-hub-artifacts --layout "$stage/layout" --image-digest "$digest" \
  --version "$version" --chart-version "$version" --revision "$revision" \
  --output "$stage/catalog" --archive "$archive"

archive_hash="$(sha256sum "$archive" | cut -d ' ' -f 1)"
portal="$stage/portal/kubeatlas-portal-$host_arch"
"$portal" --import-hub-artifacts "$archive" --hub-artifacts-sha256 "$archive_hash" \
  --hub-artifacts-dir "$stage/imported"
"$portal" --check-hub-artifacts --hub-artifacts-dir "$stage/imported"

for arch in amd64 arm64; do
  for format in deb rpm; do
    name="kubeatlas-portal_${version}_linux_${arch}.$format"
    KUBEATLAS_HUB_BINARY="$stage/portal/kubeatlas-portal-$arch" \
    KUBEATLAS_HUB_CATALOG="$stage/imported" \
    KUBEATLAS_RELEASE_VERSION="$version" KUBEATLAS_HUB_ARCH="$arch" \
      nfpm package --config deploy/linux/kubeatlas-portal.nfpm.yaml \
        --packager "$format" --target "$stage/$name"
    mv "$stage/$name" "$dist/$name"
  done
done
