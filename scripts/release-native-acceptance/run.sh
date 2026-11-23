#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

if [[ $# != 5 ]]; then
  printf 'Usage: %s PACKAGE_DIR HUB_BUNDLE VERSION SOURCE_SHA REPORT_DIR\n' "$0" >&2
  exit 2
fi
[[ "$(uname -s):$(uname -m)" = Linux:aarch64 ]]
[[ "$(docker info --format '{{.CgroupVersion}}')" = 2 ]]
packages="$(realpath -e "$1")"
bundle="$(realpath -e "$2")"
version="$3"
revision="$4"
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ && "$revision" =~ ^[0-9a-f]{40}$ ]]
mkdir -p "$5"
reports="$(realpath -e "$5")"
fixtures="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)"
work="$(mktemp -d)"
name="ka-native-$(basename "$work" | cut -d. -f2 | tr '[:upper:]' '[:lower:]')"
[[ "$name" =~ ^ka-native-[a-z0-9]+$ ]]
label="io.kubeatlas.release-fixture"
container=''
proxy_pid=''
kind_started=no
network_created=no

stop_hub() {
  if [[ -n "$proxy_pid" ]]; then
    kill "$proxy_pid" 2>/dev/null || true
    wait "$proxy_pid" 2>/dev/null || true
    proxy_pid=''
  fi
  if [[ -n "$container" ]] &&
     [[ "$(docker inspect "$container" --format '{{index .Config.Labels "io.kubeatlas.release-fixture"}}' 2>/dev/null)" = "$name" ]]; then
    if [[ "$1" != 0 ]]; then
      docker exec "$container" journalctl -u kubeatlas-portal --no-pager -n 15 >&2 || true
    fi
    docker rm -f -v "$container" >/dev/null || true
  fi
  container=''
}

cleanup() {
  status=$?
  set +e
  stop_hub "$status"
  if [[ "$kind_started" = yes ]]; then
    kind delete cluster --name "$name" --kubeconfig "$work/kubeconfig"
  fi
  if [[ "$network_created" = yes ]]; then
    docker network rm "$name" >/dev/null
  fi
  for method in deb rpm; do
    docker image rm "$name-$method:systemd" >/dev/null 2>&1 || true
  done
  rm -rf -- "$work"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

cd "$work"
mkdir bin
export PATH="$work/bin:$PATH"
export KIND_EXPERIMENTAL_PROVIDER=docker
export KIND_EXPERIMENTAL_DOCKER_NETWORK="$name"

download_tool() {
  curl --proto '=https' --proto-redir '=https' --retry 3 --fail --silent --show-error \
    --location --max-time 120 --output "bin/$1" "$2"
  printf '%s  bin/%s\n' "$3" "$1" | sha256sum --check --status
  chmod 0700 "bin/$1"
}

download_tool kind https://github.com/kubernetes-sigs/kind/releases/download/v0.30.0/kind-linux-arm64 \
  7ea2de9d2d190022ed4a8a4e3ac0636c8a455e460b9a13ccf19f15d07f4f00eb
download_tool kubectl https://dl.k8s.io/release/v1.34.0/bin/linux/arm64/kubectl \
  00b182d103a8a73da7a4d11e7526d0543dcf352f06cc63a1fde25ce9243f49a0
CGO_ENABLED=0 go build -o "$work/bin/tls-forward" "$fixtures/tls-forward.go"
for method in deb rpm; do
  docker build --label "$label=$name" --tag "$name-$method:systemd" \
    --file "$fixtures/Dockerfile.$method" "$fixtures"
  [[ "$(docker image inspect "$name-$method:systemd" --format '{{.Architecture}}')" = arm64 ]]
done

docker network create --label "$label=$name" "$name" >/dev/null
network_created=yes
gateway="$(docker network inspect "$name" --format '{{(index .IPAM.Config 0).Gateway}}')"
[[ "$gateway" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
cat > kind.yaml <<'EOF'
kind: Cluster
apiVersion: kind.x-k8s.io/v1alpha4
networking:
  apiServerAddress: 127.0.0.1
containerdConfigPatches:
  - |-
    [plugins."io.containerd.grpc.v1.cri".registry]
      config_path = "/etc/containerd/certs.d"
nodes:
  - role: control-plane
EOF
kind_started=yes
kind create cluster --name "$name" --config kind.yaml --kubeconfig "$work/kubeconfig" --wait 180s \
  --image kindest/node:v1.34.0@sha256:7416a61b42b1662ca6ca89f02028ac133a309a2a30ba309614e8ec94d976dc5a
context="kind-$name"
node="$name-control-plane"
kube=(kubectl --kubeconfig "$work/kubeconfig" --context "$context")
test "$("${kube[@]}" config current-context)" = "$context"
uid="$("${kube[@]}" get namespace kube-system -o 'jsonpath={.metadata.uid}')"
test -n "$uid"
test "$(docker inspect "$node" --format '{{index .Config.Labels "io.x-k8s.kind.cluster"}}')" = "$name"
registry_config="config_path = [\"']/etc/containerd/certs.d[\"']"
if ! docker exec "$node" containerd config dump | grep -E "$registry_config" >/dev/null; then
  docker exec "$node" sh -ceu '
    config=/etc/containerd/config.toml
    grep -Eq "^version = 2$" "$config"
    if grep -q "config_path" "$config"; then exit 1; fi
    printf "\n[plugins.\"io.containerd.grpc.v1.cri\".registry]\n  config_path = \"/etc/containerd/certs.d\"\n" >> "$config"
  '
  docker exec "$node" systemctl restart containerd
fi
docker exec "$node" containerd config dump | grep -E "$registry_config" >/dev/null

openssl req -x509 -newkey rsa:2048 -nodes -days 1 -sha256 \
  -keyout ca.key -out ca.crt -subj '/CN=KubeAtlas package acceptance CA' \
  -addext 'keyUsage=critical,keyCertSign,cRLSign' >/dev/null 2>&1
openssl req -newkey rsa:2048 -nodes -sha256 -keyout tls.key -out tls.csr \
  -subj "/CN=$gateway" >/dev/null 2>&1
printf 'subjectAltName=IP:%s\nextendedKeyUsage=serverAuth\nbasicConstraints=critical,CA:FALSE\n' "$gateway" > extensions
openssl x509 -req -in tls.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out tls.crt -days 1 -sha256 -extfile extensions >/dev/null 2>&1
openssl verify -CAfile ca.crt tls.crt >/dev/null
image_digest="$(tar -xOzf "$bundle" hub-agent-bundle/manifest.json |
  jq -er --arg version "$version" --arg revision "$revision" \
    'select(.version == $version and .sourceRevision == $revision) | .image.digest')"
[[ "$image_digest" =~ ^sha256:[0-9a-f]{64}$ ]]

for method in deb rpm; do
  case "$method" in deb) port=22431 ;; rpm) port=22432 ;; esac
  authority="$gateway:$port"
  origin="https://$authority"
  trust="/etc/containerd/certs.d/$authority"
  docker exec "$node" install -d -m 0755 "$trust"
  docker cp ca.crt "$node:$trust/ca.crt"
  printf 'server = "%s"\n[host."%s"]\n  capabilities = ["pull", "resolve"]\n  ca = "%s/ca.crt"\n' \
    "$origin" "$origin" "$trust" | docker exec -i "$node" sh -c 'cat > "$1/hosts.toml"' sh "$trust"

  container="$name-$method"
  package="kubeatlas-portal_${version}_linux_arm64.$method"
  test -f "$packages/$package"
  docker run -d --name "$container" --hostname "$container" --label "$label=$name" \
    --privileged --cgroupns private --network "$name" --tmpfs /run --tmpfs /run/lock \
    --mount "type=bind,src=$packages/$package,dst=/qa-package.$method,readonly" \
    --entrypoint /bin/sh "$name-$method:systemd" \
    -ceu 'if ! findmnt -n -o OPTIONS /sys/fs/cgroup | grep -qw rw; then mount -o remount,rw /sys/fs/cgroup; fi; exec /sbin/init' \
    >/dev/null
  for _ in $(seq 1 30); do
    if docker exec "$container" systemctl show -p Version --value >/dev/null 2>&1; then break; fi
    sleep 1
  done
  test "$(docker exec "$container" uname -m)" = aarch64
  test "$(docker exec "$container" cat /proc/1/comm)" = systemd
  docker exec "$container" sh -ceu '
    test ! -e /var/lib/kubeatlas-portal/state.json
    test ! -e /usr/bin/kubeatlas-portal
    ! getent passwd kubeatlas-portal
  '
  if [[ "$method" = deb ]]; then
    docker exec "$container" unshare --net apt install -y "/qa-package.$method"
    docker exec "$container" dpkg-query -W kubeatlas-portal
  else
    docker exec "$container" unshare --net dnf --disablerepo='*' install -y "/qa-package.$method"
    docker exec "$container" rpm -q kubeatlas-portal
  fi
  docker exec "$container" systemctl is-active kubeatlas-portal
  docker exec "$container" systemctl is-enabled kubeatlas-portal
  test "$(docker exec "$container" /usr/bin/kubeatlas-portal --version)" = "kubeatlas-portal $version"
  test "$(docker exec "$container" systemctl show kubeatlas-portal -p User --value)" = kubeatlas-portal
  docker exec --user kubeatlas-portal "$container" /usr/bin/kubeatlas-portal \
    --check-hub-artifacts --hub-artifacts-dir=/usr/share/kubeatlas-portal/agent-catalog
  test "$(docker exec "$container" curl --fail --silent --retry 8 --retry-all-errors --max-time 5 \
    http://127.0.0.1:1333/api/setup)" = '{"required":true}'

  host="$(docker inspect "$container" --format "{{(index .NetworkSettings.Networks \"$name\").IPAddress}}")"
  [[ "$host" =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]
  printf 'KUBEATLAS_PORTAL_HUB_ORIGIN=%s\nKUBEATLAS_PORTAL_OPTIONS=--listen=%s:1333\n' "$origin" "$host" |
    docker exec -i "$container" sh -ceu 'umask 077; cat > /etc/kubeatlas-portal/hub.env'
  docker exec "$container" systemctl restart kubeatlas-portal
  tls-forward -listen "$authority" -upstream "http://$host:1333" -cert tls.crt -key tls.key &
  proxy_pid=$!
  curl --fail --silent --show-error --cacert ca.crt --retry 8 --retry-all-errors \
    --max-time 5 "$origin/healthz"
  kill -0 "$proxy_pid"
  python3 "$fixtures/accept.py" "$method" --kubeconfig "$work/kubeconfig" --context "$context" \
    --uid "$uid" --ca "$work/ca.crt" --origin "$origin" --container "$container" \
    --version "$version" --image-digest "$image_digest" --source-sha "$revision" \
    --package "$packages/$package" --architecture arm64 | tee "$reports/$method.json"
  stop_hub 0
done
