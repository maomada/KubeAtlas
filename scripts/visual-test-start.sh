#!/usr/bin/env bash
# Start a KubeAtlas instance for visual testing.
# Usage: ./scripts/visual-test-start.sh [--skip-build]

set -euo pipefail

SKIP_BUILD=false
for arg in "$@"; do
  case "$arg" in
    --skip-build) SKIP_BUILD=true ;;
    *) echo "Unknown argument: $arg" >&2; exit 2 ;;
  esac
done

STATEFILE=".playwright-mcp/visual-test-state.env"
if [[ -f "$STATEFILE" ]]; then
  existing_pid=""
  while IFS='=' read -r key value; do
    if [[ "$key" == "KUBEATLAS_PID" ]]; then
      existing_pid="$value"
      break
    fi
  done < "$STATEFILE"
  if [[ "$existing_pid" =~ ^[0-9]+$ ]] && kill -0 "$existing_pid" 2>/dev/null; then
    echo "A visual-test process is already recorded in $STATEFILE (pid $existing_pid)." >&2
    exit 1
  fi
fi

for bin in curl sha256sum git python3; do
  command -v "$bin" >/dev/null 2>&1 || { echo "Missing required dependency: $bin" >&2; exit 1; }
done

if [[ "$SKIP_BUILD" == false ]]; then
  command -v make >/dev/null 2>&1 || { echo "Missing required dependency: make" >&2; exit 1; }
  echo "Building KubeAtlas..."
  make build
fi

if [[ ! -x ./kubeatlas ]]; then
  echo "ERROR: ./kubeatlas binary not found or not executable. Run 'make build' first." >&2
  exit 1
fi

SOURCE_SHA="$(git rev-parse HEAD)"
if [[ -z "$(git status --porcelain)" ]]; then
  SOURCE_STATE=clean
else
  SOURCE_STATE=dirty
fi
if [[ "$SKIP_BUILD" == true ]]; then
  BINARY_PROVENANCE="unknown (--skip-build)"
else
  BINARY_PROVENANCE="make build from checkout $SOURCE_SHA ($SOURCE_STATE)"
fi
read -r BINARY_SHA _ < <(sha256sum ./kubeatlas)
if ! BINARY_VERSION="$(./kubeatlas --version 2>&1)"; then
  echo "ERROR: ./kubeatlas --version failed." >&2
  exit 1
fi

PORT=$((9300 + RANDOM % 100))
TIMESTAMP="$(date +%Y%m%d-%H%M%S)"
SSDIR=".playwright-mcp/visual-test/$TIMESTAMP"
LOGFILE="/tmp/kubeatlas-visual-test-$PORT.log"
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
  echo "Port $PORT is already in use; refusing to reuse an existing server." >&2
  exit 1
fi
if [[ -d .playwright-mcp/visual-test ]]; then
  while IFS= read -r -d '' dir; do
    rmdir "$dir" 2>/dev/null || true
  done < <(find .playwright-mcp/visual-test -mindepth 1 -maxdepth 1 -type d -empty -print0 2>/dev/null)
fi
mkdir -p "$SSDIR"

PID=""
STATE_WRITTEN=false
stop_unpublished_child() {
  if [[ -n "$PID" && "$STATE_WRITTEN" != true ]]; then
    if kill -0 "$PID" 2>/dev/null; then
      kill "$PID" 2>/dev/null || true
    fi
    wait "$PID" 2>/dev/null || true
  fi
}
trap stop_unpublished_child EXIT

echo "Source checkout: $SOURCE_SHA ($SOURCE_STATE)"
echo "Binary provenance: $BINARY_PROVENANCE"
echo "Binary SHA-256: $BINARY_SHA"
echo "Binary version: $BINARY_VERSION"
echo "Starting KubeAtlas on port $PORT..."
./kubeatlas --port "$PORT" --no-browser > "$LOGFILE" 2>&1 &
PID=$!

echo -n "Waiting for KubeAtlas to be ready"
READY=false
for _ in $(seq 1 30); do
  if ! kill -0 "$PID" 2>/dev/null; then
    echo " exited early"
    echo "KubeAtlas failed to start. Check logs: $LOGFILE" >&2
    exit 1
  fi
  if curl -fsS --max-time 2 "http://127.0.0.1:$PORT/api/health" >/dev/null 2>&1; then
    if ! kill -0 "$PID" 2>/dev/null; then
      echo " exited during readiness"
      echo "KubeAtlas failed to start. Check logs: $LOGFILE" >&2
      exit 1
    fi
    READY=true
    break
  fi
  echo -n "."
  sleep 1
done
if [[ "$READY" != true ]]; then
  echo " TIMEOUT"
  echo "KubeAtlas failed to start. Check logs: $LOGFILE" >&2
  exit 1
fi
echo " ready!"

printf 'KUBEATLAS_PID=%s\nKUBEATLAS_PORT=%s\nSCREENSHOT_DIR=%s\nKUBEATLAS_LOG=%s\nKUBEATLAS_URL=http://127.0.0.1:%s\n' \
  "$PID" "$PORT" "$SSDIR" "$LOGFILE" "$PORT" > "$STATEFILE"
STATE_WRITTEN=true

echo ""
echo "=== Visual Test Ready ==="
echo "  URL:          http://127.0.0.1:$PORT"
echo "  PID:          $PID"
echo "  Screenshots:  $SSDIR"
echo "  Logs:         $LOGFILE"
echo "  State:        $STATEFILE"
echo ""
echo "  REMINDER: set browser viewport BEFORE the first navigate, e.g."
echo "    mcp__playwright__browser_resize({ width: 1920, height: 1080 })"
echo "  Playwright defaults to ~1280px — too narrow to catch real layout bugs."
