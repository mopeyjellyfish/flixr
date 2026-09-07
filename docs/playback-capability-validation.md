# Playback capability validation

Flixr assesses the selected title before creating a playback session. The
browser makes separate Media Capabilities requests for:

1. the original MP4 file;
2. the original H.264/AAC streams carried by fMP4 HLS; and
3. Flixr's bounded compatibility output.

The source requests include the catalogued AVC profile and level, dimensions,
video bitrate, frame rate, and the primary audio track's AAC profile, channel
count, sample rate, and bitrate. HDR source playback also requires a
`(dynamic-range: high)` display match. Missing or rejected source evidence
cannot authorize direct play or stream copy.

The compatibility request describes the output FFmpeg actually creates:
H.264 High level 4.0, at most 1920x1080, constant 30 fps, 5 Mbps target and
maximum video bitrate, 8-bit 4:2:0 video, plus AAC-LC stereo at 48 kHz and
128 kbps when the source has audio. The planner rejects larger, odd-sized,
greater-than-30-fps, HDR, and unknown-codec inputs because that rendition does
not scale, tone-map, or promise a decoder for them.

## Automated fixture results

The media-integration gate probes committed synthetic media and the generated
output. Its capability payload represents a browser that positively assessed
all three configurations.

| Fixture | Relevant source | Expected plan | Verified output |
| --- | --- | --- | --- |
| Blue Horizon 2026 | MP4, H.264 High L1.2, AAC-LC stereo, 320x180, 24 fps, 8-bit | Direct | Original file |
| Signal S01E01 | Matroska, H.264 High L1.2, AAC-LC mono, 320x180, 24 fps, 8-bit | Remux | Source video and primary audio preserved in fMP4 HLS |
| Compatibility Check 2026 | AVI, MPEG-4 Part 2 and MP3 mono | Transcode | H.264 High L4.0, yuv420p, 30 fps and AAC-LC stereo/48 kHz |
| Oversize, odd-sized, over-30-fps, HDR, or unknown-codec cases | Outside the fixed rendition contract | Unsupported | Typed `playback_unsupported` response |

For every generated remux and transcode path, the gate checks the master
manifest's `CODECS` declaration, the media playlist's fMP4 initialization map,
the probed codecs and rendition properties, a combined initialization/media
fragment's MP4 container, and full decode. HTTP tests check the HLS manifest
MIME type and the MP4 MIME type for initialization and media fragments.

## Qualification boundary

These automated checks establish planner, browser-request, transport, and
decoder behavior on the development host. They do not establish playback on a
strict native client, a tolerant desktop browser, or physical iOS, Android,
TV, Cast, AirPlay, and GPU targets. No qualified run currently records those
clients' exact hardware, GPU, OS, and browser versions.

Issue #60 must remain open until the physical strict-client and tolerant-client
matrix records actual play, fallback, and unsupported results for every
required fixture. Browser mocks and generated FFmpeg output cannot close that
qualification gap.
