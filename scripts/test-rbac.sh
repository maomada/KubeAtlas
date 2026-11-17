#!/usr/bin/env bash
#
# Live end-to-end RBAC test for KubeAtlas auth-enabled mode.
#
# Requires an explicitly selected disposable cluster. Namespaces, fixture
# ClusterRoles and ClusterRoleBindings are run-unique; the CRD is create-only
# and a collision is refused. Cleanup uses only the durable UID ledger.
#
# WARNING: never run against a shared or production cluster.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
PORT="${PORT:-9389}"
AUTH_SECRET="rbac-test-secret-do-not-use-in-prod-1234567890"
KUBEATLAS_BIN="${KUBEATLAS_BIN:-./kubeatlas}"
SKIP_BUILD=false
ASSUME_YES=false
SHOW_HELP=false

for arg in "$@"; do
  case "$arg" in
    --skip-build) SKIP_BUILD=true ;;
    --yes|-y) ASSUME_YES=true ;;
    --help|-h) SHOW_HELP=true ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done
if [[ "$SHOW_HELP" == true ]]; then
  sed -n '2,/^$/p' "$0" | sed 's/^# \?//'
  printf 'Usage: %s [--skip-build] [--yes|-y]\n' "$0"
  printf 'Set KUBEATLAS_TEST_CONTEXT explicitly; NS_ALICE and NS_BOB may override the run-unique defaults.\n'
  exit 0
fi

CTX="${KUBEATLAS_TEST_CONTEXT:-}"
[[ -n "$CTX" ]] || {
  echo "KUBEATLAS_TEST_CONTEXT is required; the current context is never selected implicitly." >&2
  exit 2
}

for bin in kubectl jq curl python3 sha256sum git; do
  command -v "$bin" >/dev/null 2>&1 || { echo "missing required dependency: $bin" >&2; exit 1; }
done
if [[ "$SKIP_BUILD" != true ]]; then
  command -v make >/dev/null 2>&1 || { echo "missing required dependency: make" >&2; exit 1; }
  echo "Building KubeAtlas..."
  make build >/dev/null
fi
[[ -x "$KUBEATLAS_BIN" ]] || {
  echo "KubeAtlas binary not found or not executable at $KUBEATLAS_BIN." >&2
  exit 1
}

SOURCE_SHA="$(git rev-parse HEAD)"
if [[ -z "$(git status --porcelain)" ]]; then
  SOURCE_STATE=clean
else
  SOURCE_STATE=dirty
fi
if [[ "$SKIP_BUILD" == true ]]; then
  BINARY_PROVENANCE="unknown (--skip-build)"
else
  BINARY_DIR="${KUBEATLAS_BIN%/*}"
  if [[ "$BINARY_DIR" == "$KUBEATLAS_BIN" ]]; then
    BINARY_DIR="."
  elif [[ -z "$BINARY_DIR" ]]; then
    BINARY_DIR="/"
  fi
  BINARY_PATH="$(cd -- "$BINARY_DIR" && pwd -P)/${KUBEATLAS_BIN##*/}"
  DEFAULT_BINARY_PATH="$(pwd -P)/kubeatlas"
  if [[ "$BINARY_PATH" == "$DEFAULT_BINARY_PATH" ]]; then
    BINARY_PROVENANCE="make build from checkout $SOURCE_SHA ($SOURCE_STATE)"
  else
    BINARY_PROVENANCE="unknown (custom KUBEATLAS_BIN override)"
  fi
fi
read -r BINARY_SHA _ < <(sha256sum "$KUBEATLAS_BIN")
if ! BINARY_VERSION="$("$KUBEATLAS_BIN" --version 2>&1)"; then
  echo "KubeAtlas binary --version preflight failed." >&2
  exit 1
fi
if ! python3 - "$PORT" <<'PY'
import socket
import sys

sock = socket.socket()
try:
    sock.bind(("127.0.0.1", int(sys.argv[1])))
except OSError:
    raise SystemExit(1)
finally:
    sock.close()
PY
then
  echo "Port $PORT is already in use; refusing to start KubeAtlas." >&2
  exit 1
fi

RUN_ID="$(python3 -c 'import uuid; print(uuid.uuid4().hex[:12])')"
RUN_OWNER="rbac-$RUN_ID"
NS_ALICE="${NS_ALICE:-kubeatlas-rbac-alice-$RUN_ID}"
NS_BOB="${NS_BOB:-kubeatlas-rbac-bob-$RUN_ID}"
[[ "$NS_ALICE" != "$NS_BOB" ]] || { echo "NS_ALICE and NS_BOB must differ." >&2; exit 2; }
CR_PODS="kubeatlas-rbac-$RUN_ID-pods-only"
CR_NODES="kubeatlas-rbac-$RUN_ID-nodes-only"
CRB_CAROL="kubeatlas-rbac-$RUN_ID-carol"
CRB_ERIN="kubeatlas-rbac-$RUN_ID-erin"
CRD_NAME="kubeatlastests.test.skyhook.io"
RUN_DIR=""
LEDGER=""
FROZEN_KUBECONFIG=""
KUBEATLAS_PID=""
MUTATIONS_STARTED=false
FAILS=0

G='\033[0;32m'; R='\033[0;31m'; Y='\033[1;33m'; B='\033[1;34m'; N='\033[0m'
ok()   { echo -e "${G}✓${N} $1"; }
fail() { echo -e "${R}✗${N} $1"; FAILS=$((FAILS+1)); }
info() { echo -e "${B}→${N} $1"; }
warn() { echo -e "${Y}⚠${N} $1"; }

echo
echo -e "${Y}This will create disposable RBAC fixtures in context: ${B}${CTX}${N}"
if [[ "$ASSUME_YES" != true ]]; then
  if ! IFS= read -r -p "Continue? [y/N] " ans; then
    echo "aborted (no confirmation)" >&2
    exit 1
  fi
  [[ "$ans" =~ ^[Yy]$ ]] || { echo "aborted"; exit 1; }
fi

umask 077
EVIDENCE_ROOT="${KUBEATLAS_RBAC_EVIDENCE_DIR:-${HOME}/.kubeatlas/test-runs}"
if ! python3 - "$EVIDENCE_ROOT" "$SCRIPT_DIR/.." <<'PY'
import sys
from pathlib import Path

evidence = Path(sys.argv[1]).resolve()
repository = Path(sys.argv[2]).resolve()
if evidence == repository or repository in evidence.parents:
    raise SystemExit(1)
PY
then
  echo "RBAC evidence directory must be outside the repository." >&2
  exit 1
fi
mkdir -p -- "$EVIDENCE_ROOT"
RUN_DIR="$(mktemp -d "$EVIDENCE_ROOT/kubeatlas-rbac-${RUN_ID}.XXXXXX")"
chmod 700 "$RUN_DIR"
LEDGER="$RUN_DIR/ledger.jsonl"
: > "$LEDGER"
chmod 600 "$LEDGER"
FROZEN_KUBECONFIG="$RUN_DIR/kubeconfig"
remove_private_kubeconfig() {
  [[ -z "$FROZEN_KUBECONFIG" ]] || rm -f -- "$FROZEN_KUBECONFIG"
}
trap remove_private_kubeconfig EXIT

if ! kubectl --context "$CTX" config view --raw --flatten --minify > "$FROZEN_KUBECONFIG" 2>/dev/null; then
  echo "Unable to snapshot the selected kubeconfig; no cluster resources were changed." >&2
  exit 1
fi
chmod 600 "$FROZEN_KUBECONFIG"
kubectl --kubeconfig "$FROZEN_KUBECONFIG" --context "$CTX" config set current-context "$CTX" >/dev/null 2>&1
FROZEN_CONTEXT="$(kubectl --kubeconfig "$FROZEN_KUBECONFIG" --context "$CTX" config current-context 2>/dev/null)"
[[ "$FROZEN_CONTEXT" == "$CTX" ]] || {
  echo "Frozen kubeconfig does not select KUBEATLAS_TEST_CONTEXT; no resources were changed." >&2
  exit 1
}

kubectl_safe() {
  kubectl --kubeconfig "$FROZEN_KUBECONFIG" --context "$CTX" "$@"
}
for namespace in "$NS_ALICE" "$NS_BOB"; do
  if ! existing="$(kubectl_safe get namespace "$namespace" --ignore-not-found -o name 2>/dev/null)"; then
    echo "Unable to check namespace collision for $namespace; no resources were changed." >&2
    exit 1
  fi
  [[ -z "$existing" ]] || {
    echo "Refusing to adopt existing namespace $namespace; no resources were changed." >&2
    exit 1
  }
done
if ! existing_crd="$(kubectl_safe get crd "$CRD_NAME" --ignore-not-found -o name 2>/dev/null)"; then
  echo "Unable to check CRD collision; no resources were changed." >&2
  exit 1
fi
[[ -z "$existing_crd" ]] || {
  echo "Refusing to adopt existing CRD $CRD_NAME; no resources were changed." >&2
  exit 1
}

cleanup() {
  local original_status=$?
  local cleanup_status=0
  trap - EXIT
  set +e
  if [[ -n "$KUBEATLAS_PID" ]] && kill -0 "$KUBEATLAS_PID" 2>/dev/null; then
    info "stopping KubeAtlas (pid $KUBEATLAS_PID)"
    kill "$KUBEATLAS_PID" 2>/dev/null || cleanup_status=1
    wait "$KUBEATLAS_PID" 2>/dev/null
    wait_status=$?
    [[ "$wait_status" -eq 0 || "$wait_status" -eq 143 ]] || cleanup_status=1
  fi
  if [[ "$MUTATIONS_STARTED" == true ]]; then
    info "cleaning only UID-ledgered run fixtures"
    python3 "$SCRIPT_DIR/engineering_rbac.py" cleanup \
      --ledger "$LEDGER" --kubeconfig "$FROZEN_KUBECONFIG" \
      --context "$CTX" --owner "$RUN_OWNER" || cleanup_status=1
  fi
  remove_private_kubeconfig
  if [[ "$cleanup_status" -ne 0 ]]; then
    echo "Cleanup failed or residue is uncertain; review ledger: $LEDGER" >&2
    [[ "$original_status" -ne 0 ]] || original_status=1
  fi
  echo "RBAC evidence directory retained: $RUN_DIR"
  exit "$original_status"
}
trap cleanup EXIT

create_owned() {
  python3 "$SCRIPT_DIR/engineering_rbac.py" create \
    --ledger "$LEDGER" --kubeconfig "$FROZEN_KUBECONFIG" \
    --context "$CTX" --owner "$RUN_OWNER"
}

info "Source checkout: $SOURCE_SHA ($SOURCE_STATE)"
info "Binary provenance: $BINARY_PROVENANCE"
info "Binary SHA-256: $BINARY_SHA"
info "Binary version: $BINARY_VERSION"
info "Frozen context: $CTX"
info "Run ledger: $LEDGER"

MUTATIONS_STARTED=true
info "creating run-unique namespaces, pods and Secret"
create_owned <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NS_ALICE
EOF
create_owned <<EOF
apiVersion: v1
kind: Namespace
metadata:
  name: $NS_BOB
EOF
create_owned <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: rbac-test-pod-a
  namespace: $NS_ALICE
spec:
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.9
    imagePullPolicy: IfNotPresent
  restartPolicy: Never
EOF
create_owned <<EOF
apiVersion: v1
kind: Pod
metadata:
  name: rbac-test-pod-b
  namespace: $NS_BOB
spec:
  containers:
  - name: pause
    image: registry.k8s.io/pause:3.9
    imagePullPolicy: IfNotPresent
  restartPolicy: Never
EOF
create_owned <<EOF
apiVersion: v1
kind: Secret
metadata:
  name: rbac-test-secret-a
  namespace: $NS_ALICE
type: Opaque
stringData:
  token: ignored
EOF

info "installing test CRD (cluster-scoped, not in static cluster-only list)"
create_owned <<'EOF'
apiVersion: apiextensions.k8s.io/v1
kind: CustomResourceDefinition
metadata:
  name: kubeatlastests.test.skyhook.io
spec:
  group: test.skyhook.io
  names:
    kind: KubeAtlasTest
    plural: kubeatlastests
    singular: kubeatlastest
  scope: Cluster
  versions:
  - name: v1
    served: true
    storage: true
    schema:
      openAPIV3Schema:
        type: object
        properties:
          spec:
            type: object
            x-kubernetes-preserve-unknown-fields: true
EOF
kubectl_safe wait --for=condition=Established "crd/$CRD_NAME" --timeout=10s >/dev/null
create_owned <<'EOF'
apiVersion: test.skyhook.io/v1
kind: KubeAtlasTest
metadata:
  name: rbac-test-cluster-scoped-1
EOF
create_owned <<'EOF'
apiVersion: test.skyhook.io/v1
kind: KubeAtlasTest
metadata:
  name: rbac-test-cluster-scoped-2
EOF

info "creating run-scoped ClusterRoles and bindings"
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: $CR_PODS
rules:
- apiGroups: [""]
  resources: ["pods", "namespaces"]
  verbs: ["get", "list", "watch"]
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: $CRB_CAROL
subjects:
- kind: User
  name: carol
roleRef:
  kind: ClusterRole
  name: $CR_PODS
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: $CR_NODES
rules:
- apiGroups: [""]
  resources: ["nodes"]
  verbs: ["get", "list", "watch"]
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: $CRB_ERIN
subjects:
- kind: User
  name: erin
roleRef:
  kind: ClusterRole
  name: $CR_NODES
  apiGroup: rbac.authorization.k8s.io
EOF

info "creating namespaced RoleBindings and Secret reader fixtures"
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-alice
  namespace: $NS_ALICE
subjects:
- kind: User
  name: alice
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-bob
  namespace: $NS_BOB
subjects:
- kind: User
  name: bob
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-dave-a
  namespace: $NS_ALICE
subjects:
- kind: User
  name: dave
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-dave-b
  namespace: $NS_BOB
subjects:
- kind: User
  name: dave
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: Role
metadata:
  name: kubeatlas-rbac-test-secret-reader
  namespace: $NS_ALICE
rules:
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-frank
  namespace: $NS_ALICE
subjects:
- kind: User
  name: frank
roleRef:
  kind: Role
  name: kubeatlas-rbac-test-secret-reader
  apiGroup: rbac.authorization.k8s.io
EOF
create_owned <<EOF
apiVersion: rbac.authorization.k8s.io/v1
kind: RoleBinding
metadata:
  name: kubeatlas-test-frank-view
  namespace: $NS_ALICE
subjects:
- kind: User
  name: frank
roleRef:
  kind: ClusterRole
  name: view
  apiGroup: rbac.authorization.k8s.io
EOF

info "starting KubeAtlas on port $PORT (auth-mode=proxy)"
KUBECONFIG="$FROZEN_KUBECONFIG" nohup "$KUBEATLAS_BIN" \
  --port "$PORT" --kubeconfig "$FROZEN_KUBECONFIG" \
  --auth-mode=proxy --auth-secret="$AUTH_SECRET" --no-browser \
  > "$RUN_DIR/kubeatlas.log" 2>&1 &
KUBEATLAS_PID=$!
LOGFILE="$RUN_DIR/kubeatlas.log"

READY=false
for _ in $(seq 1 30); do
  if ! kill -0 "$KUBEATLAS_PID" 2>/dev/null; then
    echo "KubeAtlas exited early; see $LOGFILE" >&2
    exit 1
  fi
  if curl -fsS --max-time 2 "http://localhost:$PORT/api/health" >/dev/null 2>&1; then
    if ! kill -0 "$KUBEATLAS_PID" 2>/dev/null; then
      echo "KubeAtlas exited during readiness; see $LOGFILE" >&2
      exit 1
    fi
    READY=true
    break
  fi
  sleep 1
done
[[ "$READY" == true ]] || { echo "KubeAtlas readiness timed out; see $LOGFILE" >&2; exit 1; }
ok "KubeAtlas ready at http://localhost:$PORT"

state=""
CONNECTED=false
for _ in $(seq 1 30); do
  if ! kill -0 "$KUBEATLAS_PID" 2>/dev/null; then
    echo "KubeAtlas exited before connecting; see $LOGFILE" >&2
    exit 1
  fi
  if state="$(curl -fsS --max-time 2 "http://localhost:$PORT/api/connection" | jq -r '.state // empty' 2>/dev/null)"; then
    if [[ "$state" == "connected" ]]; then
      CONNECTED=true
      break
    fi
  else
    state=""
  fi
  sleep 1
done
[[ "$CONNECTED" == true ]] || { echo "KubeAtlas did not connect to cluster: state=$state; see $LOGFILE" >&2; exit 1; }
ok "KubeAtlas connected to the frozen cluster context"

# --- Helpers ---
as_user() {
  # as_user <user> <method> <path> [data]
  local user=$1 method=$2 path=$3 data=${4:-}
  local args=(-sf -X "$method" -H "X-Forwarded-User: $user")
  if [[ -n "$data" ]]; then
    args+=(-H 'Content-Type: application/json' -d "$data")
  fi
  curl "${args[@]}" "http://localhost:$PORT$path"
}

mcp_response() {
  local user=$1 tool=$2 json_args=$3
  local body
  body=$(jq -n --arg t "$tool" --argjson a "$json_args" \
    '{jsonrpc:"2.0",id:1,method:"tools/call",params:{name:$t,arguments:$a}}')
  curl -s -X POST -H "X-Forwarded-User: $user" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json, text/event-stream' \
    -d "$body" \
    "http://localhost:$PORT/mcp" \
  | sed -n 's/^data: //p'
}

mcp_call() {
  mcp_response "$@" | jq -r '.result.content[0].text'
}

expect_mcp_denied() {
  local desc=$1 user=$2 tool=$3 json_args=$4
  if mcp_response "$user" "$tool" "$json_args" | jq -e '.result.isError == true and .result.structuredContent.error_code == "user_forbidden" and (.result.content[0].text | fromjson | .error_code == "user_forbidden")' >/dev/null; then
    ok "$desc"
  else
    fail "$desc — expected explicit RBAC error"
  fi
}

expect_rest_forbidden() {
  local desc=$1 user=$2 path=$3 status
  status=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Forwarded-User: $user" "http://localhost:$PORT$path")
  if [[ "$status" == "403" ]]; then ok "$desc"; else fail "$desc — expected 403, got $status"; fi
}

# arr_len reads a JSON array on stdin and prints its length, or "ERR" if
# the input isn't a valid JSON array. Replaces the older `jq '. | length'
# || echo 0` idiom whose `0` fallback collapsed transport / parse errors
# into the same value as a successful "no leak" assertion, masking real
# regressions in the leak-detection tests.
arr_len() {
  jq -r 'if type == "array" then length else "ERR" end' 2>/dev/null || echo "ERR"
}

# expect_mcp_code <description> <mcp_call text> <code> — passes when the tool
# answered with a read error carrying that error_code.
expect_mcp_code() {
  local desc=$1 text=$2 want=$3
  if echo "$text" | jq -e --arg c "$want" '.error_code == $c' >/dev/null 2>&1; then
    ok "$desc"
  else
    fail "$desc — expected error_code $want, got: $text"
  fi
}

# expect_zero <description> <count> — passes when count is "0", fails on
# anything else (including "ERR" from arr_len). Use for "no leak" tests.
expect_zero() {
  local desc=$1 actual=$2
  case "$actual" in
    0)   ok "$desc" ;;
    ERR) fail "$desc — invalid response (transport/parse error)" ;;
    *)   fail "$desc — expected 0, got $actual" ;;
  esac
}

# expect_at_least_one <description> <count> — passes when count is a
# positive integer, fails on "0" or "ERR". Use for "should see ≥1" tests.
expect_at_least_one() {
  local desc=$1 actual=$2
  case "$actual" in
    ERR) fail "$desc — invalid response (transport/parse error)" ;;
    0)   fail "$desc — got 0" ;;
    *)   ok "$desc ($actual)" ;;
  esac
}

# Wait for namespace listings to settle in the cache
sleep 2

echo
echo -e "${B}=== REST: per-user namespace listing ===${N}"

ALICE_NS=$(as_user alice GET /api/cluster/namespace-scope | jq -r '.accessibleNamespaces | sort | join(",")')
if [[ "$ALICE_NS" == "$NS_ALICE" ]]; then
  ok "alice sees only $NS_ALICE: [$ALICE_NS]"
else
  fail "alice should see [$NS_ALICE], got [$ALICE_NS]"
fi

BOB_NS=$(as_user bob GET /api/cluster/namespace-scope | jq -r '.accessibleNamespaces | sort | join(",")')
if [[ "$BOB_NS" == "$NS_BOB" ]]; then
  ok "bob sees only $NS_BOB: [$BOB_NS]"
else
  fail "bob should see [$NS_BOB], got [$BOB_NS]"
fi

echo
echo -e "${B}=== REST: pod listing is filtered ===${N}"

ALICE_PODS=$(as_user alice GET /api/resources/pods | jq -r '.items[].metadata.name // empty' | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$ALICE_PODS" == "rbac-test-pod-a" ]]; then
  ok "alice sees only rbac-test-pod-a"
else
  fail "alice pod list = [$ALICE_PODS], expected [rbac-test-pod-a]"
fi

BOB_PODS=$(as_user bob GET /api/resources/pods | jq -r '.items[].metadata.name // empty' | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$BOB_PODS" == "rbac-test-pod-b" ]]; then
  ok "bob sees only rbac-test-pod-b"
else
  fail "bob pod list = [$BOB_PODS], expected [rbac-test-pod-b]"
fi

echo
echo -e "${B}=== REST: cluster-only kinds hidden from restricted users ===${N}"

expect_rest_forbidden "alice cannot list Nodes" alice /api/resources/nodes

echo
echo -e "${B}=== MCP: list_resources filters per-user ===${N}"

ALICE_MCP_PODS=$(mcp_call alice list_resources '{"kind":"pods"}' | jq -r '.items[].name // empty' 2>/dev/null | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$ALICE_MCP_PODS" == "rbac-test-pod-a" ]]; then
  ok "MCP alice list_resources kind=pods returns only rbac-test-pod-a"
else
  fail "MCP alice pods = [$ALICE_MCP_PODS], expected [rbac-test-pod-a]"
fi

BOB_MCP_PODS=$(mcp_call bob list_resources '{"kind":"pods"}' | jq -r '.items[].name // empty' 2>/dev/null | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$BOB_MCP_PODS" == "rbac-test-pod-b" ]]; then
  ok "MCP bob list_resources kind=pods returns only rbac-test-pod-b"
else
  fail "MCP bob pods = [$BOB_MCP_PODS], expected [rbac-test-pod-b]"
fi

echo
echo -e "${B}=== MCP: cluster-only kinds hidden ===${N}"

expect_mcp_code "MCP alice list_resources kind=nodes is her own denial (cluster-only)" "$(mcp_call alice list_resources '{"kind":"nodes"}')" user_forbidden

echo
echo -e "${B}=== MCP: list_namespaces filters ===${N}"

ALICE_MCP_NS=$(mcp_call alice list_namespaces '{}' | jq -r '.[].name // empty' 2>/dev/null | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$ALICE_MCP_NS" == "$NS_ALICE" ]]; then
  ok "MCP alice list_namespaces returns only $NS_ALICE"
else
  fail "MCP alice list_namespaces = [$ALICE_MCP_NS], expected [$NS_ALICE]"
fi

echo
echo -e "${B}=== Picker is per-user, doesn't affect other users ===${N}"

# alice picks her allowed namespace
as_user alice POST /api/cluster/namespace "{\"namespaces\":[\"$NS_ALICE\"]}" >/dev/null
ALICE_AFTER=$(as_user alice GET /api/cluster/namespace-scope | jq -r '.actives | join(",")')
BOB_AFTER=$(as_user bob   GET /api/cluster/namespace-scope | jq -r '.actives | join(",")')

if [[ "$ALICE_AFTER" == "$NS_ALICE" ]]; then
  ok "alice's pick set to $NS_ALICE"
else
  fail "alice's pick should be $NS_ALICE, got $ALICE_AFTER"
fi

if [[ "$BOB_AFTER" == "" ]]; then
  ok "bob's view unaffected by alice's pick (cache stayed shared)"
else
  fail "bob's pick = '$BOB_AFTER' — should be empty (alice's pick leaked)"
fi

# bob's pod list should still work after alice's pick — proves cache still has bob's namespace
BOB_PODS_AFTER=$(as_user bob GET /api/resources/pods | jq -r '.items[].metadata.name // empty' | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$BOB_PODS_AFTER" == "rbac-test-pod-b" ]]; then
  ok "bob's pod list still works after alice's pick (cache wasn't shrunk)"
else
  fail "bob's pod list broke after alice's pick: [$BOB_PODS_AFTER]"
fi

echo
echo -e "${B}=== Picking a denied namespace is rejected ===${N}"

REJECT_HTTP=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
  -H "X-Forwarded-User: alice" -H 'Content-Type: application/json' \
  -d "{\"namespaces\":[\"$NS_BOB\"]}" \
  "http://localhost:$PORT/api/cluster/namespace")
if [[ "$REJECT_HTTP" == "403" ]]; then
  ok "alice rejected with 403 when picking $NS_BOB"
else
  fail "expected 403 when alice picks $NS_BOB; got $REJECT_HTTP"
fi

# Mixed-allowed/denied: alice picking [her ns, bob's ns] must 403 atomically
# (no partial pick). Verify the picker is all-or-nothing.
MIXED_HTTP=$(curl -s -o /dev/null -w '%{http_code}' -X POST \
  -H "X-Forwarded-User: alice" -H 'Content-Type: application/json' \
  -d "{\"namespaces\":[\"$NS_ALICE\",\"$NS_BOB\"]}" \
  "http://localhost:$PORT/api/cluster/namespace")
if [[ "$MIXED_HTTP" == "403" ]]; then
  ok "alice rejected with 403 on mixed allowed+denied pick (atomic)"
else
  fail "expected 403 on mixed pick; got $MIXED_HTTP"
fi
ALICE_MIXED_AFTER=$(as_user alice GET /api/cluster/namespace-scope | jq -r '.actives | join(",")')
if [[ "$ALICE_MIXED_AFTER" == "$NS_ALICE" ]]; then
  ok "alice's prior valid pick survived the rejected mixed update"
else
  fail "expected alice's pick to remain $NS_ALICE after rejected mixed update; got '$ALICE_MIXED_AFTER'"
fi

echo
echo -e "${B}=== Cluster-wide pods does NOT imply cluster-scoped reads (carol) ===${N}"
# carol has cluster-wide list pods + list namespaces only. She must NOT
# read Nodes / ClusterRoles / Secrets via the cache — those need their own
# RBAC. This is the per-kind SAR gate's primary regression test.

CAROL_PODS=$(as_user carol GET /api/resources/pods | jq -r '.items[].metadata.name // empty' | sort | tr '\n' ',' | sed 's/,$//')
if [[ "$CAROL_PODS" == *"rbac-test-pod-a"* && "$CAROL_PODS" == *"rbac-test-pod-b"* ]]; then
  ok "carol sees pods cluster-wide (her actual permission)"
else
  fail "carol should see all pods, got: $CAROL_PODS"
fi

expect_rest_forbidden "carol cannot list Nodes" carol /api/resources/nodes

expect_rest_forbidden "carol cannot list ClusterRoles" carol /api/resources/clusterroles

expect_mcp_code "MCP carol list_resources kind=nodes is her own denial" "$(mcp_call carol list_resources '{"kind":"nodes"}')" user_forbidden

echo
echo -e "${B}=== Multi-namespace user dashboard aggregates only allowed namespaces (dave) ===${N}"
# dave has view in both NS_ALICE and NS_BOB. He should see counts that sum
# only those two namespaces, not the whole cluster — the dashboard must
# iterate per allowed namespace rather than collapsing to a cluster-wide
# call.

DAVE_DASH=$(as_user dave GET /api/dashboard)
DAVE_DASH_PODS=$(echo "$DAVE_DASH" | jq -r '.resourceCounts.pods.total // 0')
# Expect exactly the two test pods (rbac-test-pod-a + rbac-test-pod-b).
if [[ "$DAVE_DASH_PODS" == "2" ]]; then
  ok "dave's dashboard pod count = 2 (NS_ALICE + NS_BOB only)"
else
  fail "dave's dashboard pod count = $DAVE_DASH_PODS; expected 2 (would be much higher if cluster-wide leaked)"
fi

# dave doesn't have list-nodes — node count should be 0.
DAVE_DASH_NODES=$(echo "$DAVE_DASH" | jq -r '.resourceCounts.nodes.total // 0')
if [[ "$DAVE_DASH_NODES" == "0" ]]; then
  ok "dave's dashboard node count = 0 (no list-nodes RBAC)"
else
  fail "dave's dashboard node count = $DAVE_DASH_NODES; expected 0"
fi

# dave doesn't have list-namespaces cluster-wide — ns count should be 0.
DAVE_DASH_NS=$(echo "$DAVE_DASH" | jq -r '.resourceCounts.namespaces // 0')
if [[ "$DAVE_DASH_NS" == "0" ]]; then
  ok "dave's dashboard namespaces count = 0 (no list-ns cluster-wide RBAC)"
else
  fail "dave's dashboard namespaces count = $DAVE_DASH_NS; expected 0"
fi

DAVE_NS=$(as_user dave GET /api/cluster/namespace-scope | jq -r '.accessibleNamespaces | sort | join(",")')
if [[ "$DAVE_NS" == "${NS_ALICE},${NS_BOB}" ]]; then
  ok "dave's accessibleNamespaces = [${NS_ALICE},${NS_BOB}]"
else
  fail "dave's accessibleNamespaces = [$DAVE_NS]; expected [${NS_ALICE},${NS_BOB}]"
fi

# Multi-namespace pick: dave selects both his allowed namespaces. Round-trip
# the slice and verify dashboards/pods stay scoped to exactly that set.
as_user dave POST /api/cluster/namespace "{\"namespaces\":[\"$NS_ALICE\",\"$NS_BOB\"]}" >/dev/null
DAVE_PICKS=$(as_user dave GET /api/cluster/namespace-scope | jq -r '.actives | sort | join(",")')
if [[ "$DAVE_PICKS" == "${NS_ALICE},${NS_BOB}" ]]; then
  ok "dave's multi-pick round-tripped as [${NS_ALICE},${NS_BOB}]"
else
  fail "dave's multi-pick = [$DAVE_PICKS]; expected [${NS_ALICE},${NS_BOB}]"
fi
DAVE_DASH_MULTI_PODS=$(as_user dave GET /api/dashboard | jq -r '.resourceCounts.pods.total // 0')
if [[ "$DAVE_DASH_MULTI_PODS" == "2" ]]; then
  ok "dave's dashboard with multi-pick still shows both ns pods (2)"
else
  fail "dave's dashboard with multi-pick = $DAVE_DASH_MULTI_PODS pods; expected 2"
fi
# Reset dave's pick so subsequent assertions see his unpinned view.
as_user dave POST /api/cluster/namespace '{"namespaces":[]}' >/dev/null

echo
echo -e "${B}=== Cluster-scoped CRDs not in the static map are gated via discovery (carol, dave) ===${N}"
# KubeAtlasTest is a cluster-scoped CRD that's NOT in the static IsClusterOnlyKind
# list. The discovery-based gate must catch it: only cluster-admin or
# explicitly-permitted users should see it.

# carol has cluster-wide pods+namespaces but NOT KubeAtlasTest read.
expect_rest_forbidden "REST carol cannot list kubeatlastests" carol /api/resources/kubeatlastests

# Same via the grouped-path:
expect_rest_forbidden "REST carol cannot list grouped kubeatlastests" carol "/api/resources/kubeatlastests?group=test.skyhook.io"

# MCP path:
expect_mcp_denied "MCP carol cannot list kubeatlastests" carol list_resources '{"kind":"kubeatlastests"}'

# dave (multi-namespace user) also can't see cluster-scoped CRDs without explicit RBAC.
expect_rest_forbidden "REST dave cannot list kubeatlastests" dave /api/resources/kubeatlastests

echo
echo -e "${B}=== Cluster-scoped reads aren't blocked by the noNamespaceAccess short-circuit (erin) ===${N}"
# erin has list-nodes ONLY, no namespace access. parseNamespacesForUser
# returns [] (empty allowed), so the cluster-only gate must run BEFORE the
# noNamespaceAccess short-circuit — otherwise her cluster-scoped read is
# denied even though she has the RBAC for it.

ERIN_NS=$(as_user erin GET /api/cluster/namespace-scope | jq -r '.accessibleNamespaces | sort | join(",")')
# erin has no namespace access — accessibleNamespaces should be empty.
if [[ -z "$ERIN_NS" ]]; then
  ok "erin's accessibleNamespaces is empty (correct: no namespace RBAC)"
else
  fail "erin's accessibleNamespaces = [$ERIN_NS]; expected empty"
fi

ERIN_NODES=$(as_user erin GET /api/resources/nodes | jq -c .items | arr_len)
expect_at_least_one "REST erin can list nodes despite no namespace access" "$ERIN_NODES"

expect_rest_forbidden "REST erin cannot list Pods" erin /api/resources/pods

ERIN_MCP_NODES=$(mcp_call erin list_resources '{"kind":"nodes"}' | jq '.items' 2>/dev/null | arr_len)
if [[ "$ERIN_MCP_NODES" -ge 1 ]]; then
  ok "MCP erin list_resources kind=nodes returns ≥1"
else
  fail "MCP erin got $ERIN_MCP_NODES nodes; expected ≥1"
fi

echo
echo -e "${B}=== Dashboard CRD counts are filtered per-user ===${N}"
# carol can see her cluster-wide pods+namespaces, but not kubeatlastests. Her
# dashboard/crds endpoint should NOT include kubeatlastests.
CAROL_DASH_CRDS=$(as_user carol GET /api/dashboard/crds | jq '[.topCRDs[]?.kind] | join(",")' 2>/dev/null || echo "")
if [[ "$CAROL_DASH_CRDS" != *"KubeAtlasTest"* ]]; then
  ok "carol's /api/dashboard/crds does not include cluster-scoped KubeAtlasTest"
else
  fail "carol's /api/dashboard/crds leaked KubeAtlasTest: $CAROL_DASH_CRDS"
fi

# dave (multi-namespace) — same: shouldn't include cluster-scoped CRDs.
DAVE_DASH_CRDS=$(as_user dave GET /api/dashboard/crds | jq '[.topCRDs[]?.kind] | join(",")' 2>/dev/null || echo "")
if [[ "$DAVE_DASH_CRDS" != *"KubeAtlasTest"* ]]; then
  ok "dave's /api/dashboard/crds does not include cluster-scoped KubeAtlasTest"
else
  fail "dave's /api/dashboard/crds leaked KubeAtlasTest: $DAVE_DASH_CRDS"
fi

echo
echo -e "${B}=== Per-namespace Secret RBAC (alice's view role excludes secrets) ===${N}"
# The K8s `view` ClusterRole does NOT include secrets. The chart can grant
# the SA cluster-wide secrets (rbac.helm / auth.mode != "none" / etc.), so
# the cache may hold rbac-test-secret-a; the server's per-namespace
# `list secrets` SAR must gate the read. alice's view role fails that SAR
# and she sees nothing. frank (same namespace, plus explicit secret-reader
# Role) sees the secret.

expect_rest_forbidden "REST alice cannot list Secrets" alice /api/resources/secrets

ALICE_SECRET_GET_HTTP=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Forwarded-User: alice" \
  "http://localhost:$PORT/api/resources/secrets/$NS_ALICE/rbac-test-secret-a")
if [[ "$ALICE_SECRET_GET_HTTP" == "403" ]]; then
  ok "REST alice GET secret returns 403"
else
  fail "REST alice GET secret returned $ALICE_SECRET_GET_HTTP; expected 403"
fi

expect_mcp_denied "MCP alice cannot list secrets" alice list_resources '{"kind":"secrets"}'

expect_mcp_denied "MCP alice cannot get Secret" alice get_resource "{\"kind\":\"secrets\",\"namespace\":\"$NS_ALICE\",\"name\":\"rbac-test-secret-a\"}"

ALICE_SEARCH_SECRETS=$(as_user alice GET "/api/search?q=kind:Secret" | jq -r '[.hits[]?.name] | join(",")' 2>/dev/null || echo "ERR")
if [[ "$ALICE_SEARCH_SECRETS" != *"rbac-test-secret-a"* ]]; then
  ok "REST search kind:Secret hides rbac-test-secret-a from alice"
else
  fail "REST search leaked secret to alice: [$ALICE_SEARCH_SECRETS]"
fi

# frank: positive control. Same namespace ceiling as alice, but with explicit
# secret-reader Role bound. He must see the secret.
FRANK_SECRETS=$(as_user frank GET /api/resources/secrets | jq -r '[.items[].metadata.name] | join(",")')
if [[ "$FRANK_SECRETS" == *"rbac-test-secret-a"* ]]; then
  ok "REST frank can list secrets (explicit secret-reader Role)"
else
  fail "REST frank should see rbac-test-secret-a, got: [$FRANK_SECRETS]"
fi

FRANK_SECRET_GET_HTTP=$(curl -s -o /dev/null -w '%{http_code}' -H "X-Forwarded-User: frank" \
  "http://localhost:$PORT/api/resources/secrets/$NS_ALICE/rbac-test-secret-a")
if [[ "$FRANK_SECRET_GET_HTTP" == "200" ]]; then
  ok "REST frank GET secret returns 200"
else
  fail "REST frank GET secret returned $FRANK_SECRET_GET_HTTP; expected 200"
fi

FRANK_SEARCH_SECRETS=$(as_user frank GET "/api/search?q=kind:Secret" | jq -r '[.hits[]?.name] | join(",")' 2>/dev/null || echo "ERR")
if [[ "$FRANK_SEARCH_SECRETS" == *"rbac-test-secret-a"* ]]; then
  ok "REST search kind:Secret shows rbac-test-secret-a to frank"
else
  fail "REST search missing secret for frank: [$FRANK_SEARCH_SECRETS]"
fi

# --- Summary ---
echo
if [[ "$FAILS" -eq 0 ]]; then
  echo -e "${G}All checks passed.${N}"
  exit 0
else
  echo -e "${R}$FAILS check(s) failed. See log: $LOGFILE${N}"
  exit 1
fi
