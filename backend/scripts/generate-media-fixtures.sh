#!/usr/bin/env bash
set -euo pipefail

root="${1:-$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)/testdata/media}"
films="$root/films"
tv="$root/tv/Signal/Season 01"
corpus="$root/corpus"
work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

command -v ffmpeg >/dev/null 2>&1 || { printf 'ffmpeg is required to generate Flixr media fixtures.\n' >&2; exit 1; }
mkdir -p "$films" "$tv" "$corpus/versions" "$corpus/formats"
printf '1\n00:00:00,000 --> 00:00:01,500\nSignal caption\n' > "$work/caption.srt"
printf '1\n00:00:00,000 --> 00:00:01,500\nSous-titre Signal\n' > "$work/caption-fr.srt"
printf ';FFMETADATA1\ntitle=Signal\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=0\nEND=1000\ntitle=Opening\n[CHAPTER]\nTIMEBASE=1/1000\nSTART=1000\nEND=2000\ntitle=Closing\n' > "$work/chapters.ffmeta"

make_basic() {
  local color="$1" output="$2"
  ffmpeg -hide_banner -loglevel error -y -f lavfi -i "color=c=${color}:s=320x180:r=24" -f lavfi -i 'anullsrc=channel_layout=stereo:sample_rate=48000' -t 2 -c:v libx264 -pix_fmt yuv420p -c:a aac -shortest "$output"
}

make_basic blue "$films/Blue Horizon 2026.mp4"
make_basic black "$tv/Signal S01E02.mkv"
make_basic teal "$corpus/versions/Edition Check 2026 - Theatrical Cut.mp4"
make_basic maroon "$corpus/versions/Edition Check 2026 - Director Cut.mp4"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'color=c=navy:s=320x180:r=24:d=2' -f lavfi -i 'sine=frequency=440:sample_rate=48000:duration=2' -f lavfi -i 'sine=frequency=660:sample_rate=48000:duration=2' -i "$work/caption.srt" -i "$work/caption-fr.srt" -i "$work/chapters.ffmeta" -map 0:v -map 1:a -map 2:a -map 3:s -map 4:s -map_metadata 5 -map_chapters 5 -c:v libx264 -pix_fmt yuv420p -c:a aac -c:s srt -metadata:s:a:0 language=eng -metadata:s:a:1 language=fra -metadata:s:s:0 language=eng -metadata:s:s:1 language=fra -disposition:s:0 default+forced -disposition:s:1 0 "$tv/Signal S01E01.mkv"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=24:duration=2' -f lavfi -i 'sine=frequency=660:sample_rate=48000:duration=2' -c:v mpeg4 -q:v 5 -c:a libmp3lame -b:a 64k "$films/Compatibility Check 2026.avi"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=30:duration=6' -f lavfi -i 'sine=frequency=880:sample_rate=48000:duration=6' -vf "select='eq(mod(n,5),0)+eq(mod(n,5),1)'" -fps_mode passthrough -c:v libvpx-vp9 -b:v 200k -c:a libopus "$corpus/formats/Variable Frame Rate 2026.webm"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=24:duration=2' -f lavfi -i 'sine=frequency=550:sample_rate=48000:duration=2' -c:v mpeg4 -q:v 5 -c:a aac "$corpus/formats/Compatibility Check 2026.mov"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=24:duration=2' -f lavfi -i 'sine=frequency=330:sample_rate=48000:duration=2' -c:v mpeg2video -q:v 5 -c:a mp2 -b:a 96k -f mpegts "$corpus/formats/Transport Check 2026.ts"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'testsrc2=size=320x180:rate=24:duration=2' -f lavfi -i 'sine=frequency=330:sample_rate=48000:duration=2' -c:v mpeg2video -q:v 5 -c:a mp2 -b:a 96k -f mpegts -mpegts_m2ts_mode 1 "$corpus/formats/Transport Check 2026.m2ts"
ffmpeg -hide_banner -loglevel error -y -f lavfi -i 'color=c=purple:s=64x36:r=1:d=600' -f lavfi -i 'anullsrc=channel_layout=stereo:sample_rate=48000' -t 600 -c:v libx264 -preset ultrafast -crf 40 -g 1 -pix_fmt yuv420p -c:a aac -b:a 8k -shortest "$corpus/Long Duration Seek 2026.mkv"
dd if="$films/Compatibility Check 2026.avi" of="$corpus/formats/Partial Check 2026.avi" bs=1 count=512 status=none
