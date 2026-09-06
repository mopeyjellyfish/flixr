#!/usr/bin/env bash
# Run real browser acceptance with the media server on an internet-isolated network.
set -euo pipefail
cd "$(dirname "$0")/.."
name="flixr-offline-$$"
cleanup() {
  docker rm -f "$name-proxy" "$name-server" >/dev/null 2>&1 || true
  docker volume rm "$name-data" >/dev/null 2>&1 || true
  docker network rm "$name-network" >/dev/null 2>&1 || true
  docker image rm "$name" >/dev/null 2>&1 || true
}
trap cleanup EXIT

docker build -t "$name" .
docker network create --internal "$name-network" >/dev/null
docker volume create "$name-data" >/dev/null
docker run -d --name "$name-server" --network "$name-network" \
  -e FLIXR_LISTEN_ADDR=0.0.0.0:8787 -e FLIXR_DATA_DIR=/data \
  --mount "type=volume,source=$name-data,target=/data" \
  --mount "type=bind,source=$PWD/backend/testdata/media,target=/media,readonly" \
  "$name" >/dev/null

# Only the TCP relay has a host-facing network. Flixr has no default route.
docker run -d --name "$name-proxy" --network bridge -p 127.0.0.1::8787 \
  alpine/socat TCP-LISTEN:8787,fork,reuseaddr "TCP:$name-server:8787" >/dev/null
docker network connect "$name-network" "$name-proxy"
port="$(docker port "$name-proxy" 8787/tcp | sed 's/.*://')"
export FLIXR_ACCEPTANCE_URL="http://127.0.0.1:$port"
for attempt in {1..30}; do
  if curl --fail --silent --max-time 5 "$FLIXR_ACCEPTANCE_URL/api/v1/setup/status" >/dev/null; then break; fi
  sleep 1
done
curl --fail --silent --max-time 5 "$FLIXR_ACCEPTANCE_URL/api/v1/setup/status" >/dev/null
routes="$(docker exec "$name-server" ip route)"
if grep -q '^default ' <<< "$routes"; then
  echo 'Offline validation requires a server without a default route.' >&2
  exit 1
fi
export FLIXR_SETUP_TOKEN="$(docker logs "$name-server" 2>&1 | sed -n 's/.*Flixr setup token: //p' | tail -1)"
test -n "$FLIXR_SETUP_TOKEN"
export FLIXR_FILMS_ROOT=/media/films FLIXR_TV_ROOT=/media/tv
npm --prefix frontend run test:e2e -- --project=chromium-production --output=../output/playwright/offline

# A restart must preserve the local owner claim.
docker restart "$name-server" >/dev/null
for attempt in {1..30}; do
  if curl --fail --silent --max-time 5 "$FLIXR_ACCEPTANCE_URL/api/v1/setup/status" | grep -q '"claimed":true'; then
    echo 'Offline setup, scan, playback, screen control, and restart checks passed.'
    exit 0
  fi
  sleep 1
done
echo 'Flixr did not recover its claimed state after restart.' >&2
exit 1
