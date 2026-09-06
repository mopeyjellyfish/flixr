# Slice 006 validation

## Delivery status

Slice 006 is **partially delivered**. This is a mergeable LAN screen-control checkpoint, not release-one acceptance. Slice 007 (Google Cast and native AirPlay) is not implemented. Full product UI polish and true progress-return handoff remain deferred.

## Implemented behavior

- `screens.Manager` owns runtime-only, profile-scoped presence, one-use receiver tickets, single-claim control sessions, command queues, disconnect removal, restart invalidation, and shutdown cancellation.
- Receiver tickets expire after one minute. A connected receiver remains present until its connection closes or the server shuts down; discovery does not expire a live receiver.
- Control sessions expire after one minute. The controller must choose the screen again to reconnect. Selecting the same current title reconnects controls without issuing another Play command. Automatic authority renewal is not implemented.
- WebSocket upgrades require a selected profile and the exact request origin. Each controller command rechecks the profile session. Controller reads end at authority expiry, receiver disconnect, or shutdown. Receiver writes have a five-second timeout. `main` closes the manager before HTTP shutdown.
- The coordinator waits for an open socket before sending the first command. Stale responses and replaced sockets cannot change the current connection. Discovery refresh preserves active controls. Navigating to profile selection disconnects client authority.
- The receiver accepts Play, Pause, absolute Seek, and Stop. Initial positions and compatibility seeks use the playback planner. Browser autoplay policy may require the receiver's local Play button.
- The protocol's `handoff` command currently means **Stop remote playback**. It returns the receiver to its library; it does not resume media on the sender. The UI uses the accurate Stop label.
- Screen state is the last accepted sender command, not receiver telemetry. The UI says Connected, not that playback has been confirmed. The owner list is not live playback telemetry.

## Verification

- Frontend lint, TypeScript, all 44 Vitest tests, production build, embedded assets build, and bundle boundary pass.
- Backend unit tests, full race tests, vet, build, and required FFmpeg media-integration tests pass.
- All 30 mocked Playwright tests pass across Chromium, Firefox, and WebKit. Only the expected screen GET endpoints were added to fixtures; unknown requests still return 599.
- The production Chromium suite uses the embedded executable, checked-in playable fixtures, fresh temporary data, and two independent browser contexts. It proves receiver advertising, discovery, initial remote Play, Pause, restart Seek, Stop, a new controller connection, receiver disconnect, and removal from discovery.
- The same production suite checks the chooser at 1920×1080, 1440×900, 1024×768, and 390×844. It checks serious/critical axe findings, horizontal overflow, Escape, and focus restoration, and captures screenshots. Phone and TV captures were inspected; chooser spacing was corrected and the suite rerun.
- Regression tests first failed for stale coordinator state, dropped initial position, incorrect compatibility seek, idle controller survival after shutdown, live presence expiry, and discovery overwriting receiver status. Their focused reruns pass.
- Independent review covered the complete PR diff and the uncommitted fix checkpoint. Follow-up fixes keep live receivers present and preserve connection status during discovery. The unused `Validate` method was removed.

## Remaining limits

- One-minute control authority requires explicit reconnection; no automatic renewal.
- The eight-command queue and 128-entry presence/control caps are intentionally bounded. A full command queue ends that controller session. Caps are household-wide; repeated unconnected advertisements can occupy slots until ticket expiry.
- Receiver telemetry, progress-return handoff, Cast, AirPlay, and physical-device acceptance are not established by these tests. WebKit and mocks are not device evidence.
- The existing lazy hls.js chunk-size warning remains non-blocking; Player and hls.js remain outside the entry bundle.

## Local evidence and isolation

Production checks used a temporary server on port 18790. Mocked checks used an owned Vite preview on port 48125 after an initial run encountered an unrelated server on default port 4173. Owned processes were stopped. No existing server was killed, no demo volume or credentials were changed, and port 18789 was untouched.

Generated screenshots and traces remain in ignored `frontend/test-results/`; embedded generated assets remain unpublished. `backend/web/assets/placeholder.txt` remains tracked. This checkpoint does not refresh the running demo or constitute a release.
