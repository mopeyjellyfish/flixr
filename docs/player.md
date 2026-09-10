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
shows a Play button. Seeking and automatic stream replacement keep the current
fullscreen presentation active. Leaving fullscreen, returning to the library,
finishing playback or closing the player cancels that presentation normally.

Auto is the default streaming quality and is remembered on each device. It
starts unknown connections at a bounded 720p ceiling. Four conservative distinct
fragment measurements spanning at least eight seconds can inform the next start on the
same device and FlixR origin; this evidence expires after 24 hours. Missing,
future-dated, malformed or unavailable browser storage falls back to 720p. FlixR
does not infer bandwidth from a LAN, private or VPN address.

During playback, Auto measures completed fragments and in-flight fragment bytes.
It compares delivery with the selected video plus audio bandwidth. Sustained
insufficient delivery, a draining buffer or a three-second startup without usable
media lowers the ceiling one step before the buffer is exhausted. Emergency
downshifts can interrupt an upgrade cooldown. Credible constrained fragment
delivery during startup can select the 360p floor directly. Native HLS uses that
floor when the deadline expires without usable media because fragment telemetry
is unavailable. Raising quality requires sustained
headroom and uses a longer recovery delay after a downgrade. Native HLS does not
provide fragment telemetry, so it also uses sampled buffer growth and drain.
Data saver uses the 480p ceiling.
Original uses a compatible source or stream copy when possible. The settings
menu shows the actual resolution and video bitrate selected by the server.

Quality changes prepare a replacement while the current session remains
available. FlixR commits the handoff only after the selected browser pipeline
reports usable media; a rejected, superseded or eight-second-unready candidate
is released and the retained stream is reattached. Timeline, fullscreen,
play/pause, playback speed and track intent follow the winning source. This is a
bounded stream restart and does not claim seamless multi-rendition switching.

## Versions and editions

When a title has several encodings, its detail page lists the available versions
by resolution, codec and HDR format. Choosing one pins that physical source for
the whole playback session. Resume position and other personal history stay on
the title, and the same version lineage is requested for the next episode. If a
remembered choice is missing or cannot play on the current device, Flixr explains
the problem and offers compatible alternatives; it does not silently substitute
another file. Auto checks every available, authorized version and prefers a file
the device can play directly before starting a conversion. Choosing Auto clears
a remembered explicit version after the new playback session is admitted.

Owners create version groups in **Server settings → Media versions**. Group only
different encodings of the same cut. Give theatrical, extended, director's-cut
or otherwise different editions separate labels and keep them as separate
titles. Flixr rejects series groups unless every series contains one unambiguous
episode for each matching season and episode number.

Grouping activates the canonical title's watch state. The other members' resume
positions, ratings, lists and viewing history remain stored but dormant; ungrouping
restores them unchanged. Grouping never merges that history. Editing a group
stops affected active playback sessions so a session cannot retain access through
an obsolete membership. Flixr creates streams on demand; owner-prepared rendition
generation is outside this feature.

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
the requested source time and stale picture and audio remain paused. A spinner
is the only visible loading indicator; assistive technology receives a Loading
status, while playback failures still show an actionable error. The player
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
standard element fullscreen, WebKit's native video-presentation API and
real-media playback through the built server and internet-isolated Docker
acceptance. The production suite captures phone portrait/landscape, tablet,
desktop, 4K and 8K viewports. Desktop WebKit automation exercises the native
presentation contract but does not establish physical iPhone or iPad behavior.
Simulated viewports also do not establish physical TV remote, mobile hardware,
HDR or A/V-sync qualification. Those v1 qualification gates remain tracked in
#112 and the related device issues.

`scripts/fullscreen-seek-acceptance.mjs` verifies the source-replacement path
against an isolated loopback server containing the generated 120-second color
probe (`Fullscreen Color Probe 2026.mov`: red, blue, green and white in
30-second blocks). It asserts that a fullscreen seek to 50 seconds decodes blue,
a reverse seek to 5 seconds decodes red, the same video element remains
fullscreen and the loading overlay contains no visible text. Set
`FLIXR_TEST_BASE` and `FLIXR_TEST_OUTPUT` before running it.

If an active SourceBuffer cannot finish updating and clear its old timestamp
ranges within the bounded safety wait, FlixR uses a normal media reattachment so
the seek target remains accurate. Standard container fullscreen remains active,
but that exceptional fallback may leave native OS video fullscreen. Physical
iPhone and iPad qualification must include this fallback before it is claimed.
Switching an active compatibility stream to a directly playable Original source
uses the same no-reset handoff and releases the retired MediaSource after the
direct URL is active. A manual change in the other direction must attach a new
MediaSource; browsers that require an explicit load for Managed Media Source may
leave native OS fullscreen during that transition. Automatic quality changes do
not start from direct playback.
