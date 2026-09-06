# Slice 006 validation

## Implemented seam

- Runtime-only `screens.Manager` owns profile-scoped presence, one-use expiring receiver tickets, expiring control sessions, command state, disconnect removal, restart invalidation, and shutdown closure.
- Web exposes profile-authenticated screen discovery/advertising/session routes, an owner-authenticated connected-screen route, and version 1 receiver/control WebSockets. WebSocket upgrades require the exact request origin before upgrade.
- The frontend `ScreenCoordinator` owns discovery, receiver and controller connections, versioned commands, and visible ready/connecting/connected/receiving/disconnected/error states. Playback-adjacent controls advertise this browser, choose another screen, start, pause, seek to start, and hand off. The owner view lists connected screens.
- `main` composes one runtime screen manager and closes it before HTTP shutdown, so upgraded connections do not outlive process shutdown. No screen or control authority persists across restart.

## Focused evidence

- `go test ./screens ./web` and `go test -race ./screens ./web` pass.
- `go test ./...` passes.
- Frontend lint and TypeScript checks pass.
- Focused coordinator, Player, and Owner Vitest tests pass.

## Unmet proof

- A two-real-browser-context test against the embedded executable and playable fixture was not completed in this bounded implementation pass.
- The existing complete frontend Vitest run has one setup-heading timing failure in `Browse.test.tsx:85`, which asserts headings before the catalog loads. The affected Browse test passed when rerun with the app test; this does not establish the full run as passing.
- Named viewport visual and accessibility evidence for the new chooser remains to be captured before publication.
