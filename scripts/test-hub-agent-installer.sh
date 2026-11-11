#!/usr/bin/env bash
set -Eeuo pipefail
script="$(cd "$(dirname "$0")" && pwd)/install-hub-agent.sh"
bash -n "$script"
bash "$script" --help >/dev/null

common=(--context test-context --cluster-id test-cluster --version 1.2.3 \
  --image-digest "sha256:$(printf 'a%.0s' {1..64})" \
  --chart-sha256 "$(printf 'b%.0s' {1..64})" \
  --owner-user-id "kubeatlas:portal:$(printf 'c%.0s' {1..64})" --token-file /dev/null)

if output="$(bash "$script" --hub-origin https://hub.example.test/path "${common[@]}" 2>&1)"; then
  printf 'installer accepted a path-prefixed Hub origin\n' >&2
  exit 1
fi
[[ "$output" == *'HTTPS except for loopback development origins'* ]]

missing_context=(--cluster-id test-cluster --version 1.2.3 \
  --image-digest "sha256:$(printf 'a%.0s' {1..64})" \
  --chart-sha256 "$(printf 'b%.0s' {1..64})" \
  --owner-user-id "kubeatlas:portal:$(printf 'c%.0s' {1..64})" --token-file /dev/null)
if output="$(bash "$script" --hub-origin https://hub.example.test "${missing_context[@]}" 2>&1)"; then
  printf 'installer accepted a missing explicit kube context\n' >&2
  exit 1
fi
[[ "$output" == *'all required options must be provided explicitly'* ]]
if grep -Eq -- 'curl.*(--location|(^|[[:space:]])-L([[:space:]]|$))' "$script"; then
  printf 'installer must not follow redirects\n' >&2
  exit 1
fi
printf 'Hub Agent installer shell tests passed.\n'
