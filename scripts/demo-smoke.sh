#!/bin/sh
set -eu
base_url=${FLIXR_DEMO_URL:-http://127.0.0.1:19879}
cookies=$(mktemp)
response=$(mktemp)
trap 'rm -f "$cookies" "$response"' EXIT
curl --fail --silent --max-time 10 "$base_url/api/v1/setup/status" | jq -e '.claimed and .demo and .readiness.ffmpeg and .readiness.ffprobe' >/dev/null
profile_id=$(curl --fail --silent --max-time 10 "$base_url/api/v1/profiles" | jq -r '.profiles[] | select(.name=="Alex") | .id')
test -n "$profile_id"
curl --fail --silent --max-time 10 --cookie-jar "$cookies" -H 'Content-Type: application/json' --data '{"pin":""}' "$base_url/api/v1/profiles/$profile_id/select" | jq -e '.selected' >/dev/null
view=$(curl --fail --silent --max-time 10 --cookie "$cookies" "$base_url/api/v1/catalog/view?media=all")
printf '%s\n' "$view" | jq -e '[.sections[] | select(.name=="New") | .items[]] as $items | ($items|length)==100 and ($items|map(select(.kind=="film"))|length)==50 and ($items|map(select(.kind=="series"))|length)==50 and ($items|all(.demo and (.playable|not) and (.poster|length>0)))' >/dev/null
id=$(printf '%s\n' "$view" | jq -r '[.sections[] | select(.name=="New") | .items[]][0].id')
authority=$(curl --silent --max-time 10 -o "$response" -w '%{http_code}' --cookie "$cookies" -H 'Content-Type: application/json' --data "{\"catalog_id\":\"$id\",\"capabilities\":{}}" "$base_url/api/v1/playback/plans")
test "$authority" = 409
jq -e '.error.code=="playback_not_playable"' "$response" >/dev/null
poster=$(printf '%s\n' "$view" | jq -r '[.sections[] | select(.name=="New") | .items[]][0].poster')
curl --fail --silent --max-time 10 --cookie "$cookies" "$base_url$poster" > "$response"
test -s "$response"
printf 'Demo verified: 50 movies, 50 series, cached artwork, profiles, and enforced non-playability.\n'
