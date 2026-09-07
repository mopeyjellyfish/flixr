# Playback capability validation

Flixr asks the browser about the selected title before it creates a playback
session. Direct play requires a successful native decode assessment; absent or
incomplete evidence falls back to the existing fMP4 HLS path, or reports the
title unsupported when that path is unavailable.

The local browser-engine check on 2026-09-07 used Playwright Chromium
151.0.7922.34, Firefox 153.0, and WebKit 26.5 on macOS ARM64. All three
reported H.264/AAC MP4 support and MediaSource; Chromium and WebKit also
reported native HLS. This is browser-engine evidence, not physical-device or
receiver qualification.

The automated fixture ledger is intentionally limited to plans and browser
capability requests. Blue Horizon (MP4/H.264 High/AAC, 320x180, 24fps, 8-bit)
requests direct playback after a successful MediaCapabilities result. Container
or codec mismatches request fMP4 HLS; media exceeding declared client limits
returns `playback_unsupported` until a bounded transcode rendition is available.
No automated run has established decoded-frame playback for a strict native
client, a tolerant browser, or physical receiver hardware. The local evidence
does not record a hardware model, GPU, or macOS patch version, so it must not be
used as a hardware qualification result.

Physical iOS, Android, TV, Cast, AirPlay, and GPU targets remain external
qualification work. A browser automation run cannot certify those devices.
