# Watching with FlixR

Choose Play on a movie or episode to open the player and start watching. The
picture uses the available screen, with its original proportions preserved.
The title and controls fade after three seconds of inactivity. Move the pointer,
tap the picture or use the keyboard to reveal them. Paused playback, open menus,
buffering and keyboard focus keep the controls visible.

The bottom controls let you pause, skip fifteen seconds, seek, change volume and enter
fullscreen. Select the time display to switch between elapsed and remaining time.
Settings contains streaming quality, audio tracks, subtitles, playback speed, fit/fill, episode
selection and title information. Previous and next episode buttons appear when
those episodes are available. Picture in picture appears on supported browsers.
Fullscreen stays an explicit choice; a browser that blocks automatic playback
shows a Play button.

Auto is the default streaming quality and is remembered on each device. It
starts at a transport-safe 360p ceiling in browsers with Media Source or Managed
Media Source, then raises the ceiling to 480p and 720p after measured throughput
shows enough headroom. Repeated or prolonged buffering lowers it one step. A
native-only HLS browser starts at 480p because it does not expose fragment
throughput; thirty seconds of uninterrupted playback with at least eight
seconds buffered can raise it, while buffering can lower it to 360p. Data saver
uses the 480p ceiling. Original uses a compatible source or stream copy when
possible. The settings menu shows the actual resolution and video bitrate
selected by the server. Quality changes
prepare a replacement stream and restore source position, play/pause state and
playback speed; they are not seamless multi-rendition switching.

| Key | Action |
| --- | --- |
| Space or K | Play/pause |
| Left / Right | Back/forward fifteen seconds |
| Up / Down | Volume |
| M | Mute |
| F | Fullscreen |
| Escape | Close the active menu; the browser handles leaving fullscreen |

Shortcuts apply to the player. Focused form fields and buttons retain their
normal keyboard behaviour. Dragging the timeline previews a target and seeks
when released. Repeated fifteen-second presses accumulate from the latest requested position,
even while an earlier seek finishes. During stream preparation, the timeline shows
the requested source time and stale picture and audio remain paused. The player
coalesces pending requests and keeps the latest target.

## Chapters and preview pictures

Files with embedded chapters show their chapter names and timeline markers.
Hover over or drag the timeline to request an image from the local video. Each
image represents a ten-second interval. Missing chapters are not invented;
unavailable images leave a time label so seeking still works.

No online service or credential is needed for these assets. Generation uses the
FFmpeg tools included in the container and never blocks initial playback.
There is one optional extraction worker, with one decoder/filter thread, an
eight-second timeout and a 512 KiB output limit. At most 64 generated assets
(32 MiB of payload) are held in disposable memory. Restarting clears this cache.
Busy generation returns temporarily unavailable; moving to another interval can
request another image. Supported probe containers include MP4/MOV, Matroska,
WebM, AVI, MPEG and Ogg. A file that cannot produce a preview remains playable
when its playback format is supported.

Assets require the active viewer, profile and playback session. Every request
reopens the confined, admitted source before consulting the cache. Stopping a
session cancels extraction and prevents further access, including cached images.

## Validation scope

Automated player checks cover desktop Chromium, Firefox and WebKit controls,
plus real-media playback through the built server and internet-isolated Docker
acceptance. The production suite captures phone portrait/landscape, tablet,
desktop, 4K and 8K viewports. Simulated viewports do not establish physical TV
remote, mobile hardware, HDR or A/V-sync qualification. Those v1 qualification
gates remain tracked in #112 and the related device issues.
