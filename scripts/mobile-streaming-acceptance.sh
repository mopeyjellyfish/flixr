#!/bin/sh
set -eu

repo_dir=$(CDPATH= cd -- "$(dirname "$0")/.." && pwd)
run_id="flixr-mobile-$$"
fixture_dir=$(mktemp -d "${TMPDIR:-/tmp}/flixr-mobile-fixture.XXXXXX")
container_name="$run_id"
data_volume="$run_id-data"
cache_volume="$run_id-cache"
image_name="$run_id-image"
output_file=${FLIXR_MOBILE_OUTPUT:-$repo_dir/output/mobile-streaming/result.json}
revision=$(git -C "$repo_dir" rev-parse HEAD)
if ! git -C "$repo_dir" diff --quiet HEAD || test -n "$(git -C "$repo_dir" ls-files --others --exclude-standard)"; then
  worktree_hash=$(
    {
      git -C "$repo_dir" diff --binary HEAD
      git -C "$repo_dir" ls-files --others --exclude-standard | LC_ALL=C sort | while IFS= read -r path; do
        printf '%s %s\n' "$(git -C "$repo_dir" hash-object "$path")" "$path"
      done
    } | git hash-object --stdin
  )
  revision="$revision+working:$worktree_hash"
fi

cleanup() {
  docker rm -f "$container_name" >/dev/null 2>&1 || true
  docker volume rm "$data_volume" "$cache_volume" >/dev/null 2>&1 || true
  docker image rm "$image_name" >/dev/null 2>&1 || true
  rm -rf "$fixture_dir"
}
trap cleanup EXIT INT TERM

mkdir -p "$fixture_dir/media/films" "$fixture_dir/media/tv" "$(dirname "$output_file")"
ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i "testsrc2=size=1280x720:rate=30:duration=100" \
  -f lavfi -i "sine=frequency=440:sample_rate=48000:duration=100" \
  -c:v mpeg4 -q:v 2 -c:a aac -b:a 128k -shortest \
  "$fixture_dir/media/films/Mobile Acceptance 2026.mov"

docker build -q -t "$image_name" --build-arg VERSION=mobile-acceptance --build-arg REVISION="$revision" "$repo_dir" >/dev/null
image_id=$(docker image inspect --format '{{.Id}}' "$image_name")
docker run -d --name "$container_name" -p 127.0.0.1::8787 \
  -e FLIXR_TMDB_ENABLED=false \
  -v "$data_volume:/config" -v "$cache_volume:/cache" \
  -v "$fixture_dir/media:/media:ro" "$image_name" >/dev/null
port=$(docker port "$container_name" 8787/tcp | sed -n 's/.*://p' | head -1)
test -n "$port"

FLIXR_TEST_BASE="http://127.0.0.1:$port" \
FLIXR_TEST_CONTAINER="$container_name" \
FLIXR_TEST_OUTPUT="$output_file" \
FLIXR_TEST_REVISION="$revision" \
FLIXR_TEST_IMAGE="$image_id" \
node "$repo_dir/scripts/mobile-streaming-acceptance.mjs"
