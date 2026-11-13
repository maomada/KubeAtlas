#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
fail() { printf 'hub agent bootstrap: %s\n' "$*" >&2; exit 1; }
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
for tool in curl kubectl helm sha256sum jq mktemp chmod grep awk sleep python3; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool is missing: $tool"
done
work="$(mktemp -d)" || fail 'cannot create private working directory'
chmod 700 "$work"
trap 'rm -rf -- "$work"' EXIT
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
fingerprint="$(printf '%s' "$system_uid" | sha256sum | awk '{print $1}')"
printf '%s\n' "$(jq -cn --arg profile "$KUBEATLAS_PROFILE" --arg contextName "$context" --arg clusterFingerprint "$fingerprint" '{profile:$profile,contextName:$contextName,clusterFingerprint:$clusterFingerprint}')" >"$work/request.json"
curl_args=(--silent --show-error --proto '=https,http' --max-redirs 0 --write-out '%{http_code}')
status="$(curl "${curl_args[@]}" --header 'Content-Type: application/json' --data-binary "@$work/request.json" --output "$work/response.json" "$origin/api/hub/enrollments")" || fail 'enrollment request failed'
[[ "$status" == 201 ]] || fail 'Hub did not accept enrollment request'
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
while :; do
  status="$(curl "${curl_args[@]}" --config "$work/device.curl" --request POST --output "$work/response.json" "$claim_url")" || fail 'claim poll failed'
  if [[ "$status" == 200 ]] && [[ "$(jq -r '.status // empty' "$work/response.json")" == approved ]]; then break; fi
  [[ "$status" == 200 && "$(jq -r '.status // empty' "$work/response.json")" == pending ]] || fail 'request expired, denied, or failed'
  sleep 5
done
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
bash "$work/installer.sh" --hub-origin "$origin" --context "$context" --cluster-id "$cluster_id" --version "$version" --image-digest "$image" --chart-sha256 "$chart" --owner-user-id "$owner" --token-file "$work/token" --namespace "$namespace" --release "$release"
