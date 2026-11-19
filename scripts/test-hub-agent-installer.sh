#!/usr/bin/env bash
set -Eeuo pipefail
script="$(cd "$(dirname "$0")" && pwd)/install-hub-agent.sh"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
bash -n "$script"
bash "$script" --help >/dev/null

export FAKE_MUTATIONS="$tmp/mutations"
FAKE_CHART_SHA="$(printf 'chart fixture' | sha256sum | awk '{print $1}')"
export FAKE_CHART_SHA
export FAKE_IMAGE_BODY='{"schemaVersion":2,"mediaType":"application/vnd.oci.image.manifest.v1+json"}'
FAKE_IMAGE_DIGEST="sha256:$(printf '%s' "$FAKE_IMAGE_BODY" | sha256sum | awk '{print $1}')"
export FAKE_IMAGE_DIGEST
export FAKE_CONTEXTS=test-context
export FAKE_HTTP_STATUS=200
export FAKE_CHECKSUM_SHA="$FAKE_CHART_SHA"
export FAKE_LOCAL_BOOT_ID=11111111-1111-4111-8111-111111111111
export FAKE_DOCKER_REGISTRY='{"IndexConfigs":{},"InsecureRegistryCIDRs":["127.0.0.0/8"]}'

cat() {
  if [[ "$*" == /proc/sys/kernel/random/boot_id ]]; then printf '%s\n' "$FAKE_LOCAL_BOOT_ID"; else command cat "$@"; fi
}
docker() {
  [[ "$1" == --host && "$2" == unix:///var/run/docker.sock && "$3" == info ]] || return 80
  [[ -z "${DOCKER_HOST+x}" && -z "${DOCKER_CONTEXT+x}" ]] || return 81
  printf '{"name":"fixture-node","kernel":"fixture-kernel","registry":%s}\n' "$FAKE_DOCKER_REGISTRY"
}

curl() {
  local output='' headers='' url=''
  while (($#)); do
    case "$1" in
      --output) output="$2"; shift 2;;
      --dump-header) headers="$2"; shift 2;;
      --cacert|--write-out) shift 2;;
      --silent|--show-error|--proto|--tlsv1.2|--fail)
        shift
        if (($#)) && [[ "$1" == "=http,https" ]]; then shift; fi
        ;;
      *) url="$1"; shift;;
    esac
  done
  [[ "${FAKE_CURL_FAIL:-0}" != 1 ]] || return 7
  case "$url" in
    */checksums.txt) printf '%s  chart.tgz\n' "$FAKE_CHECKSUM_SHA" >"$output";;
    */chart.tgz) printf 'chart fixture' >"$output";;
    */manifests/*) printf '%s' "$FAKE_IMAGE_BODY" >"$output"; printf 'Docker-Content-Digest: %s\r\n' "$FAKE_IMAGE_DIGEST" >"$headers";;
    *) return 8;;
  esac
  printf '%s' "$FAKE_HTTP_STATUS"
}
kubectl() {
  if [[ "$1" == --context ]]; then shift 2; fi
  if [[ "${1:-}" == --request-timeout=* ]]; then shift; fi
  if [[ "$1" == config ]]; then
    case "$2" in
      get-contexts) printf '%s\n' "$FAKE_CONTEXTS";;
      current-context) printf '%s\n' 'test-context';;
      view)
        if [[ "$*" == *jsonpath* ]]; then printf '%s\n' 'https://kube-api.example.test'; else printf '%s\n' 'current-context: test-context'; fi;;
    esac
    return 0
  fi
  case "$1" in
    cluster-info) return 0;;
    auth) printf 'yes\n';;
    get)
      if [[ "$2" == nodes ]]; then
        printf '{"items":[{"metadata":{"name":"fixture-node"},"status":{"nodeInfo":{"bootID":"%s","kernelVersion":"fixture-kernel","containerRuntimeVersion":"docker://24.0.9"}}}]}\n' "$FAKE_LOCAL_BOOT_ID"
        return 0
      fi
      if [[ "$2" == namespace && "$3" == kube-system ]]; then printf '%s\n' 'test-kube-system-uid'; return 0; fi
      local kind="$2" name="$3"
      if [[ "$kind" == namespace && "${FAKE_NAMESPACE_COLLISION:-0}" == 1 ]] ||
        [[ "$kind" == clusterrole && "${FAKE_CLUSTERROLE_COLLISION:-0}" == 1 ]]; then printf '%s/%s\n' "$kind" "$name"; fi
      ;;
    create) printf 'kubectl %s\n' "$*" >>"$FAKE_MUTATIONS";;
    *) return 9;;
  esac
}
helm() {
  case "$1" in
    list) [[ "${FAKE_RELEASE_COLLISION:-0}" == 1 ]] && printf '[{"name":"kubeatlas"}]\n' || printf '[]\n';;
    template) printf 'kind: Deployment\n';;
    install)
      printf 'helm install %s\n' "$*" >>"$FAKE_MUTATIONS"
      [[ "${FAKE_HELM_INSTALL_FAIL:-0}" != 1 ]] || return 42
      ;;
    *) return 10;;
  esac
}
export -f curl kubectl helm cat docker

token="$tmp/token"
printf '  %s  \n' "$(printf 't%.0s' {1..40})" >"$token"
chmod 600 "$token"
common=(--hub-origin https://hub.example.test --context test-context --cluster-id test-cluster --version 1.2.3
  --image-digest "$FAKE_IMAGE_DIGEST" --chart-sha256 "$FAKE_CHART_SHA"
  --owner-user-id "kubeatlas:portal:$(printf 'c%.0s' {1..64})" --token-file "$token")

expect_failure() {
  local expected="$1"; shift
  : >"$FAKE_MUTATIONS"
  local output
  if output="$(bash "$script" "${common[@]}" "$@" 2>&1)"; then
    printf 'installer unexpectedly succeeded: %s\n' "$expected" >&2; exit 1
  fi
  [[ "$output" == *"$expected"* ]] || { printf 'missing expected error %q in %s\n' "$expected" "$output" >&2; exit 1; }
  [[ ! -s "$FAKE_MUTATIONS" ]] || { printf 'mutation occurred before preflight: %s\n' "$(<"$FAKE_MUTATIONS")" >&2; exit 1; }
}

FAKE_CHECKSUM_SHA="$(printf 'x%.0s' {1..64})" expect_failure 'catalog chart checksum does not match'
FAKE_CHECKSUM_SHA="$FAKE_CHART_SHA" FAKE_HTTP_STATUS=302 expect_failure 'non-200 response'
FAKE_HTTP_STATUS=200 FAKE_CURL_FAIL=1 expect_failure 'download failed'
FAKE_CURL_FAIL=0 FAKE_CONTEXTS=other expect_failure 'explicit Kubernetes context is unavailable'
FAKE_CONTEXTS=test-context FAKE_NAMESPACE_COLLISION=1 expect_failure 'namespace already exists'
FAKE_NAMESPACE_COLLISION=0 FAKE_RELEASE_COLLISION=1 expect_failure 'Helm release already exists'
FAKE_RELEASE_COLLISION=0 FAKE_CLUSTERROLE_COLLISION=1 expect_failure 'existing cluster-scoped resource'
FAKE_CLUSTERROLE_COLLISION=0
: >"$FAKE_MUTATIONS"
if output="$(printf 'n\n' | bash "$script" "${common[@]}" 2>&1)"; then
  printf 'declined installation unexpectedly succeeded\n' >&2; exit 1
fi
[[ "$output" == *'only the exact local Hub owner User is bound to cluster-admin'* && "$output" == *'node must independently trust'* ]] || { printf 'prerequisites were not shown before confirmation: %s\n' "$output" >&2; exit 1; }
[[ ! -s "$FAKE_MUTATIONS" ]] || { printf 'declined install mutated the cluster\n' >&2; exit 1; }
: >"$FAKE_MUTATIONS"
output="$(bash "$script" "${common[@]}" --dry-run 2>&1)"
[[ "$output" == *'dry run made no Kubernetes changes'* && ! -s "$FAKE_MUTATIONS" ]] || { printf 'dry run mutated or failed: %s\n' "$output" >&2; exit 1; }
chmod 644 "$token"
expect_failure 'owner-readable and inaccessible'
chmod 600 "$token"
printf 'short\n' >"$token"
expect_failure 'trimmed token must contain at least 32 bytes'
printf '%s %s\n' "$(printf 't%.0s' {1..20})" "$(printf 'u%.0s' {1..20})" >"$token"
expect_failure 'trimmed token must not contain whitespace or control characters'
printf '%s\0%s' "$(printf 't%.0s' {1..20})" "$(printf 'u%.0s' {1..20})" >"$token"
expect_failure 'token file contains unsupported control characters'
printf '%4097s' '' | tr ' ' x >"$token"
expect_failure 'at most 4096 bytes'
if grep -Eq -- 'curl.*(--location|(^|[[:space:]])-L([[:space:]]|$))' "$script"; then
  printf 'installer must not follow redirects\n' >&2; exit 1
fi

printf '  %s  \n' "$(printf 't%.0s' {1..40})" >"$token"
common[1]=http://192.0.2.220:8080
expect_failure 'HTTP registry preflight blocked' --yes
export FAKE_DOCKER_REGISTRY='{"IndexConfigs":{"192.0.2.220:8080":{"Secure":false}},"InsecureRegistryCIDRs":["127.0.0.0/8"]}'
export FAKE_HELM_INSTALL_FAIL=1
: >"$FAKE_MUTATIONS"
if output="$(bash "$script" "${common[@]}" --yes 2>&1)"; then printf 'Helm failure unexpectedly passed\n' >&2; exit 1; fi
[[ "$output" == *'Installation recovery:'* && "$output" == *'namespace=kubeatlas'* && "$output" == *'context=test-context'* && "$output" == *'Resources may already exist'* && "$output" == *'192.0.2.220:8080'* ]] || { printf 'Helm failure lacked target/recovery guidance: %s\n' "$output" >&2; exit 1; }
[[ "$(wc -l <"$FAKE_MUTATIONS")" -eq 3 ]] || { printf 'unexpected post-failure mutations\n' >&2; exit 1; }
if grep -Eq '(delete|uninstall|upgrade|patch|apply|replace)' "$FAKE_MUTATIONS"; then printf 'failed install cleaned up or adopted resources\n' >&2; exit 1; fi
[[ "$output" != *"$(printf 't%.0s' {1..40})"* ]] || { printf 'Helm failure leaked token\n' >&2; exit 1; }
printf 'Hub Agent installer shell tests passed (preflight failures, local Docker registry, collisions, confirmation, dry-run, token validation, failed-install recovery).\n'
