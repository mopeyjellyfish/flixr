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

Physical iOS, Android, TV, Cast, AirPlay, and GPU targets remain external
qualification work. A browser automation run cannot certify those devices.
