#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
fail() { printf 'hub agent bootstrap: %s\n' "$*" >&2; exit 1; }

# BEGIN HTTP_REGISTRY_PREFLIGHT
http_registry_preflight() {
  [[ "$1" == http://* ]] || return 0
  local boot_id=''
  boot_id="$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || true)"
  python3 - "$1" "$2" "$boot_id" <<'HTTP_REGISTRY_PY'
import ipaddress
import json
import os
import subprocess
import sys
import uuid
from urllib.parse import urlsplit

origin, context, boot_id = sys.argv[1:]
if not origin.startswith("http://"):
    sys.exit(0)
authority = urlsplit(origin).netloc
host = urlsplit(origin).hostname

def guidance():
    print("Have the node administrator preserve the existing Docker configuration and add only "
          + json.dumps(authority) + " to insecure-registries. Validate the daemon's actual configuration, "
          "then use its supported SIGHUP reload (Docker 24 supports this) and verify docker info. "
          "For a systemd docker.service, the reload signal is: "
          "sudo systemctl kill --kill-who=main --signal=HUP docker.service. "
          "This installer does not change runtime settings or restart Docker.", file=sys.stderr)

def unknown(reason):
    print("HTTP registry preflight: cannot verify target-node trust for " + authority + " (" + reason + "). "
          "Every eligible Kubernetes node must separately allow HTTP image pulls from this exact authority; "
          "Docker settings on another client are not evidence for those nodes. Ask the node administrator "
          "to verify that node's container-runtime policy; this installer does not change it.", file=sys.stderr)
    sys.exit(0)

def run(command, arguments, timeout, env=None):
    return subprocess.run(["bash", "-c", command, "registry-preflight", *arguments],
                          capture_output=True, text=True, timeout=timeout, env=env)

try:
    local_boot_id = str(uuid.UUID(boot_id.strip()))
except (ValueError, AttributeError):
    unknown("local Linux boot identity is unavailable")
try:
    result = run('kubectl --context "$1" --request-timeout=10s get nodes -o json', [context], 12)
    if result.returncode:
        unknown("node inventory is unavailable")
    nodes = json.loads(result.stdout).get("items")
    if not isinstance(nodes, list) or len(nodes) != 1:
        unknown("this is not a verified single-node cluster")
    node = nodes[0]
    info = node["status"]["nodeInfo"]
    if not str(info.get("containerRuntimeVersion", "")).startswith("docker://"):
        unknown("the target node does not report Docker")
    if str(uuid.UUID(info.get("bootID", ""))) != local_boot_id:
        unknown("the Kubernetes node is not this booted host")
    names = {node["metadata"]["name"]}
    names.update(item["address"] for item in node["status"].get("addresses", [])
                 if item.get("type") == "Hostname" and isinstance(item.get("address"), str))
    env = os.environ.copy()
    for key in ("DOCKER_HOST", "DOCKER_CONTEXT", "DOCKER_TLS", "DOCKER_TLS_VERIFY",
                "DOCKER_CERT_PATH", "DOCKER_API_VERSION"):
        env.pop(key, None)
    template = '{"name":{{json .Name}},"kernel":{{json .KernelVersion}},"registry":{{json .RegistryConfig}}}'
    result = run('docker --host unix:///var/run/docker.sock info --format "$1"', [template], 10, env)
    if result.returncode:
        unknown("the local Unix Docker daemon is unavailable or access is denied")
    docker = json.loads(result.stdout)
    if docker.get("name") not in names or docker.get("kernel") != info.get("kernelVersion"):
        unknown("the local Docker daemon does not match the Kubernetes node")
    registry = docker["registry"]
    indexes, cidrs = registry["IndexConfigs"], registry["InsecureRegistryCIDRs"]
    if not isinstance(indexes, dict) or not isinstance(cidrs, list):
        unknown("Docker registry policy is unreadable")
    entry = indexes.get(authority)
    if isinstance(entry, dict) and entry.get("Secure") is False:
        sys.exit(0)
    address = ipaddress.ip_address(host)
    networks = [ipaddress.ip_network(value, strict=False) for value in cidrs]
    if any(address in network for network in networks):
        sys.exit(0)
except (ValueError, TypeError, KeyError, AttributeError, OSError, subprocess.TimeoutExpired):
    unknown("local-node identity or Docker registry policy could not be verified")

print("HTTP registry preflight blocked: verified local Docker node "
      + node["metadata"]["name"] + " does not trust " + authority + " for HTTP image pulls.", file=sys.stderr)
guidance()
sys.exit(1)
HTTP_REGISTRY_PY
}
# END HTTP_REGISTRY_PREFLIGHT
: "${KUBEATLAS_PROFILE:?profile is required}"
: "${KUBEATLAS_ORIGIN:?origin is required}"
: "${KUBEATLAS_VERSION:?version is required}"
: "${KUBEATLAS_BOOTSTRAP_SHA256:?checksum is required}"
[[ "$KUBEATLAS_PROFILE" == lan || "$KUBEATLAS_PROFILE" == public ]] || fail 'profile is invalid'
[[ "$KUBEATLAS_BOOTSTRAP_SHA256" =~ ^[0-9a-f]{64}$ ]] || fail 'bootstrap checksum is invalid'
[[ "$KUBEATLAS_VERSION" =~ ^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$ ]] || fail 'catalog version is invalid'
validate_origin() {
  python3 - "$1" <<'PY'
import ipaddress
import re
import sys
from urllib.parse import urlsplit

try:
    value = urlsplit(sys.argv[1])
    host = value.hostname
    port = value.port
    if value.scheme not in ("http", "https") or not value.netloc or host is None:
        raise ValueError()
    if value.username is not None or value.password is not None or value.path not in ("", "/") or value.query or value.fragment:
        raise ValueError()
    if port is not None and not 1 <= port <= 65535:
        raise ValueError()
    try:
        ipaddress.ip_address(host)
        literal_ip = True
    except ValueError:
        literal_ip = False
        if len(host) > 253 or not all(re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9-]{0,61}[A-Za-z0-9])?", label) for label in host.split(".")):
            raise ValueError()
    if value.scheme == "http" and not (literal_ip or host.lower() == "localhost"):
        raise ValueError()
except ValueError:
    sys.exit(1)
PY
}
validate_origin "$KUBEATLAS_ORIGIN" || fail 'origin must be HTTPS DNS/IP or HTTP literal IP/loopback'
origin="${KUBEATLAS_ORIGIN%/}"
ca_file="${KUBEATLAS_AGENT_HUB_CA_FILE:-}"
if [[ -n "$ca_file" ]]; then
  [[ -f "$ca_file" && ! -L "$ca_file" && -r "$ca_file" ]] ||
    fail 'KUBEATLAS_AGENT_HUB_CA_FILE must be a readable regular file, not a symlink'
fi
for tool in curl kubectl helm sha256sum jq mktemp chmod grep awk sleep python3; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool is missing: $tool"
done
work="$(mktemp -d)" || fail 'cannot create private working directory'
chmod 700 "$work"
id='' cluster_id='' namespace='' release='' claim_state=not-started
bootstrap_cleanup() {
  local status=$?
  trap - EXIT
  if (( status != 0 )) && [[ "$claim_state" != not-started ]]; then
    printf 'Registration recovery: claim state=%s; cluster=%q; context=%q; namespace=%q; release=%q; kube-system UID=%q.\n' "$claim_state" "${cluster_id:-unknown}" "${context:-unknown}" "${namespace:-unknown}" "${release:-unknown}" "${system_uid:-unknown}" >&2
    printf 'Check %s/clusters and the displayed Kubernetes target before retrying. The Hub may already have a cluster record and the one-time claim cannot be replayed. Repair the existing release or resolve retained resources before approving a new pairing.\n' "$origin" >&2
  elif (( status != 0 )) && [[ -n "$id" ]]; then
    printf 'Enrollment %s was not claimed; this bootstrap made no Kubernetes changes. Pending approval expires after about ten minutes.\n' "$id" >&2
  fi
  if ! rm -rf -- "$work"; then
    printf 'Private working directory cleanup failed: %q; protected credential files may remain.\n' "$work" >&2
    (( status != 0 )) || status=1
  fi
  exit "$status"
}
trap bootstrap_cleanup EXIT
context="$(kubectl config current-context 2>/dev/null)" || fail 'cannot read current Kubernetes context'
[[ -n "$context" ]] || fail 'current Kubernetes context is empty'
kubectl --context "$context" config view --raw --flatten --minify >"$work/kubeconfig" || fail 'cannot freeze kubeconfig'
chmod 600 "$work/kubeconfig"
[[ -s "$work/kubeconfig" ]] || fail 'frozen kubeconfig is empty'
export KUBECONFIG="$work/kubeconfig"
[[ "$(kubectl config current-context 2>/dev/null)" == "$context" ]] || fail 'frozen context changed'
api_server="$(kubectl --context "$context" config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)" || fail 'cannot read API server'
system_uid="$(kubectl --context "$context" get namespace kube-system -o jsonpath='{.metadata.uid}' 2>/dev/null)" || fail 'cannot read kube-system identity'
[[ -n "$api_server" && -n "$system_uid" ]] || fail 'cluster identity is incomplete'
kubectl --context "$context" cluster-info >/dev/null 2>&1 || fail 'cluster preflight failed'
printf 'Selected context: %s\nAPI server: %s\nkube-system UID: %s\n' "$context" "$api_server" "$system_uid" >&2
if [[ "$origin" == http://* ]]; then
  printf '%s\n' 'WARNING: HTTP exposes enrollment credentials, the returned Agent token, and artifact downloads to network interception; checksum verification does not authenticate the transport. Use only a trusted network.' >&2
fi
http_registry_preflight "$origin" "$context" || fail 'HTTP registry trust is missing; enrollment was not started and no Kubernetes changes were made'
fingerprint="$(printf '%s' "$system_uid" | sha256sum | awk '{print $1}')"
printf '%s\n' "$(jq -cn --arg profile "$KUBEATLAS_PROFILE" --arg contextName "$context" --arg clusterFingerprint "$fingerprint" '{profile:$profile,contextName:$contextName,clusterFingerprint:$clusterFingerprint}')" >"$work/request.json"
curl_args=(--disable --silent --show-error --proto '=https,http' --connect-timeout 10 --max-time 30 --max-redirs 0 --write-out '%{http_code}')
if [[ -n "$ca_file" ]]; then curl_args+=(--cacert "$ca_file"); fi
status="$(curl "${curl_args[@]}" --header 'Content-Type: application/json' --data-binary "@$work/request.json" --output "$work/response.json" "$origin/api/hub/enrollments")" || fail 'could not reach the Hub to create an enrollment; check the connection profile and certificate'
[[ "$status" == 201 ]] || fail "Hub did not accept enrollment request (HTTP $status); check the selected connection profile and Agent catalog"
claim_deadline=$((SECONDS + 600))
id="$(jq -er '.id | select(test("^[0-9a-f]{32}$"))' "$work/response.json")" || fail 'enrollment ID is invalid'
device="$(jq -er '.deviceSecret | select(test("^ka_device_[0-9a-f]{64}$"))' "$work/response.json")" || fail 'device secret is invalid'
code="$(jq -er '.userCode | select(test("^[A-Z2-9]{4}-[A-Z2-9]{4}$"))' "$work/response.json")" || fail 'approval code is invalid'
namespace="kubeatlas-agent-${id:0:10}"
release="$namespace"
fullname="$release"
if [[ "$release" != *kubeatlas* ]]; then fullname="${release}-kubeatlas"; fi
fullname="${fullname:0:63}"
for permission in 'create namespaces' 'create secrets' 'create serviceaccounts' 'create configmaps' 'create deployments' 'create services' 'create clusterroles' 'create clusterrolebindings' 'bind clusterroles' 'escalate clusterroles' 'create roles' 'create rolebindings'; do
  read -r verb resource <<<"$permission"
  [[ "$(kubectl --context "$context" auth can-i "$verb" "$resource" --namespace "$namespace" 2>/dev/null)" == yes ]] || fail "installation privilege preflight failed: $permission"
done
if output="$(kubectl --context "$context" get namespace "$namespace" --ignore-not-found -o name 2>&1)"; then [[ -z "$output" ]] || fail "namespace collision: $namespace"; else fail 'cannot verify namespace'; fi
if output="$(helm list --kube-context "$context" --namespace "$namespace" --filter "^$release$" --output json 2>&1)"; then [[ "$output" == '[]' ]] || fail "Helm release collision: $release"; else fail 'cannot verify Helm release'; fi
for kind_name in \
  "clusterrole/$fullname" \
  "clusterrolebinding/$fullname" \
  "clusterrolebinding/${fullname}-hub-owner" \
  "clusterrolebinding/${fullname}-hub-role-owner" \
  "clusterrolebinding/${fullname}-hub-role-member" \
  "clusterrolebinding/${fullname}-hub-role-viewer" \
  "clusterrole/${fullname}-hub-infra-readonly" \
  "clusterrolebinding/${fullname}-hub-infra-readonly"; do
  kind="${kind_name%%/*}"; name="${kind_name#*/}"
  output="$(kubectl --context "$context" get "$kind" "$name" --ignore-not-found -o name 2>/dev/null)" || fail "cannot verify $kind/$name"
  [[ -z "$output" ]] || fail "cluster-scoped resource collision: $kind/$name"
done
printf 'Enrollment %s; approval code %s. Approve this code in the Hub.\n' "$id" "$code" >&2
printf 'Install only in the displayed context %q? [y/N] ' "$context" >&2
read -r answer
[[ "$answer" == y || "$answer" == Y || "$answer" == yes || "$answer" == YES ]] || fail 'installation was not confirmed'
printf 'header = "Authorization: Bearer %s"\n' "$device" >"$work/device.curl"
chmod 600 "$work/device.curl"
unset device
claim_url="$origin/api/hub/enrollments/$id/claim"
printf 'Waiting for approval in the Hub.\n' >&2
while :; do
  (( SECONDS < claim_deadline )) || fail 'the Hub approval window elapsed'
  claim_state=attempted
  status="$(curl "${curl_args[@]}" --config "$work/device.curl" --request POST --output "$work/response.json" "$claim_url")" || fail 'lost contact with the Hub during claim; check the Hub before trying again'
  case "$status" in
    200)
      claim_status="$(jq -er '.status' "$work/response.json")" || fail 'invalid response while claiming enrollment'
      [[ "$claim_status" == approved ]] && break
      [[ "$claim_status" == pending ]] || fail 'unexpected status while claiming enrollment'
      claim_state=not-started
      ;;
    401|409) fail "Hub enrollment is expired, denied, or unavailable (HTTP $status)";;
    429) fail 'Hub enrollment rate limit reached (HTTP 429)';;
    503) fail 'Hub could not complete enrollment (HTTP 503); check catalog and connection profile availability';;
    *) fail "Hub enrollment claim failed (HTTP $status)";;
  esac
  sleep 5
done
claim_state=claimed
printf 'Hub approval received. Downloading the pinned Agent installer.\n' >&2
cluster_id="$(jq -er '.clusterId | select(test("^[a-z0-9][a-z0-9-]{0,62}$"))' "$work/response.json")" || fail 'cluster ID is invalid'
owner="$(jq -er '.ownerUserId | select(test("^kubeatlas:portal:[0-9a-f]{64}$"))' "$work/response.json")" || fail 'Hub owner identity is invalid'
version="$(jq -er '.version | select(type == "string")' "$work/response.json")" || fail 'claim version is invalid'
[[ "$version" == "$KUBEATLAS_VERSION" ]] || fail 'claim version does not match the pinned bootstrap version'
image="$(jq -er '.imageDigest | select(test("^sha256:[0-9a-f]{64}$"))' "$work/response.json")"
chart="$(jq -er '.chartSHA256 | select(test("^[0-9a-f]{64}$"))' "$work/response.json")"
installer_sha="$(jq -er '.installerSHA256 | select(test("^[0-9a-f]{64}$"))' "$work/response.json")"
token="$(jq -er '.agentToken | select(test("^ka_agent_[A-Za-z0-9_-]{43}$"))' "$work/response.json")"
[[ "$(jq -er '.hubOrigin' "$work/response.json")" == "$origin" ]] || fail 'claim origin mismatch'
claim_namespace="$(jq -er '.namespace | select(test("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"))' "$work/response.json")" || fail 'claim namespace is invalid'
claim_release="$(jq -er '.release | select(test("^[a-z0-9]([-a-z0-9]*[a-z0-9])?$"))' "$work/response.json")" || fail 'claim release is invalid'
[[ "$claim_namespace" == "$namespace" && "$claim_release" == "$release" ]] || fail 'claim installation target differs from preflight'
printf '%s' "$token" >"$work/token"
chmod 600 "$work/token"
unset token
status="$(curl "${curl_args[@]}" --output "$work/installer.sh" "$origin/downloads/agent/$version/installer.sh")" || fail 'installer download failed'
[[ "$status" == 200 ]] || fail 'installer endpoint returned non-200; redirects are not followed'
printf '%s  %s\n' "$installer_sha" "$work/installer.sh" | sha256sum --check --status || fail 'installer checksum mismatch'
chmod 700 "$work/installer.sh"
installer_args=(
  --hub-origin "$origin" --context "$context" --cluster-id "$cluster_id"
  --version "$version" --image-digest "$image" --chart-sha256 "$chart"
  --owner-user-id "$owner" --token-file "$work/token" --namespace "$namespace" --release "$release"
)
if [[ -n "$ca_file" ]]; then installer_args+=(--ca-file "$ca_file"); fi
if jq -e 'has("openCostChartSHA256") or has("openCostImageDigest")' "$work/response.json" >/dev/null; then
  cost_chart="$(jq -er '.openCostChartSHA256 | select(test("^[0-9a-f]{64}$"))' "$work/response.json")" || fail 'OpenCost chart checksum is invalid'
  cost_image="$(jq -er '.openCostImageDigest | select(test("^sha256:[0-9a-f]{64}$"))' "$work/response.json")" || fail 'OpenCost image digest is invalid'
  installer_args+=(--opencost-chart-sha256 "$cost_chart" --opencost-image-digest "$cost_image")
fi
bash "$work/installer.sh" "${installer_args[@]}"
