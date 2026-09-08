# Playback capability validation

Flixr assesses the selected title before creating a playback session. The
browser makes separate Media Capabilities requests for:

1. the original MP4 file;
2. the original H.264/AAC streams carried by fMP4 HLS; and
3. Flixr's bounded compatibility output.

The three requests run concurrently. Each has a two-second deadline; a stalled or
rejected request cannot authorize its playback path or delay the others indefinitely.
Completed positive evidence for another path remains usable.

The source requests include the catalogued AVC profile and level, display
dimensions after right-angle rotation, video bitrate, a conservative frame-rate
ceiling, and the primary audio track's AAC profile, channel count, sample rate,
and bitrate. The ceiling is the greater of ffprobe's average and nominal/base
rates so variable-rate bursts cannot be represented by a lower average. A
non-right-angle rotation leaves dimensions unknown and therefore cannot
authorize playback. HDR source playback also requires a
`(dynamic-range: high)` display match. Missing or rejected source evidence
cannot authorize direct play or stream copy.

The compatibility request describes the output FFmpeg actually creates:
H.264 High level 4.0, within a 1920x1080 landscape or 1080x1920 portrait
envelope, constant 30 fps, 5 Mbps target and maximum video bitrate, 8-bit 4:2:0
video, plus AAC-LC stereo at 48 kHz and 128 kbps when the source has audio. The
planner rejects larger, odd-sized, greater-than-30-fps, HDR, and unknown-codec
inputs because that rendition does not scale, tone-map, or promise a decoder
for them.

## Automated fixture results

The media-integration gate probes committed synthetic media and the generated
output. Its capability payload represents a browser that positively assessed
all three configurations.

| Fixture | Relevant source | Expected plan | Verified output |
| --- | --- | --- | --- |
| Blue Horizon 2026 | MP4, H.264 High L1.2, AAC-LC stereo, 320x180, 24 fps, 8-bit | Direct | Original file |
| Signal S01E01 | Matroska, H.264 High L1.2, AAC-LC mono, 320x180, 24 fps, 8-bit | Remux | Source video and primary audio preserved in fMP4 HLS |
| Compatibility Check 2026 | AVI, MPEG-4 Part 2 and MP3 mono | Transcode | H.264 High L4.0, yuv420p, 30 fps and AAC-LC stereo/48 kHz |
| Generated VFR Burst Check | MP4 with a 25.714 fps average and 60 fps nominal/base ceiling | Unsupported by the 30 fps compatibility fixture | 60 fps ceiling persists; the lower average does not authorize a path |
| Generated Rotated Display Check | MP4, MPEG-4 Part 2, coded 1920x1080 with 90° rotation | Transcode | H.264 High L4.0 at the preserved 1080x1920 display geometry |
| Oversize, odd-sized, over-30-fps, HDR, or unknown-codec cases | Outside the fixed rendition contract | Unsupported | Typed `playback_unsupported` response |

For every generated remux and transcode path, the gate checks the master
manifest's `CODECS` declaration, the media playlist's fMP4 initialization map,
the probed codecs and rendition properties, a combined initialization/media
fragment's MP4 container, and full decode. HTTP tests check the HLS manifest
MIME type and the MP4 MIME type for initialization and media fragments.

## Qualification boundary

The production browser acceptance runs the built server against real direct,
fMP4-remux and compatibility-transcode fixtures in Chromium. It also verifies
recovery after failed segment requests and subtitle selection across repeated
seeks. These runs establish the tested desktop browser path, not physical iOS,
Android, TV, Cast, AirPlay or GPU qualification.

The 8 September 2026 strict-client attempt found Safari 26.6
(21624.4.5.11.5) on the local Mac. WebDriver refused to create a session because
Safari's “Allow remote automation” setting is disabled. No Safari playback was
performed and no setting was changed. A strict native-client run still needs
actual play/fallback/unsupported results for the required fixtures, with the
host, OS and browser recorded. Issue #60 remains open until that evidence is
complete; browser mocks and generated FFmpeg output do not replace it.
