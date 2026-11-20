#!/usr/bin/env bash
set -Eeuo pipefail
script="$(cd "$(dirname "$0")" && pwd)/bootstrap-hub-agent.sh"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
bash -n "$script"
bootstrap_guard="$(awk '/^# BEGIN HTTP_REGISTRY_PREFLIGHT$/ {copy=1; next} /^# END HTTP_REGISTRY_PREFLIGHT$/ {copy=0} copy' "$script")"
installer_guard="$(awk '/^# BEGIN HTTP_REGISTRY_PREFLIGHT$/ {copy=1; next} /^# END HTTP_REGISTRY_PREFLIGHT$/ {copy=0} copy' "${script%/*}/install-hub-agent.sh")"
[[ -n "$bootstrap_guard" && "$bootstrap_guard" == "$installer_guard" ]] || { printf 'HTTP registry guards have drifted\n' >&2; exit 1; }

export FAKE_ORIGIN=https://hub.example.test
export FAKE_ENROLLMENT_ID=0123456789abcdef0123456789abcdef
FAKE_DEVICE_SECRET="ka_device_$(printf 'd%.0s' {1..64})"
export FAKE_DEVICE_SECRET
FAKE_AGENT_TOKEN="ka_agent_$(printf 't%.0s' {1..43})"
export FAKE_AGENT_TOKEN
export FAKE_CLUSTER_ID=agent-cluster
export FAKE_NAMESPACE="kubeatlas-agent-${FAKE_ENROLLMENT_ID:0:10}"
FAKE_OWNER_USER_ID="kubeatlas:portal:$(printf 'c%.0s' {1..64})"
export FAKE_OWNER_USER_ID
FAKE_IMAGE_DIGEST="sha256:$(printf 'a%.0s' {1..64})"
export FAKE_IMAGE_DIGEST
FAKE_CHART_SHA256="$(printf 'chart fixture' | sha256sum | awk '{print $1}')"
export FAKE_CHART_SHA256
FAKE_INSTALLER_BODY="$(command cat <<'INSTALLER'
#!/usr/bin/env bash
set -Eeuo pipefail
printf "%s\n" "$@" >"$FAKE_INSTALLER_ARGV_LOG"
context="" token_file=""
while (($#)); do case "$1" in --context) context="$2"; shift 2;; --token-file) token_file="$2"; shift 2;; *) shift;; esac; done
[[ "$context" == "$FAKE_CONTEXT" ]]
[[ -f "$KUBECONFIG" && "$(stat -c %a "$KUBECONFIG")" == 600 ]]
[[ "$(kubectl config current-context)" == "$FAKE_CONTEXT" ]]
[[ -f "$token_file" && "$(stat -c %a "$token_file")" == 600 ]]
[[ "$(<"$token_file")" == "$FAKE_AGENT_TOKEN" ]]
printf "%s\n" "600" >>"$FAKE_TOKEN_MODES"
printf "%s\n" ran >>"$FAKE_INSTALLER_RUNS"
if [[ "${FAKE_INSTALLER_FAIL:-0}" == 1 ]]; then printf 'fixture installer failed\n' >&2; exit 23; fi
INSTALLER
)"
export FAKE_INSTALLER_BODY
FAKE_INSTALLER_SHA256="$(printf '%s' "$FAKE_INSTALLER_BODY" | sha256sum | awk '{print $1}')"
export FAKE_INSTALLER_SHA256

export FAKE_ARGV_LOG="$tmp/argv.log"
export FAKE_CURL_URL_LOG="$tmp/curl-urls.log"
export FAKE_CONTEXT_MODES="$tmp/context-modes.log"
export FAKE_DEVICE_MODES="$tmp/device-modes.log"
export FAKE_TOKEN_MODES="$tmp/token-modes.log"
export FAKE_INSTALLER_ARGV_LOG="$tmp/installer-argv.log"
export FAKE_INSTALLER_RUNS="$tmp/installer-runs.log"
export FAKE_MUTATIONS="$tmp/mutations.log"
export FAKE_GET_LOG="$tmp/get.log"
export FAKE_DOCKER_LOG="$tmp/docker.log"

# This exported fixture receives arguments from the child bootstrap/installer.
# shellcheck disable=SC2120
cat() {
  if [[ "$*" == /proc/sys/kernel/random/boot_id ]]; then printf '%s\n' "$FAKE_LOCAL_BOOT_ID"; else command cat "$@"; fi
}
docker() {
  printf '%s\n' "$*" >>"$FAKE_DOCKER_LOG"
  [[ "$1" == --host && "$2" == unix:///var/run/docker.sock && "$3" == info ]] || return 80
  [[ -z "${DOCKER_HOST+x}" && -z "${DOCKER_CONTEXT+x}" ]] || return 81
  [[ "${FAKE_DOCKER_FAIL:-0}" != 1 ]] || return 82
  printf '{"name":"%s","kernel":"%s","registry":%s}\n' "$FAKE_DOCKER_NAME" "$FAKE_DOCKER_KERNEL" "$FAKE_DOCKER_REGISTRY"
}

curl() {
  {
    printf 'curl'
    printf ' %q' "$@"
    printf '\n'
  } >>"$FAKE_ARGV_LOG"
  local output='' config='' cacert='' url='' status=200
  [[ "$1" == --disable ]] || return 90
  while (($#)); do
    case "$1" in
      --output) output="$2"; shift 2;;
      --config) config="$2"; shift 2;;
      --cacert) cacert="$2"; shift 2;;
      --disable|--silent|--show-error) shift;;
      --proto|--connect-timeout|--max-time|--max-redirs|--write-out|--header|--data-binary|--request) shift 2;;
      *) url="$1"; shift;;
    esac
  done
  [[ "$cacert" == "${KUBEATLAS_AGENT_HUB_CA_FILE:-}" ]] || { printf 'unexpected CA selection\n' >&2; return 89; }
  printf '%s\n' "$url" >>"$FAKE_CURL_URL_LOG"
  [[ "$url" == "$FAKE_ORIGIN/"* ]] || { printf 'unexpected external URL: %s\n' "$url" >&2; return 91; }
  case "$url" in
    "$FAKE_ORIGIN/api/hub/enrollments")
      printf '{"id":"%s","deviceSecret":"%s","userCode":"ABCD-EFGH"}\n' \
        "$FAKE_ENROLLMENT_ID" "$FAKE_DEVICE_SECRET" >"$output"
      status="$FAKE_CREATE_STATUS"
      ;;
    "$FAKE_ORIGIN/api/hub/enrollments/$FAKE_ENROLLMENT_ID/claim")
      [[ "${FAKE_LOST_CLAIM_RESPONSE:-0}" != 1 ]] || return 7
      [[ -n "$config" && "$(stat -c %a "$config")" == 600 ]] || { printf 'device config is not protected\n' >&2; return 92; }
      grep -Fq -- "$FAKE_DEVICE_SECRET" "$config" || { printf 'device config does not contain expected claim credential\n' >&2; return 93; }
      printf '%s\n' "$(stat -c %a "$config")" >>"$FAKE_DEVICE_MODES"
      printf '{"status":"approved","clusterId":"%s","ownerUserId":"%s","version":"%s","imageDigest":"%s","chartSHA256":"%s","installerSHA256":"%s","hubOrigin":"%s","namespace":"%s","release":"%s","agentToken":"%s"%s}\n' \
        "$FAKE_CLUSTER_ID" "$FAKE_OWNER_USER_ID" "$FAKE_CLAIM_VERSION" "$FAKE_IMAGE_DIGEST" \
        "$FAKE_CHART_SHA256" "$FAKE_CLAIM_INSTALLER_SHA256" "$FAKE_CLAIM_ORIGIN" \
        "$FAKE_CLAIM_NAMESPACE" "$FAKE_CLAIM_RELEASE" "$FAKE_AGENT_TOKEN" "$FAKE_OPENCOST_FIELDS" >"$output"
      status="$FAKE_CLAIM_STATUS"
      ;;
    "$FAKE_ORIGIN/downloads/agent/"*/installer.sh)
      printf '%s' "$FAKE_INSTALLER_BODY" >"$output"
      status="$FAKE_INSTALLER_STATUS"
      ;;
    *)
      printf 'unexpected Hub URL: %s\n' "$url" >&2
      return 94
      ;;
  esac
  printf '%s' "$status"
}

kubectl() {
  {
    printf 'kubectl'
    printf ' %q' "$@"
    printf '\n'
  } >>"$FAKE_ARGV_LOG"
  if [[ "${1:-}" == --context ]]; then shift 2; fi
  if [[ "${1:-}" == --request-timeout=* ]]; then shift; fi
  case "${1:-}" in
    config)
      case "${2:-}" in
        current-context)
          local context="$FAKE_CONTEXT"
          if [[ -n "${KUBECONFIG:-}" ]]; then
            local mode
            mode="$(stat -c %a "$KUBECONFIG")"
            [[ "$mode" == 600 ]] || return 95
            grep -Fxq "current-context: $FAKE_CONTEXT" "$KUBECONFIG" || return 96
            printf '%s:%s\n' "$FAKE_CONTEXT" "$mode" >>"$FAKE_CONTEXT_MODES"
            [[ "$FAKE_CHANGED_CONTEXT" == 1 ]] && context=changed-context
          fi
          printf '%s\n' "$context"
          ;;
        view)
          if [[ "$*" == *jsonpath* ]]; then
            printf '%s\n' 'https://kube-api.example.test'
          else
            printf 'apiVersion: v1\ncurrent-context: %s\nclusters:\n- name: test\n  cluster:\n    server: https://kube-api.example.test\n' "$FAKE_CONTEXT"
          fi
          ;;
        *) return 97;;
      esac
      ;;
    auth)
      local requested="${3:-} ${4:-}"
      if [[ "$requested" == "$FAKE_DENY_PERMISSION" ]]; then printf 'no\n'; else printf 'yes\n'; fi
      ;;
    get)
      printf '%s/%s\n' "${2:-}" "${3:-}" >>"$FAKE_GET_LOG"
      if [[ "${2:-}" == nodes ]]; then
        local node
        node="$(printf '{"metadata":{"name":"fixture-node"},"status":{"addresses":[{"type":"Hostname","address":"fixture-node"}],"nodeInfo":{"bootID":"%s","kernelVersion":"fixture-kernel","containerRuntimeVersion":"%s"}}}' "$FAKE_NODE_BOOT_ID" "$FAKE_NODE_RUNTIME")"
        if [[ "$FAKE_NODE_COUNT" == 2 ]]; then printf '{"items":[%s,%s]}\n' "$node" "$node"; else printf '{"items":[%s]}\n' "$node"; fi
      elif [[ "${2:-}" == namespace && "${3:-}" == kube-system ]]; then
        printf '%s\n' fake-kube-system-uid
      elif [[ "${2:-}" == namespace && "$FAKE_NAMESPACE_COLLISION" == 1 ]]; then
        printf 'namespace/%s\n' "$3"
      elif [[ "${2:-}/${3:-}" == "$FAKE_COLLISION_KIND/$FAKE_COLLISION_NAME" ]]; then
        printf '%s/%s\n' "$2" "$3"
      fi
      ;;
    cluster-info) return 0;;
    create|apply|delete|patch|replace)
      printf 'kubectl %s\n' "$*" >>"$FAKE_MUTATIONS"
      ;;
    *) return 98;;
  esac
}

helm() {
  {
    printf 'helm'
    printf ' %q' "$@"
    printf '\n'
  } >>"$FAKE_ARGV_LOG"
  case "${1:-}" in
    list)
      if [[ "$FAKE_RELEASE_COLLISION" == 1 ]]; then printf '[{"name":"existing"}]\n'; else printf '[]\n'; fi
      ;;
    install|upgrade|uninstall)
      printf 'helm %s\n' "$*" >>"$FAKE_MUTATIONS"
      ;;
    *) return 99;;
  esac
}
export -f curl kubectl helm cat docker

reset_case() {
  export FAKE_ORIGIN=https://hub.example.test
  export FAKE_CONTEXT=test-context FAKE_CHANGED_CONTEXT=0 FAKE_DENY_PERMISSION=''
  export FAKE_NAMESPACE_COLLISION=0 FAKE_RELEASE_COLLISION=0
  export FAKE_COLLISION_KIND='' FAKE_COLLISION_NAME=''
  export FAKE_CREATE_STATUS=201 FAKE_CLAIM_STATUS=200 FAKE_INSTALLER_STATUS=200
  export FAKE_LOST_CLAIM_RESPONSE=0
  export FAKE_CLAIM_VERSION=1.2.3 FAKE_CLAIM_ORIGIN="$FAKE_ORIGIN"
  export FAKE_CLAIM_NAMESPACE="$FAKE_NAMESPACE" FAKE_CLAIM_RELEASE="$FAKE_NAMESPACE"
  export FAKE_CLAIM_INSTALLER_SHA256="$FAKE_INSTALLER_SHA256"
  export FAKE_OPENCOST_FIELDS=''
  export FAKE_INSTALLER_FAIL=0 FAKE_DOCKER_FAIL=0 FAKE_NODE_COUNT=1
  export FAKE_LOCAL_BOOT_ID=11111111-1111-4111-8111-111111111111
  export FAKE_NODE_BOOT_ID="$FAKE_LOCAL_BOOT_ID" FAKE_NODE_RUNTIME=docker://24.0.9
  export FAKE_DOCKER_NAME=fixture-node FAKE_DOCKER_KERNEL=fixture-kernel
  export FAKE_DOCKER_REGISTRY='{"IndexConfigs":{},"InsecureRegistryCIDRs":["127.0.0.0/8"]}'
  unset DOCKER_HOST DOCKER_CONTEXT KUBEATLAS_AGENT_HUB_CA_FILE
  export KUBEATLAS_PROFILE=lan KUBEATLAS_ORIGIN="$FAKE_ORIGIN" KUBEATLAS_VERSION=1.2.3
  KUBEATLAS_BOOTSTRAP_SHA256="$(printf 'b%.0s' {1..64})"
  export KUBEATLAS_BOOTSTRAP_SHA256
  : >"$FAKE_ARGV_LOG"
  : >"$FAKE_CURL_URL_LOG"
  : >"$FAKE_CONTEXT_MODES"
  : >"$FAKE_DEVICE_MODES"
  : >"$FAKE_TOKEN_MODES"
  : >"$FAKE_INSTALLER_ARGV_LOG"
  : >"$FAKE_INSTALLER_RUNS"
  : >"$FAKE_MUTATIONS"
  : >"$FAKE_GET_LOG"
  : >"$FAKE_DOCKER_LOG"
}

assert_no_external_downloads() {
  local url
  while IFS= read -r url; do
    [[ "$url" == "$FAKE_ORIGIN/"* ]] || { printf 'unexpected download origin: %s\n' "$url" >&2; exit 1; }
  done <"$FAKE_CURL_URL_LOG"
}

assert_safe_argv() {
  local path
  for path in "$FAKE_ARGV_LOG" "$FAKE_INSTALLER_ARGV_LOG"; do
    if grep -Fq -- "$FAKE_DEVICE_SECRET" "$path" || grep -Fq -- "$FAKE_AGENT_TOKEN" "$path"; then
      printf 'enrollment secret appeared in logged argv: %s\n' "$path" >&2
      exit 1
    fi
  done
}

assert_no_cluster_mutation() {
  local label="$1"
  [[ ! -s "$FAKE_MUTATIONS" ]] || { printf '%s performed a cluster mutation: %s\n' "$label" "$(<"$FAKE_MUTATIONS")" >&2; exit 1; }
}

assert_no_mutation() {
  local label="$1"
  assert_no_cluster_mutation "$label"
  [[ ! -s "$FAKE_INSTALLER_RUNS" ]] || { printf '%s executed the installer\n' "$label" >&2; exit 1; }
  [[ ! -s "$FAKE_INSTALLER_ARGV_LOG" ]] || { printf '%s invoked the installer\n' "$label" >&2; exit 1; }
}

expect_failure() {
  local label="$1" expected="$2" answer="${3:-yes}" output=''
  if output="$(printf '%s\n' "$answer" | env -u KUBECONFIG bash "$script" 2>&1)"; then
    printf '%s unexpectedly succeeded\n' "$label" >&2
    exit 1
  fi
  [[ "$output" == *"$expected"* ]] || { printf '%s lacked %q in output: %s\n' "$label" "$expected" "$output" >&2; exit 1; }
  [[ "$output" != *"$FAKE_DEVICE_SECRET"* && "$output" != *"$FAKE_AGENT_TOKEN"* ]] || { printf '%s printed an enrollment secret\n' "$label" >&2; exit 1; }
  assert_no_mutation "$label"
  assert_safe_argv
  assert_no_external_downloads
}

reset_case
if output="$(printf 'yes\n' | env -u KUBECONFIG bash "$script" 2>&1)"; then
  [[ -s "$FAKE_INSTALLER_RUNS" ]] || { printf 'installer did not run: %s\n' "$output" >&2; exit 1; }
else
  printf 'bootstrap success case failed: %s\n' "$output" >&2
  exit 1
fi
[[ "$(<"$FAKE_INSTALLER_RUNS")" == ran ]] || { printf 'unexpected installer run marker\n' >&2; exit 1; }
grep -Fxq 'test-context:600' "$FAKE_CONTEXT_MODES" || { printf 'installer did not receive the frozen mode-0600 context\n' >&2; exit 1; }
[[ "$(<"$FAKE_DEVICE_MODES")" == 600 && "$(<"$FAKE_TOKEN_MODES")" == 600 ]] || { printf 'device or agent token file mode was not 0600\n' >&2; exit 1; }
for argument in --context test-context --cluster-id "$FAKE_CLUSTER_ID" --namespace "$FAKE_NAMESPACE" --release "$FAKE_NAMESPACE" --token-file; do
  grep -Fxq -- "$argument" "$FAKE_INSTALLER_ARGV_LOG" || { printf 'pinned installer did not receive %s\n' "$argument" >&2; exit 1; }
done
[[ "$output" != *"$FAKE_DEVICE_SECRET"* && "$output" != *"$FAKE_AGENT_TOKEN"* ]] || { printf 'bootstrap printed an enrollment secret\n' >&2; exit 1; }
assert_safe_argv
assert_no_external_downloads
[[ "$(wc -l <"$FAKE_CURL_URL_LOG")" -eq 3 ]] || { printf 'unexpected download count: %s\n' "$(<"$FAKE_CURL_URL_LOG")" >&2; exit 1; }
assert_no_cluster_mutation success

reset_case
export KUBEATLAS_AGENT_HUB_CA_FILE="$tmp/Hub private CA.pem"
printf 'fixture CA\n' >"$KUBEATLAS_AGENT_HUB_CA_FILE"
if ! output="$(printf 'yes\n' | env -u KUBECONFIG bash "$script" 2>&1)"; then
  printf 'bootstrap custom CA case failed: %s\n' "$output" >&2; exit 1
fi
grep -Fxq -- '--ca-file' "$FAKE_INSTALLER_ARGV_LOG"
grep -Fxq -- "$KUBEATLAS_AGENT_HUB_CA_FILE" "$FAKE_INSTALLER_ARGV_LOG"
assert_safe_argv
assert_no_external_downloads

reset_case
export KUBEATLAS_AGENT_HUB_CA_FILE="$tmp/missing-hub-ca.pem"
expect_failure 'missing custom CA' 'KUBEATLAS_AGENT_HUB_CA_FILE must be a readable regular file'
[[ ! -s "$FAKE_CURL_URL_LOG" ]] || { printf 'invalid CA reached the Hub\n' >&2; exit 1; }

reset_case
opencost_chart="$(printf 'c%.0s' {1..64})"
opencost_image="sha256:$(printf 'e%.0s' {1..64})"
export FAKE_OPENCOST_FIELDS=",\"openCostChartSHA256\":\"$opencost_chart\",\"openCostImageDigest\":\"$opencost_image\""
if ! output="$(printf 'yes\n' | env -u KUBECONFIG bash "$script" 2>&1)"; then
  printf 'bootstrap OpenCost case failed: %s\n' "$output" >&2
  exit 1
fi
for argument in --opencost-chart-sha256 "$opencost_chart" --opencost-image-digest "$opencost_image"; do
  grep -Fxq -- "$argument" "$FAKE_INSTALLER_ARGV_LOG" || { printf 'installer did not receive %s\n' "$argument" >&2; exit 1; }
done
assert_no_cluster_mutation 'OpenCost prepare arguments'
assert_safe_argv
assert_no_external_downloads

reset_case
export FAKE_OPENCOST_FIELDS=",\"openCostChartSHA256\":\"$opencost_chart\""
expect_failure 'missing OpenCost image digest' 'OpenCost image digest is invalid'

for permission in 'create namespaces' 'bind clusterroles' 'escalate clusterroles'; do
  reset_case
  export FAKE_DENY_PERMISSION="$permission"
  expect_failure "permission denial ($permission)" "installation privilege preflight failed: $permission"
done

reset_case
export FAKE_CHANGED_CONTEXT=1
expect_failure 'changed context' 'frozen context changed'

reset_case
export FAKE_CONTEXT=unexpected-context
expect_failure 'declined context' 'installation was not confirmed' n

reset_case
export FAKE_NAMESPACE_COLLISION=1
expect_failure 'namespace collision' 'namespace collision:'

reset_case
export FAKE_RELEASE_COLLISION=1
expect_failure 'release collision' 'Helm release collision:'

reset_case
export FAKE_COLLISION_KIND=clusterrolebinding
export FAKE_COLLISION_NAME="$FAKE_NAMESPACE-hub-role-member"
expect_failure 'rendered ClusterRoleBinding collision' "cluster-scoped resource collision: $FAKE_COLLISION_KIND/$FAKE_COLLISION_NAME"
grep -Fxq "$FAKE_COLLISION_KIND/$FAKE_COLLISION_NAME" "$FAKE_GET_LOG" || { printf 'bootstrap did not preflight the rendered resource name\n' >&2; exit 1; }
if grep -Fq "/claim" "$FAKE_CURL_URL_LOG"; then
  printf 'bootstrap claimed enrollment despite the rendered resource collision\n' >&2
  exit 1
fi

reset_case
export FAKE_CREATE_STATUS=503
expect_failure 'enrollment creation unavailable' 'Hub did not accept enrollment request (HTTP 503)'

reset_case
export FAKE_CLAIM_STATUS=429
expect_failure 'claim rate limited' 'Hub enrollment rate limit reached (HTTP 429)'

reset_case
export FAKE_LOST_CLAIM_RESPONSE=1
expect_failure 'claim transport failure' 'lost contact with the Hub during claim'
[[ "$(grep -c '/claim$' "$FAKE_CURL_URL_LOG")" -eq 1 ]] || { printf 'ambiguous claim was retried\n' >&2; exit 1; }

reset_case
export FAKE_CLAIM_ORIGIN=https://other.example.test
expect_failure 'claim origin mismatch' 'claim origin mismatch'

reset_case
export FAKE_CLAIM_VERSION=9.9.9
expect_failure 'claim version mismatch' 'claim version does not match the pinned bootstrap version'

reset_case
export FAKE_CLAIM_NAMESPACE=attacker-namespace
expect_failure 'claim target mismatch' 'claim installation target differs from preflight'

reset_case
export FAKE_INSTALLER_STATUS=302
expect_failure 'installer redirect' 'installer endpoint returned non-200'

reset_case
FAKE_CLAIM_INSTALLER_SHA256="$(printf '0%.0s' {1..64})"
export FAKE_CLAIM_INSTALLER_SHA256
expect_failure 'installer checksum mismatch' 'installer checksum mismatch'

use_http_origin() {
  export FAKE_ORIGIN=http://192.0.2.220:8080 KUBEATLAS_ORIGIN=http://192.0.2.220:8080 FAKE_CLAIM_ORIGIN=http://192.0.2.220:8080
}

expect_bootstrap_success() {
  local label="$1" expected="${2:-}"
  local result
  if ! result="$(printf 'yes\n' | env -u KUBECONFIG bash "$script" 2>&1)"; then printf '%s failed: %s\n' "$label" "$result" >&2; exit 1; fi
  [[ -z "$expected" || "$result" == *"$expected"* ]] || { printf '%s lacked expected guidance: %s\n' "$label" "$result" >&2; exit 1; }
  [[ "$(<"$FAKE_INSTALLER_RUNS")" == ran ]] || { printf '%s did not invoke installer once\n' "$label" >&2; exit 1; }
  [[ "$result" != *"$FAKE_DEVICE_SECRET"* && "$result" != *"$FAKE_AGENT_TOKEN"* ]] || { printf '%s leaked a secret\n' "$label" >&2; exit 1; }
  assert_no_cluster_mutation "$label"
  assert_safe_argv
  assert_no_external_downloads
}

reset_case
use_http_origin
expect_failure 'local HTTP Docker registry missing' 'HTTP registry preflight blocked'
[[ ! -s "$FAKE_CURL_URL_LOG" ]] || { printf 'runtime failure created an enrollment or claimed a token\n' >&2; exit 1; }
grep -Fq -- '--host unix:///var/run/docker.sock info' "$FAKE_DOCKER_LOG"

reset_case
use_http_origin
export FAKE_DOCKER_REGISTRY='{"IndexConfigs":{"192.0.2.220:8080":{"Secure":false}},"InsecureRegistryCIDRs":["127.0.0.0/8"]}'
export DOCKER_HOST=ssh://unrelated-daemon DOCKER_CONTEXT=unrelated-context
expect_bootstrap_success 'exact HTTP registry with remote client overrides'

reset_case
use_http_origin
export FAKE_DOCKER_REGISTRY='{"IndexConfigs":{},"InsecureRegistryCIDRs":["192.0.2.0/24"]}'
expect_bootstrap_success 'HTTP registry allowed by CIDR'

for wrong_authority in 192.0.2.220:5000 192.0.2.220:8081; do
  reset_case
  use_http_origin
  export FAKE_DOCKER_REGISTRY="{\"IndexConfigs\":{\"$wrong_authority\":{\"Secure\":false}},\"InsecureRegistryCIDRs\":[\"127.0.0.0/8\"]}"
  expect_failure 'different HTTP registry port' 'HTTP registry preflight blocked'
  [[ ! -s "$FAKE_CURL_URL_LOG" ]] || { printf 'wrong registry match reached enrollment\n' >&2; exit 1; }
done

reset_case
use_http_origin
export FAKE_NODE_BOOT_ID=22222222-2222-4222-8222-222222222222
expect_bootstrap_success 'remote Kubernetes node' 'not this booted host'
[[ ! -s "$FAKE_DOCKER_LOG" ]] || { printf 'remote-node preflight inspected client Docker\n' >&2; exit 1; }

reset_case
use_http_origin
export FAKE_NODE_COUNT=2
expect_bootstrap_success 'multi-node Kubernetes cluster' 'not a verified single-node cluster'
[[ ! -s "$FAKE_DOCKER_LOG" ]] || { printf 'multi-node preflight inspected only client Docker\n' >&2; exit 1; }

reset_case
use_http_origin
export FAKE_NODE_RUNTIME=containerd://1.6.39
expect_bootstrap_success 'non-Docker runtime' 'does not report Docker'
[[ ! -s "$FAKE_DOCKER_LOG" ]] || { printf 'containerd preflight inspected Docker\n' >&2; exit 1; }

reset_case
use_http_origin
export FAKE_DOCKER_NAME=another-host
expect_bootstrap_success 'different local Docker daemon' 'does not match the Kubernetes node'

reset_case
use_http_origin
export FAKE_DOCKER_FAIL=1
expect_bootstrap_success 'Docker policy unavailable' 'unavailable or access is denied'

reset_case
expect_bootstrap_success 'HTTPS skips HTTP registry probing'
[[ ! -s "$FAKE_DOCKER_LOG" ]] || { printf 'HTTPS unexpectedly inspected Docker\n' >&2; exit 1; }

reset_case
export FAKE_INSTALLER_FAIL=1
if result="$(printf 'yes\n' | env -u KUBECONFIG bash "$script" 2>&1)"; then printf 'installer failure unexpectedly passed bootstrap\n' >&2; exit 1; fi
[[ "$result" == *'claim state=claimed'* && "$result" == *"cluster=$FAKE_CLUSTER_ID"* && "$result" == *"namespace=$FAKE_NAMESPACE"* && "$result" == *'one-time claim cannot be replayed'* ]] || { printf 'post-claim failure lacked recovery target: %s\n' "$result" >&2; exit 1; }
[[ "$result" != *"$FAKE_DEVICE_SECRET"* && "$result" != *"$FAKE_AGENT_TOKEN"* ]] || { printf 'post-claim failure leaked credentials\n' >&2; exit 1; }
[[ "$(<"$FAKE_INSTALLER_RUNS")" == ran ]] || { printf 'failed installer was invoked more than once\n' >&2; exit 1; }
[[ "$(grep -c '/claim$' "$FAKE_CURL_URL_LOG")" == 1 ]] || { printf 'approved claim was retried\n' >&2; exit 1; }
assert_safe_argv
assert_no_cluster_mutation 'failed fixture installer'

printf 'Hub Agent bootstrap shell tests passed (frozen context, registry identity/trust, no premature enrollment, secret files, authorization, collisions, claim recovery, redirects, checksums).\n'
