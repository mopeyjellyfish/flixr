#!/bin/sh
set -eu

base_url=${FLIXR_DEMO_URL:-http://127.0.0.1:8787}
cookies=$(mktemp)
not_playable=$(mktemp)
trap 'rm -f "$cookies" "$not_playable"' EXIT

status=$(curl --fail --silent --show-error "$base_url/api/v1/setup/status")
printf '%s\n' "$status" | jq --exit-status '.readiness.ffmpeg == true and .readiness.ffprobe == true' >/dev/null
if printf '%s\n' "$status" | jq --exit-status '.claimed == true' >/dev/null; then
  printf 'Demo smoke requires a fresh, unclaimed demo volume. Run make demo-reset first.\n' >&2
  exit 1
fi

token=$(docker compose logs --no-color flixr | sed -n 's/.*Flixr setup token: //p' | head -n 1)
test -n "$token"
owner_password=$(od -An -N16 -tx1 /dev/urandom | tr -d ' \n')

curl --fail --silent --show-error --cookie-jar "$cookies" \
  --header 'Content-Type: application/json' \
  --data "{\"token\":\"$token\",\"password\":\"$owner_password\"}" \
  "$base_url/api/v1/setup/claim" | jq --exit-status '.claimed == true' >/dev/null

profile=$(curl --fail --silent --show-error --cookie "$cookies" \
  --header 'Content-Type: application/json' \
  --data '{"name":"Demo viewer","pin":""}' \
  "$base_url/api/v1/profiles")
profile_id=$(printf '%s\n' "$profile" | jq --raw-output '.id')
test -n "$profile_id" && test "$profile_id" != null

curl --fail --silent --show-error --cookie "$cookies" --request POST "$base_url/api/v1/owner/scan" | jq --exit-status '(.scan.status == "running") or (.scan.status == "complete")' >/dev/null
for _ in $(seq 1 30); do
  scan=$(curl --fail --silent --show-error --cookie "$cookies" "$base_url/api/v1/owner/scan/status")
  [ "$(printf '%s\n' "$scan" | jq --raw-output '.scan.status')" = complete ] && break
  sleep 1
done
printf '%s\n' "$scan" | jq --exit-status '.scan.status == "complete" and .scan.failed == 0' >/dev/null

curl --fail --silent --show-error --cookie-jar "$cookies" --cookie "$cookies" \
  --header 'Content-Type: application/json' --data '{"pin":""}' \
  "$base_url/api/v1/profiles/$profile_id/select" | jq --exit-status '.selected == true' >/dev/null

view=$(curl --fail --silent --show-error --cookie "$cookies" "$base_url/api/v1/catalog/view?media=all")
printf '%s\n' "$view" | jq --exit-status '[.sections[] | select(.name == "New") | .items[]] as $items | ($items | map(select(.kind == "film")) | length) == 20 and ($items | map(select(.kind == "series")) | length) == 20 and ($items | all(.playable == false and .demo == true))' >/dev/null
demo_id=$(printf '%s\n' "$view" | jq --raw-output '[.sections[] | select(.name == "New") | .items[]][0].id')
test -n "$demo_id" && test "$demo_id" != null
playback_status=$(curl --silent --show-error --output "$not_playable" --write-out '%{http_code}' \
  --cookie "$cookies" --header 'Content-Type: application/json' \
  --data "{\"catalog_id\":\"$demo_id\",\"capabilities\":{\"containers\":[\"mp4\"],\"video_codecs\":[\"h264\"],\"audio_codecs\":[\"aac\"]}}" \
  "$base_url/api/v1/playback/plans")
test "$playback_status" = 409
jq --exit-status '.error.code == "playback_not_playable"' "$not_playable" >/dev/null

docker compose restart flixr
docker compose up --detach --wait
view=$(curl --fail --silent --show-error --cookie "$cookies" "$base_url/api/v1/catalog/view?media=all")
printf '%s\n' "$view" | jq --exit-status '[.sections[] | select(.name == "New") | .items[]] as $items | ($items | length) == 40 and ($items | all(.playable == false and .demo == true))' >/dev/null

printf 'Demo smoke passed: setup readiness, 20 films, 20 series, viewer API, non-playable records, and restart persistence.\n'
