#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

fail() { printf 'hub agent installer: %s\n' "$*" >&2; exit 1; }

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

usage() {
  printf '%s\n' 'Usage: install-hub-agent.sh --hub-origin ORIGIN --context CONTEXT --cluster-id ID --version VERSION --image-digest sha256:... --chart-sha256 HEX --owner-user-id ID --token-file FILE [--ca-file FILE] [--namespace NS] [--release NAME] [--dry-run] [--yes]'
}

hub_origin='' context='' cluster_id='' version='' image_digest='' chart_sha256='' owner_user_id=''
token_file='' ca_file='' namespace='kubeatlas' release='kubeatlas' dry_run=false assume_yes=false
while (($#)); do
  case "$1" in
    --hub-origin|--context|--cluster-id|--version|--image-digest|--chart-sha256|--owner-user-id|--token-file|--ca-file|--namespace|--release)
      (($# >= 2)) || { usage >&2; fail "missing value for $1"; }
      key="$1" value="$2"; shift 2
      case "$key" in
        --hub-origin) hub_origin="$value";; --context) context="$value";; --cluster-id) cluster_id="$value";;
        --version) version="$value";; --image-digest) image_digest="$value";; --chart-sha256) chart_sha256="$value";;
        --owner-user-id) owner_user_id="$value";; --token-file) token_file="$value";; --ca-file) ca_file="$value";;
        --namespace) namespace="$value";; --release) release="$value";;
      esac
      ;;
    --dry-run) dry_run=true; shift;;
    --yes) assume_yes=true; shift;;
    --help|-h) usage; exit 0;;
    *) usage >&2; fail "unknown option: $1";;
  esac
done

for required in "$hub_origin" "$context" "$cluster_id" "$version" "$image_digest" "$chart_sha256" "$owner_user_id" "$token_file"; do
  [[ -n "$required" ]] || { usage >&2; fail 'all required options must be provided explicitly'; }
done
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
validate_origin "$hub_origin" || fail 'Hub origin must be HTTPS DNS/IP or HTTP literal IP/loopback'
hub_origin="${hub_origin%/}"
[[ "$cluster_id" =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || fail 'cluster ID is invalid'
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$ ]] || fail 'catalog version is invalid'
[[ "$image_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'image digest is invalid'
[[ "$chart_sha256" =~ ^[0-9a-f]{64}$ ]] || fail 'chart checksum is invalid'
[[ "$owner_user_id" =~ ^kubeatlas:portal:[0-9a-f]{64}$ ]] || fail 'local Hub owner identity is invalid'
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ && ${#namespace} -le 63 ]] || fail 'namespace is invalid'
[[ "$release" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ && ${#release} -le 53 ]] || fail 'release name is invalid'
if [[ "$release" != *kubeatlas* && ${#release} -gt 43 ]]; then fail 'release name is too long for the exact owner binding'; fi

for tool in curl helm kubectl sha256sum grep awk mktemp stat wc od tr cat python3; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool is missing: $tool"
done
work="$(mktemp -d)" || fail 'cannot create private temporary directory'
chmod 700 "$work"
mutation_started=false
installer_cleanup() {
  local status=$?
  trap - EXIT
  if (( status != 0 )) && [[ "$mutation_started" == true ]]; then
    printf 'Installation recovery: cluster=%q; context=%q; namespace=%q; release=%q; kube-system UID=%q.\n' "$cluster_id" "$context" "$namespace" "$release" "${system_uid:-unknown}" >&2
    printf '%s\n' 'Installation did not complete. Resources may already exist and are retained for reconciliation; this installer does not adopt or delete them. Verify the displayed cluster identity, then inspect Pod status and warning Events in this namespace. Do not rerun a fresh install into an existing namespace.' >&2
    if [[ "$hub_origin" == http://* ]]; then
      printf 'For ImagePullBackOff or ErrImagePull, verify that every eligible node explicitly trusts HTTP registry %s; successful curl downloads do not prove container-runtime trust.\n' "${hub_origin#http://}" >&2
    fi
  fi
  if ! rm -rf -- "$work"; then
    printf 'Private working directory cleanup failed: %q.\n' "$work" >&2
    (( status != 0 )) || status=1
  fi
  exit "$status"
}
trap installer_cleanup EXIT
[[ -f "$token_file" && ! -L "$token_file" && -r "$token_file" ]] || fail 'token file must be a readable regular file, not a symlink'
token_mode="$(stat -c '%a' -- "$token_file" 2>/dev/null)" || fail 'cannot inspect token file permissions'
(( (8#$token_mode & 0400) != 0 && (8#$token_mode & 077) == 0 )) || fail 'token file must be owner-readable and inaccessible to group or other users'
token_size="$(wc -c <"$token_file")" || fail 'cannot inspect token file size'
(( token_size > 0 && token_size <= 4096 )) || fail 'token file must contain at most 4096 bytes'
if LC_ALL=C od -An -v -t x1 -- "$token_file" | tr -s '[:space:]' '\n' | grep -Fxq '00'; then
  fail 'token file contains unsupported control characters'
fi
token_value="$(LC_ALL=C cat -- "$token_file")" || fail 'cannot read token file'
token_value="${token_value#"${token_value%%[!$' \t\r\n']*}"}"
token_value="${token_value%"${token_value##*[!$' \t\r\n']}"}"
token_length="$(LC_ALL=C printf '%s' "$token_value" | wc -c)" || fail 'cannot validate trimmed token length'
(( token_length >= 32 )) || fail 'trimmed token must contain at least 32 bytes'
[[ ! "$token_value" =~ [[:space:][:cntrl:]] ]] || fail 'trimmed token must not contain whitespace or control characters'
if [[ -n "$ca_file" ]]; then
  [[ -f "$ca_file" && ! -L "$ca_file" && -r "$ca_file" ]] || fail 'CA file must be a readable regular file, not a symlink'
fi

contexts="$(kubectl config get-contexts -o name 2>/dev/null)" || fail 'cannot list Kubernetes contexts'
printf '%s\n' "$contexts" | grep -Fxq -- "$context" || fail 'explicit Kubernetes context is unavailable'
kubectl --context "$context" config view --raw --flatten --minify >"$work/kubeconfig" || fail 'cannot freeze explicit Kubernetes context'
chmod 600 "$work/kubeconfig"
[[ -s "$work/kubeconfig" ]] || fail 'frozen kubeconfig is empty'
export KUBECONFIG="$work/kubeconfig"
[[ "$(kubectl config current-context 2>/dev/null)" == "$context" ]] || fail 'frozen kubeconfig changed the selected context'
api_server="$(kubectl --context "$context" config view --minify -o jsonpath='{.clusters[0].cluster.server}' 2>/dev/null)" || fail 'cannot read Kubernetes API server'
system_uid="$(kubectl --context "$context" get namespace kube-system -o jsonpath='{.metadata.uid}' 2>/dev/null)" || fail 'cannot read kube-system identity'
[[ -n "$api_server" && -n "$system_uid" ]] || fail 'Kubernetes context identity is incomplete'
printf 'Selected Kubernetes context: %s\nAPI server: %s\nkube-system UID: %s\n' "$context" "$api_server" "$system_uid" >&2
kubectl --context "$context" cluster-info >/dev/null 2>&1 || fail 'Kubernetes context preflight failed'
http_registry_preflight "$hub_origin" "$context" || fail 'HTTP registry trust is missing; no Kubernetes changes were made'
for permission in 'create namespaces' 'create secrets' 'create clusterrolebindings' 'create clusterroles' 'bind clusterroles' 'escalate clusterroles' 'create serviceaccounts' 'create configmaps' 'create deployments' 'create services' 'create roles' 'create rolebindings'; do
  read -r verb resource <<< "$permission"
  [[ "$(kubectl --context "$context" auth can-i "$verb" "$resource" --namespace "$namespace" 2>/dev/null)" == yes ]] || fail "Kubernetes permission preflight failed: $permission"
done

if output="$(kubectl --context "$context" get namespace "$namespace" --ignore-not-found -o name 2>&1)"; then
  [[ -z "$output" ]] || fail "namespace already exists; this installer refuses adoption: $namespace"
else
  fail 'unable to verify that the target namespace is new'
fi
if output="$(helm list --kube-context "$context" --namespace "$namespace" --filter "^${release}$" --output json 2>&1)"; then
  [[ "$output" == '[]' || "$output" == *'"name"'* ]] || fail 'could not verify Helm release state'
  [[ "$output" == '[]' ]] || fail "Helm release already exists; this installer refuses adoption: $release"
else
  fail 'could not verify Helm release state'
fi
fullname="$release"
if [[ "$release" != *kubeatlas* ]]; then fullname="${release}-kubeatlas"; fi
fullname="${fullname:0:63}"
for kind_name in \
  "clusterrole/$fullname" \
  "clusterrolebinding/$fullname" \
  "clusterrolebinding/${fullname}-hub-owner" \
  "clusterrolebinding/${fullname}-hub-role-owner" \
  "clusterrolebinding/${fullname}-hub-role-member" \
  "clusterrolebinding/${fullname}-hub-role-viewer" \
  "clusterrole/${fullname}-hub-infra-readonly" \
  "clusterrolebinding/${fullname}-hub-infra-readonly"; do
  kind="${kind_name%%/*}" resource="${kind_name#*/}"
  if output="$(kubectl --context "$context" get "$kind" "$resource" --ignore-not-found -o name 2>&1)"; then
    [[ -z "$output" ]] || fail "existing cluster-scoped resource would be adopted: $kind/$resource"
  else
    fail "unable to verify cluster-scoped resource state: $kind/$resource"
  fi
done

curl_args=(--silent --show-error --proto '=http,https' --tlsv1.2 --write-out '%{http_code}')
if [[ -n "$ca_file" ]]; then curl_args+=(--cacert "$ca_file"); fi
download() {
  local url="$1" destination="$2" status
  status="$(curl "${curl_args[@]}" --output "$destination" "$url")" || fail 'same-origin artifact download failed'
  [[ "$status" == 200 ]] || fail 'artifact endpoint returned a non-200 response; redirects are not followed'
}

download "$hub_origin/downloads/agent/$version/checksums.txt" "$work/checksums.txt"
grep -Eq "^${chart_sha256}[[:space:]]+chart\.tgz$" "$work/checksums.txt" || fail 'catalog chart checksum does not match the requested version'
download "$hub_origin/downloads/agent/$version/chart.tgz" "$work/chart.tgz"
printf '%s  %s\n' "$chart_sha256" "$work/chart.tgz" | sha256sum --check --status || fail 'downloaded chart checksum does not match'

status="$(curl "${curl_args[@]}" --dump-header "$work/image-headers" --output "$work/image-manifest" "$hub_origin/v2/kubeatlas/agent/manifests/$image_digest")" || fail 'Hub image manifest preflight failed'
[[ "$status" == 200 ]] || fail 'Hub image manifest endpoint returned a non-200 response; redirects are not followed'
header_digest="$(awk 'tolower($1)=="docker-content-digest:" {gsub("\r", "", $2); value=$2} END {print value}' "$work/image-headers")"
[[ "$header_digest" == "$image_digest" ]] || fail 'Hub image manifest digest header does not match'
printf '%s  %s\n' "${image_digest#sha256:}" "$work/image-manifest" | sha256sum --check --status || fail 'Hub image manifest digest does not match'

token_secret="${release}-hub-token"
ca_secret=''
if [[ -n "$ca_file" ]]; then ca_secret="${release}-hub-ca"; fi
values=(--set hub.enabled=true --set-string "hub.url=$hub_origin" --set-string "hub.clusterID=$cluster_id" \
  --set-string "hub.tokenSecret=$token_secret" --set-string "hub.ownerUserID=$owner_user_id" \
  --set-string "image.repository=${hub_origin#*://}/kubeatlas/agent" --set-string "image.digest=$image_digest" \
  --set auth.mode=proxy --set mcp.enabled=true)
if [[ -n "$ca_secret" ]]; then values+=(--set-string "hub.caSecret=$ca_secret"); fi
helm template "$release" "$work/chart.tgz" --kube-context "$context" --namespace "$namespace" "${values[@]}" >"$work/rendered.yaml" || fail 'Hub Agent chart preflight render failed'

if [[ "$dry_run" == true ]]; then
  printf 'Preflight complete; dry run made no Kubernetes changes.\n'
  exit 0
fi
printf '%s\n' 'Prerequisites: only the exact local Hub owner User is bound to cluster-admin; organization role groups never receive cluster-admin.' >&2
if [[ "$hub_origin" == http://* ]]; then
  printf '%s\n' 'HTTP exposes credentials and artifact downloads to network interception. Every eligible node must be manually configured by its administrator to pull from the HTTP registry; this installer does not change node runtimes.' >&2
else
  printf '%s\n' 'Every target node must independently trust the Hub Registry TLS certificate; the Agent CA file does not configure the container runtime.' >&2
fi
if [[ "$assume_yes" != true ]]; then
  printf 'Install a new Hub Agent release into context %q, namespace %q? [y/N] ' "$context" "$namespace" >&2
  read -r answer
  [[ "$answer" == y || "$answer" == Y || "$answer" == yes || "$answer" == YES ]] || fail 'installation was not confirmed'
fi

mutation_started=true
kubectl --context "$context" create namespace "$namespace" || fail 'namespace creation failed; no broad cleanup was attempted'
kubectl --context "$context" create secret generic "$token_secret" --namespace "$namespace" --from-file="token=$token_file" || fail 'token Secret creation failed; owned namespace remains for reconciliation'
if [[ -n "$ca_secret" ]]; then
  kubectl --context "$context" create secret generic "$ca_secret" --namespace "$namespace" --from-file="ca.crt=$ca_file" || fail 'CA Secret creation failed; owned resources remain for reconciliation'
fi
helm install "$release" "$work/chart.tgz" --kube-context "$context" --namespace "$namespace" --wait --timeout 5m "${values[@]}" || fail 'Helm install failed; created resources remain for reconciliation'
printf 'Helm release deployed. This is not proof of an authenticated Hub connection; confirm Connected in the Hub directory.\n'
