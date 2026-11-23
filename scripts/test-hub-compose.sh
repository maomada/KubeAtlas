#!/usr/bin/env bash
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf -- "$WORK"' EXIT

export KUBEATLAS_HUB_VERSION=0.0.0-test
export KUBEATLAS_HUB_ENV_FILE="$WORK/hub.env"
unset KUBEATLAS_HUB_PORT KUBEATLAS_HUB_CONTAINER_PORT

compose=(docker compose --env-file /dev/null -f "$ROOT/deploy/compose/compose.yml")

assert_ports() {
  local published="$1" target="$2"
  jq -e --arg published "$published" --argjson target "$target" '
    .services.hub
    | (.ports | length == 1) and
      (.ports[0] | .host_ip == "127.0.0.1" and .published == $published and .target == $target) and
      (.command | index("--listen=0.0.0.0:\($target)") != null)
  ' >/dev/null
}

"${compose[@]}" config --format json | assert_ports 1333 1333
echo "Default Hub host and container ports are 1333"

export KUBEATLAS_HUB_PORT=9290
"${compose[@]}" config --format json | assert_ports 9290 1333
echo "An existing reverse proxy can keep host port 9290"

cat > "$WORK/retain-previous.yml" <<'YAML'
services:
  hub:
    command:
      - --listen=0.0.0.0:9290
      - --hub-state-file=/var/lib/kubeatlas-portal/hub/state.json
      - --hub-previous-artifacts-dir=/opt/kubeatlas/previous-agent-catalog
      - --hub-previous-artifacts-version=0.1.3
    volumes:
      - /srv/kubeatlas/previous-agent-catalog:/opt/kubeatlas/previous-agent-catalog:ro
YAML

export KUBEATLAS_HUB_CONTAINER_PORT=9290
config=$("${compose[@]}" -f "$WORK/retain-previous.yml" config --format json)
assert_ports 9290 9290 <<< "$config"
jq -e '
  .services.hub.command | index("--hub-previous-artifacts-version=0.1.3") != null
' <<< "$config" >/dev/null
echo "The original retain-previous override keeps its listener and catalog"
