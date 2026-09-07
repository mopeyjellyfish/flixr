#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
port="${FLIXR_METADATA_ACCEPTANCE_PORT:-4191}"
startup_attempts="${FLIXR_METADATA_ACCEPTANCE_STARTUP_ATTEMPTS:-480}"
poll_interval="${FLIXR_METADATA_ACCEPTANCE_POLL_INTERVAL:-.25}"
server_log="$(mktemp)"
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" >/dev/null 2>&1 || true
    wait "$server_pid" >/dev/null 2>&1 || true
  fi
  rm -f "$server_log"
}
trap cleanup EXIT

npm --prefix frontend run build:embed
(
  cd backend
  FLIXR_METADATA_ACCEPTANCE_PORT="$port" go test -tags=metadata_acceptance ./web -run TestMetadataAcceptanceServer -count=1 -timeout=5m
) >"$server_log" 2>&1 &
server_pid="$!"
for ((attempt = 0; attempt < startup_attempts; attempt++)); do
  if curl --fail --silent --max-time 1 "http://127.0.0.1:$port/__acceptance/config" >/dev/null; then
    FLIXR_ACCEPTANCE_URL="http://127.0.0.1:$port" npm --prefix frontend run test:e2e -- --project=chromium-metadata --output=../output/playwright/metadata
    exit 0
  fi
  if ! kill -0 "$server_pid" >/dev/null 2>&1; then
    cat "$server_log" >&2
    exit 1
  fi
  sleep "$poll_interval"
done
cat "$server_log" >&2
echo 'Metadata acceptance server did not become ready.' >&2
exit 1
