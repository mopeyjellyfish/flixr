#!/usr/bin/env bash
# Runs issue #56's cold/warm artwork timing while a real fixture remux and scan run.
# It creates and mutates only a mktemp directory. The source demo cache is copied.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
baseline="${FLIXR_MEASURE_BASELINE:-origin/main}"
demo_source="${FLIXR_MEASURE_DEMO_SOURCE:?Set FLIXR_MEASURE_DEMO_SOURCE to the local demo cache to copy.}"
fixture_source="${FLIXR_MEASURE_FIXTURE_SOURCE:-$repo_root/backend/testdata/media/corpus/Long Duration Seek 2026.mkv}"
port="${FLIXR_MEASURE_PORT:-18989}"
copy_count="${FLIXR_MEASURE_COPY_COUNT:-300}"
measurement_dir="$(mktemp -d "${TMPDIR:-/tmp}/flixr-artwork-acceptance.XXXXXX")"
source_dir="$measurement_dir/source"
data_dir="$measurement_dir/data"
media_dir="$measurement_dir/media"
cookie_jar="$measurement_dir/cookies.txt"
profile_jar="$measurement_dir/profile-cookies.txt"
result_dir="${FLIXR_MEASURE_RESULT_DIR:-$repo_root/frontend/test-results/artwork-acceptance-$(date +%Y%m%d-%H%M%S)}"
server_pid=""
sampler_pid=""

cleanup() {
  if [[ -n "$sampler_pid" ]]; then kill "$sampler_pid" 2>/dev/null || true; wait "$sampler_pid" 2>/dev/null || true; fi
  if [[ -n "$server_pid" ]]; then kill "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  rm -rf "$measurement_dir"
}
trap cleanup EXIT

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
for command in git tar npm node go curl jq ffmpeg ffprobe ps; do require "$command"; done
[[ -d "$demo_source" ]] || { echo "demo source is not a directory: $demo_source" >&2; exit 1; }
[[ -f "$fixture_source" ]] || { echo "fixture source is not a file: $fixture_source" >&2; exit 1; }
mkdir -p "$source_dir" "$data_dir/demo" "$media_dir/films" "$result_dir"

# The archived tree fixes the binary and embedded frontend to the recorded release SHA.
git -C "$repo_root" archive "$baseline" | tar -xf - -C "$source_dir"
baseline_sha="$(git -C "$repo_root" rev-parse "$baseline")"
cp -R "$demo_source"/. "$data_dir/demo/"
cp "$fixture_source" "$media_dir/films/Long Duration Seek 2026.mkv"
(cd "$source_dir/frontend" && npm ci --ignore-scripts)
(cd "$source_dir/frontend" && npm run build:embed)
(cd "$source_dir/backend" && go build -o "$measurement_dir/flixr" .)

wait_for_http() {
  for _ in $(seq 1 100); do
    if curl --fail --silent --max-time 1 "http://127.0.0.1:$port/api/v1/setup/status" >/dev/null; then return 0; fi
    sleep 0.1
  done
  echo "isolated Flixr server did not start" >&2
  return 1
}

start_server() {
  "$@" >"$result_dir/server.log" 2>&1 &
  server_pid="$!"
  wait_for_http
}

# Import the copied demo cache once, creating durable original artwork in the temp data.
start_server env FLIXR_DEMO=true FLIXR_DATA_DIR="$data_dir" FLIXR_LISTEN_ADDR="127.0.0.1:$port" "$measurement_dir/flixr"
kill "$server_pid"; wait "$server_pid" 2>/dev/null || true; server_pid=""

# This is intentionally the sole cache clear. Original artwork and all source paths remain intact.
derivative_dir="$data_dir/artwork/derivatives"
[[ "$derivative_dir" == "$data_dir"/artwork/derivatives ]] || exit 1
rm -rf "$derivative_dir"
[[ ! -e "$derivative_dir" ]] || { echo "could not clear isolated derivative directory" >&2; exit 1; }

# First scan admits the playable fixture. The copied demo records retain 100 cached posters.
start_server env FLIXR_DATA_DIR="$data_dir" FLIXR_LISTEN_ADDR="127.0.0.1:$port" FLIXR_FILMS_ROOT="$media_dir/films" FLIXR_SCAN_ON_START=true FLIXR_SCAN_WORKERS=2 "$measurement_dir/flixr"
base_url="http://127.0.0.1:$port"
curl --fail --silent --show-error --cookie-jar "$cookie_jar" -H "Content-Type: application/json" -d '{"password":"flixr-demo-only"}' "$base_url/api/v1/owner/login" >/dev/null
alex_id="$(curl --fail --silent --show-error "$base_url/api/v1/profiles" | jq -r '.profiles[] | select(.name == "Alex") | .id')"
[[ -n "$alex_id" && "$alex_id" != "null" ]] || { echo "copied demo did not provide Alex" >&2; exit 1; }
curl --fail --silent --show-error --cookie-jar "$profile_jar" -H "Content-Type: application/json" -d '{"pin":""}' "$base_url/api/v1/profiles/$alex_id/select" >/dev/null
for _ in $(seq 1 150); do
  scan="$(curl --fail --silent --show-error --cookie "$cookie_jar" "$base_url/api/v1/owner/scan/status")"
  [[ "$(jq -r '.scan.status' <<<"$scan")" != "running" ]] && break
  sleep 0.1
done
[[ "$(jq -r '.scan.status' <<<"$scan")" == "complete" ]] || { echo "initial fixture scan did not complete: $scan" >&2; exit 1; }
fixture_id="$(curl --fail --silent --show-error --cookie "$profile_jar" "$base_url/api/v1/catalog/search?q=Long%20Duration%20Seek" | jq -r '.items[] | select(.title == "Long Duration Seek 2026") | .id')"
[[ -n "$fixture_id" && "$fixture_id" != "null" ]] || { echo "fixture was not browseable after initial scan" >&2; exit 1; }

# New, isolated copies make the concurrent scan invoke the real ffprobe path.
for number in $(seq 1 "$copy_count"); do
  cp "$fixture_source" "$media_dir/films/Scan Fixture $number 2026.mkv"
done

playback="$(curl --fail --silent --show-error --cookie "$profile_jar" -H "Content-Type: application/json" -d "{\"catalog_id\":\"$fixture_id\",\"capabilities\":{\"containers\":[\"mp4\"],\"video_codecs\":[\"h264\"],\"audio_codecs\":[\"aac\"],\"supports_fmp4_hls\":true}}" "$base_url/api/v1/playback/plans")"
[[ "$(jq -r '.plan.kind' <<<"$playback")" == "remux" ]] || { echo "fixture did not begin fMP4 remux: $playback" >&2; exit 1; }
manifest="$(jq -r '.media_url' <<<"$playback")"
curl --fail --silent --show-error --cookie "$profile_jar" "$base_url$manifest" >/dev/null

sample_resources() {
  while :; do
    sample="$(ps -axo pid=,ppid=,%cpu=,rss=,command= | awk -v parent="$server_pid" '$1 == parent || $2 == parent { cpu += $3; rss += $4; names = names (names ? "," : "") $5 } END { printf "%.1f %d %s\\n", cpu, rss, names }')"
    cpu="${sample%% *}"; rest="${sample#* }"; rss="${rest%% *}"; names="${rest#* }"
    printf '%s cpu_percent=%s rss_kib=%s processes=%s\n' "$(date -u +%FT%TZ)" "$cpu" "$rss" "$names" >>"$result_dir/resources.log"
    sleep 0.1
  done
}

sample_resources & sampler_pid="$!"
curl --fail --silent --show-error --cookie "$cookie_jar" -X POST "$base_url/api/v1/owner/scan" >"$result_dir/scan-start.json"
printf '%s browser_artwork_run_started\n' "$(date -u +%FT%TZ)" >"$result_dir/phases.log"
FLIXR_MEASURE_URL="$base_url" node "$repo_root/frontend/scripts/measure-artwork-local.mjs" >"$result_dir/artwork-timings.json"
printf '%s browser_artwork_run_finished\n' "$(date -u +%FT%TZ)" >>"$result_dir/phases.log"
for _ in $(seq 1 300); do
  scan="$(curl --fail --silent --show-error --cookie "$cookie_jar" "$base_url/api/v1/owner/scan/status")"
  [[ "$(jq -r '.scan.status' <<<"$scan")" != "running" ]] && break
  sleep 0.1
done
kill "$sampler_pid"; wait "$sampler_pid" 2>/dev/null || true; sampler_pid=""
printf '%s\n' "$scan" >"$result_dir/scan-final.json"
[[ "$(jq -r '.scan.status' <<<"$scan")" == "complete" ]] || { echo "concurrent scan did not complete: $scan" >&2; exit 1; }

derivative_count="$(find "$derivative_dir" -type f -name '*.jpg' | wc -l | tr -d ' ')"
peak_cpu="$(awk '{split($2, value, "="); if (value[2] + 0 > peak) peak = value[2] + 0} END {printf "%.1f", peak}' "$result_dir/resources.log")"
peak_rss="$(awk '{split($3, value, "="); if (value[2] + 0 > peak) peak = value[2] + 0} END {printf "%d", peak}' "$result_dir/resources.log")"
jq -n --arg source_sha "$baseline_sha" --arg fixture "$fixture_source" --argjson copy_count "$copy_count" --argjson derivatives "$derivative_count" --argjson peak_cpu "$peak_cpu" --argjson peak_rss "$peak_rss" --slurpfile timings "$result_dir/artwork-timings.json" --slurpfile scan "$result_dir/scan-final.json" '{source_sha:$source_sha,fixture:$fixture,concurrent_scan_copies:$copy_count,derivatives_after_cold:$derivatives,resources:{sampling_interval_ms:100,peak_cpu_percent:$peak_cpu,peak_rss_kib:$peak_rss,scope:"Flixr server plus immediate ffmpeg/ffprobe children"},artwork_timings:$timings[0],scan:$scan[0]}' >"$result_dir/summary.json"
printf 'Results: %s\n' "$result_dir"
cat "$result_dir/summary.json"
