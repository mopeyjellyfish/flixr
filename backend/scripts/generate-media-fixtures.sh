#!/usr/bin/env bash
set -euo pipefail

root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/testdata/media}"
films="$root/films"
tv="$root/tv/Signal/Season 01"

command -v ffmpeg >/dev/null 2>&1 || {
  printf 'ffmpeg is required to generate Flixr media fixtures.\n' >&2
  exit 1
}

mkdir -p "$films" "$tv"

make_fixture() {
  local color="$1"
  local output="$2"
  ffmpeg -hide_banner -loglevel error -y \
    -f lavfi -i "color=c=${color}:s=320x180:r=24" \
    -f lavfi -i 'anullsrc=channel_layout=stereo:sample_rate=48000' \
    -t 0.25 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest \
    "$output"
}

make_fixture blue "$films/Blue Horizon 2026.mp4"
make_fixture navy "$tv/Signal S01E01.mkv"
make_fixture black "$tv/Signal S01E02.mkv"

ffmpeg -hide_banner -loglevel error -y \
  -f lavfi -i 'testsrc2=size=320x180:rate=24:duration=2' \
  -f lavfi -i 'sine=frequency=660:sample_rate=48000:duration=2' \
  -c:v mpeg4 -q:v 5 \
  -c:a libmp3lame -b:a 64k "$films/Compatibility Check 2026.avi"
