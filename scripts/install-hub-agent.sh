#!/usr/bin/env bash
set -Eeuo pipefail
umask 077

fail() { printf 'hub agent installer: %s\n' "$*" >&2; exit 1; }

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
if [[ "$hub_origin" =~ ^https://([A-Za-z0-9.-]+|\[[0-9A-Fa-f:]+\])(:[0-9]{1,5})?/?$ ]]; then
  :
elif [[ "$hub_origin" =~ ^http://(localhost|127\.[0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3}|\[::1\])(:[0-9]{1,5})?/?$ ]]; then
  :
else
  fail 'Hub origin must be HTTPS except for loopback development origins'
fi
hub_origin="${hub_origin%/}"
[[ "$cluster_id" =~ ^[a-z0-9][a-z0-9-]{0,62}$ ]] || fail 'cluster ID is invalid'
[[ "$version" =~ ^[A-Za-z0-9][A-Za-z0-9.+_-]{0,127}$ ]] || fail 'catalog version is invalid'
[[ "$image_digest" =~ ^sha256:[0-9a-f]{64}$ ]] || fail 'image digest is invalid'
[[ "$chart_sha256" =~ ^[0-9a-f]{64}$ ]] || fail 'chart checksum is invalid'
[[ "$owner_user_id" =~ ^kubeatlas:portal:[0-9a-f]{64}$ ]] || fail 'local Hub owner identity is invalid'
[[ "$namespace" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ && ${#namespace} -le 63 ]] || fail 'namespace is invalid'
[[ "$release" =~ ^[a-z0-9]([-a-z0-9]*[a-z0-9])?$ && ${#release} -le 53 ]] || fail 'release name is invalid'
if [[ "$release" != *kubeatlas* && ${#release} -gt 43 ]]; then fail 'release name is too long for the exact owner binding'; fi

for tool in curl helm kubectl sha256sum grep awk mktemp stat; do
  command -v "$tool" >/dev/null 2>&1 || fail "required tool is missing: $tool"
done
[[ -f "$token_file" && ! -L "$token_file" && -r "$token_file" ]] || fail 'token file must be a readable regular file, not a symlink'
token_mode="$(stat -c '%a' -- "$token_file" 2>/dev/null)" || fail 'cannot inspect token file permissions'
(( (8#$token_mode & 077) == 0 )) || fail 'token file must not be accessible by group or other users'
[[ -s "$token_file" ]] || fail 'token file is empty'
if [[ -n "$ca_file" ]]; then
  [[ -f "$ca_file" && ! -L "$ca_file" && -r "$ca_file" ]] || fail 'CA file must be a readable regular file, not a symlink'
fi

kubectl config get-contexts "$context" >/dev/null 2>&1 || fail 'explicit Kubernetes context is unavailable'
kubectl --context "$context" cluster-info >/dev/null 2>&1 || fail 'Kubernetes context preflight failed'
for permission in 'create namespaces' 'create secrets' 'create clusterrolebindings' 'create clusterroles' 'create serviceaccounts'; do
  read -r verb resource <<< "$permission"
  [[ "$(kubectl --context "$context" auth can-i "$verb" "$resource" --namespace "$namespace" 2>/dev/null)" == yes ]] || fail "Kubernetes permission preflight failed: $permission"
done

if output="$(kubectl --context "$context" get namespace "$namespace" -o name 2>&1)"; then
  fail "namespace already exists; this installer refuses adoption: $namespace"
elif [[ "$output" != *NotFound* && "$output" != *'not found'* ]]; then
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
for kind_name in "clusterrole/$fullname" "clusterrolebinding/$fullname" "clusterrolebinding/${fullname}-hub-owner"; do
  kind="${kind_name%%/*}" resource="${kind_name#*/}"
  if output="$(kubectl --context "$context" get "$kind" "$resource" -o name 2>&1)"; then
    fail "existing cluster-scoped resource would be adopted: $kind/$resource"
  elif [[ "$output" != *NotFound* && "$output" != *'not found'* ]]; then
    fail "unable to verify cluster-scoped resource state: $kind/$resource"
  fi
done

work="$(mktemp -d)" || fail 'cannot create private temporary directory'
chmod 700 "$work"
trap 'rm -rf -- "$work"' EXIT
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
if [[ "$assume_yes" != true ]]; then
  printf 'Install a new Hub Agent release into context %q, namespace %q? [y/N] ' "$context" "$namespace" >&2
  read -r answer
  [[ "$answer" == y || "$answer" == Y || "$answer" == yes || "$answer" == YES ]] || fail 'installation was not confirmed'
fi

kubectl --context "$context" create namespace "$namespace" || fail 'namespace creation failed; no broad cleanup was attempted'
kubectl --context "$context" create secret generic "$token_secret" --namespace "$namespace" --from-file="token=$token_file" || fail 'token Secret creation failed; owned namespace remains for reconciliation'
if [[ -n "$ca_secret" ]]; then
  kubectl --context "$context" create secret generic "$ca_secret" --namespace "$namespace" --from-file="ca.crt=$ca_file" || fail 'CA Secret creation failed; owned resources remain for reconciliation'
fi
helm install "$release" "$work/chart.tgz" --kube-context "$context" --namespace "$namespace" --wait --timeout 5m "${values[@]}" || fail 'Helm install failed; created resources remain for reconciliation'
printf 'Helm release deployed. This is not proof of an authenticated Hub connection; confirm Connected in the Hub directory.\n'
