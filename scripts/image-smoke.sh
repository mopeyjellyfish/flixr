#!/usr/bin/env bash
# A release must work with a fresh volume, non-root user and no compiler/toolchain.
set -euo pipefail
image="${1:?usage: image-smoke.sh IMAGE [VERSION]}"
version="${2:-dev}"
name="flixr-image-check-$$"
cleanup() { docker rm -f "$name" >/dev/null 2>&1 || true; docker volume rm "$name-data" "$name-cache" "$name-backups" >/dev/null 2>&1 || true; }
trap cleanup EXIT
platform="${FLIXR_SMOKE_PLATFORM:-$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')}"
docker run --rm --platform "$platform" "$image" --version | grep -F "Flixr $version ("
docker run --rm --platform "$platform" --entrypoint sh "$image" -ec '
  test "$(id -u)" = 100
  ! command -v node; ! command -v npm; ! command -v go; ! command -v gcc
  test -s /etc/ssl/certs/ca-certificates.crt
  ffmpeg -hide_banner -loglevel error -f lavfi -i testsrc2=size=64x64:rate=5 -t 0.4 -c:v libx264 /tmp/check.mp4
  ffprobe -v error -show_entries stream=codec_name /tmp/check.mp4 | grep h264
'
docker run --rm --platform "$platform" \
  --mount "type=volume,source=$name-backups,target=/backups" \
  --entrypoint sh "$image" -ec '
    test "$(id -u)" = 100
    test -w /backups
    touch /backups/.flixr-write-check
  '
docker run -d --platform "$platform" --name "$name" --read-only --tmpfs /tmp \
  --cap-drop ALL --security-opt no-new-privileges \
  --mount "type=volume,source=$name-data,target=/config" \
  --mount "type=volume,source=$name-cache,target=/cache" \
  --mount "type=volume,source=$name-backups,target=/backups" \
  -p 127.0.0.1::8787 "$image" >/dev/null
port="$(docker port "$name" 8787/tcp | sed 's/.*://')"
for attempt in {1..60}; do
  if curl -fsS "http://127.0.0.1:$port/api/v1/setup/status" > /dev/null; then break; fi
  sleep 1
done
status="$(curl -fsS "http://127.0.0.1:$port/api/v1/setup/status")"
for field in '"claimed":false' '"ffmpeg":true' '"ffprobe":true'; do grep -q "$field" <<< "$status"; done
if [[ "${FLIXR_EXPECT_APPLICATION_METADATA:-}" == 1 ]]; then
	for field in '"configured":true' '"source":"application"'; do grep -q "$field" <<< "$status"; done
fi
if grep -q '"demo":true' <<< "$status"; then echo 'Production image started in demo mode' >&2; exit 1; fi
curl -fsS "http://127.0.0.1:$port/" | grep -q '<html'
docker stop --time 15 "$name" >/dev/null
test "$(docker inspect --format '{{.State.ExitCode}}' "$name")" = 0
echo "Image smoke passed: $image ${FLIXR_SMOKE_PLATFORM:-native}"
