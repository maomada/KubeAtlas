#!/usr/bin/env bash
set -Eeuo pipefail
script="$(cd "$(dirname "$0")" && pwd)/bootstrap-hub-agent.sh"
tmp="$(mktemp -d)"
trap 'rm -rf -- "$tmp"' EXIT
bash -n "$script"

export FAKE_ORIGIN=https://hub.example.test
export FAKE_ENROLLMENT_ID=0123456789abcdef0123456789abcdef
export FAKE_DEVICE_SECRET="ka_device_$(printf 'd%.0s' {1..64})"
export FAKE_AGENT_TOKEN="ka_agent_$(printf 't%.0s' {1..43})"
export FAKE_CLUSTER_ID=agent-cluster
export FAKE_NAMESPACE="kubeatlas-agent-${FAKE_ENROLLMENT_ID:0:10}"
export FAKE_OWNER_USER_ID="kubeatlas:portal:$(printf 'c%.0s' {1..64})"
export FAKE_IMAGE_DIGEST="sha256:$(printf 'a%.0s' {1..64})"
FAKE_CHART_SHA256="$(printf 'chart fixture' | sha256sum | awk '{print $1}')"
export FAKE_CHART_SHA256
FAKE_INSTALLER_BODY="$(printf '%s\n' \
  '#!/usr/bin/env bash' \
  'set -Eeuo pipefail' \
  'printf "%s\n" "$@" >"$FAKE_INSTALLER_ARGV_LOG"' \
  'context="" token_file=""' \
  'while (($#)); do case "$1" in --context) context="$2"; shift 2;; --token-file) token_file="$2"; shift 2;; *) shift;; esac; done' \
  '[[ "$context" == "$FAKE_CONTEXT" ]]' \
  '[[ -f "$KUBECONFIG" && "$(stat -c %a "$KUBECONFIG")" == 600 ]]' \
  '[[ "$(kubectl config current-context)" == "$FAKE_CONTEXT" ]]' \
  '[[ -f "$token_file" && "$(stat -c %a "$token_file")" == 600 ]]' \
  '[[ "$(<"$token_file")" == "$FAKE_AGENT_TOKEN" ]]' \
  'printf "%s\n" "600" >>"$FAKE_TOKEN_MODES"' \
  'printf "%s\n" ran >>"$FAKE_INSTALLER_RUNS"')"
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

curl() {
  printf 'curl' >>"$FAKE_ARGV_LOG"
  printf ' %q' "$@" >>"$FAKE_ARGV_LOG"
  printf '\n' >>"$FAKE_ARGV_LOG"
  local output='' config='' url='' status=200
  while (($#)); do
    case "$1" in
      --output) output="$2"; shift 2;;
      --config) config="$2"; shift 2;;
      --silent|--show-error) shift;;
      --proto|--max-redirs|--write-out|--header|--data-binary|--request) shift 2;;
      *) url="$1"; shift;;
    esac
  done
  printf '%s\n' "$url" >>"$FAKE_CURL_URL_LOG"
  [[ "$url" == "$FAKE_ORIGIN/"* ]] || { printf 'unexpected external URL: %s\n' "$url" >&2; return 91; }
  case "$url" in
    "$FAKE_ORIGIN/api/hub/enrollments")
      printf '{"id":"%s","deviceSecret":"%s","userCode":"ABCD-EFGH"}\n' \
        "$FAKE_ENROLLMENT_ID" "$FAKE_DEVICE_SECRET" >"$output"
      status="$FAKE_CREATE_STATUS"
      ;;
    "$FAKE_ORIGIN/api/hub/enrollments/$FAKE_ENROLLMENT_ID/claim")
      [[ -n "$config" && "$(stat -c %a "$config")" == 600 ]] || { printf 'device config is not protected\n' >&2; return 92; }
      grep -Fq -- "$FAKE_DEVICE_SECRET" "$config" || { printf 'device config does not contain expected claim credential\n' >&2; return 93; }
      printf '%s\n' "$(stat -c %a "$config")" >>"$FAKE_DEVICE_MODES"
      printf '{"status":"approved","clusterId":"%s","ownerUserId":"%s","version":"%s","imageDigest":"%s","chartSHA256":"%s","installerSHA256":"%s","hubOrigin":"%s","namespace":"%s","release":"%s","agentToken":"%s"}\n' \
        "$FAKE_CLUSTER_ID" "$FAKE_OWNER_USER_ID" "$FAKE_CLAIM_VERSION" "$FAKE_IMAGE_DIGEST" \
        "$FAKE_CHART_SHA256" "$FAKE_CLAIM_INSTALLER_SHA256" "$FAKE_CLAIM_ORIGIN" \
        "$FAKE_CLAIM_NAMESPACE" "$FAKE_CLAIM_RELEASE" "$FAKE_AGENT_TOKEN" >"$output"
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
  printf 'kubectl' >>"$FAKE_ARGV_LOG"
  printf ' %q' "$@" >>"$FAKE_ARGV_LOG"
  printf '\n' >>"$FAKE_ARGV_LOG"
  if [[ "${1:-}" == --context ]]; then shift 2; fi
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
      if [[ "${2:-}" == namespace && "${3:-}" == kube-system ]]; then
        printf '%s\n' fake-kube-system-uid
      elif [[ "${2:-}" == namespace && "$FAKE_NAMESPACE_COLLISION" == 1 ]]; then
        printf 'namespace/%s\n' "$3"
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
  printf 'helm' >>"$FAKE_ARGV_LOG"
  printf ' %q' "$@" >>"$FAKE_ARGV_LOG"
  printf '\n' >>"$FAKE_ARGV_LOG"
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
export -f curl kubectl helm

reset_case() {
  export FAKE_CONTEXT=test-context FAKE_CHANGED_CONTEXT=0 FAKE_DENY_PERMISSION=''
  export FAKE_NAMESPACE_COLLISION=0 FAKE_RELEASE_COLLISION=0
  export FAKE_CREATE_STATUS=201 FAKE_CLAIM_STATUS=200 FAKE_INSTALLER_STATUS=200
  export FAKE_CLAIM_VERSION=1.2.3 FAKE_CLAIM_ORIGIN="$FAKE_ORIGIN"
  export FAKE_CLAIM_NAMESPACE="$FAKE_NAMESPACE" FAKE_CLAIM_RELEASE="$FAKE_NAMESPACE"
  export FAKE_CLAIM_INSTALLER_SHA256="$FAKE_INSTALLER_SHA256"
  export KUBEATLAS_PROFILE=lan KUBEATLAS_ORIGIN="$FAKE_ORIGIN" KUBEATLAS_VERSION=1.2.3
  export KUBEATLAS_BOOTSTRAP_SHA256="$(printf 'b%.0s' {1..64})"
  : >"$FAKE_ARGV_LOG"
  : >"$FAKE_CURL_URL_LOG"
  : >"$FAKE_CONTEXT_MODES"
  : >"$FAKE_DEVICE_MODES"
  : >"$FAKE_TOKEN_MODES"
  : >"$FAKE_INSTALLER_ARGV_LOG"
  : >"$FAKE_INSTALLER_RUNS"
  : >"$FAKE_MUTATIONS"
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
export FAKE_CLAIM_INSTALLER_SHA256="$(printf '0%.0s' {1..64})"
expect_failure 'installer checksum mismatch' 'installer checksum mismatch'

printf 'Hub Agent bootstrap shell tests passed (frozen context, secret files, authorization, collisions, claim binding, redirects, checksums).\n'
