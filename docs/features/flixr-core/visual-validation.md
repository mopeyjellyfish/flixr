# Delivery Units 1–2 visual validation

## Authority

- Accepted direction: `DESIGN.md` — Cobalt Signal.
- Product states: setup, owner operations, profile selection, home, search, film detail, series detail, local-only metadata, partial scan, request failure, direct playback, remux playback, compatibility transcode, pause, resume, and capacity failure.
- Media and artwork: generated synthetic fixtures and original geometric SVG artwork only.

The local Chromium, Firefox, and WebKit mocked matrix and embedded-production Chromium acceptance passed for Delivery Unit 2. The Delivery Unit 1 matrix also passed in GitHub Actions run [`33285726565`](https://github.com/mopeyjellyfish/flixr/actions/runs/33285726565), which uploaded the browser evidence artifacts.

## Evidence matrix

| Route or flow | Viewport | State and interaction | Evidence | Status |
| --- | --- | --- | --- | --- |
| `/setup` → `/owner` | 1920×1080, 1440×900, 1024×768, 390×844 | Claim the first owner, including missing-tool readiness | Local Chromium and CI Chromium/Firefox/WebKit mocked matrix | Pass |
| `/profiles` | All four named viewports | Select an open profile; reject an invalid PIN; show rate limiting | Local Chromium and CI Chromium/Firefox/WebKit mocked matrix | Pass |
| `/home` | All four named viewports | Populated film and series rails; focal title and visible details action; film and ordered episode details; close and restore focus | Local Chromium `populated-home.png` output and CI Chromium/Firefox/WebKit artifacts | Pass |
| `/search` | All four named viewports | Empty query, no results, and one result after debounce | Local Chromium and CI Chromium/Firefox/WebKit mocked matrix | Pass |
| `/owner` | All four named viewports | Missing ffprobe, persisted roots, configured TMDB state, and partial scan | Local Chromium and CI Chromium/Firefox/WebKit mocked matrix | Pass |
| `/home` | All four named viewports | Local-only metadata and request failure | Local Chromium and CI Chromium/Firefox/WebKit mocked matrix | Pass |
| Production executable | 1920×1080, 1440×900, 1024×768, 390×844 Chromium | Claim, save roots, scan four generated media files, create/select a profile, capture focal home, reload a series deep link, use browser Back, and search | `frontend/e2e/production.spec.ts`; `production-home-*.png`; `production-search-desktop.png` | Pass |
| Production playback | 1440×900 and 390×844 Chromium | Capability-planned direct MP4, Matroska remux with in-window seek, MPEG-4/MP3 AVI to H.264/AAC transcode, native controls, explicit actions, local status, and no horizontal overflow | `production-direct-playback-desktop.png`; `production-direct-playback-phone.png`; `production-remux-playback-desktop.png`; `production-transcode-playback-desktop.png` | Pass |
| Mocked player | 1920×1080, 1440×900, 1024×768, 390×844 Chromium/Firefox/WebKit | Axe, overflow, console/page errors, direct seek intent, buffering, paused heartbeat, cross-client resume, stop, expired authority, recovery, network interruption, and capacity | `frontend/e2e/mock-api.spec.ts`; `player-*.png` | Pass |
| Production executable | 1440×900 and 390×844 Chrome | Existing durable catalog with one generated film and a two-episode generated series | Manual browser capture and accessibility snapshot | Pass |

The Playwright screenshots are generated under ignored `frontend/test-results/` paths. CI uploads the mocked and production evidence as separate artifacts. It does not publish `.playwright-cli/**` or review-only artwork.

## Checks

- Cobalt hierarchy, Manrope headings, Atkinson Hyperlegible Next interface text, near-black canvas, cobalt actions, white focus, and green local status are visible.
- The focal action stays visible at every named viewport.
- Rails use bounded DOM virtualization. The 10,000-item unit proof does not render the full catalog.
- Film and series dialogs use catalog IDs only. The series dialog shows ordered seasons and episodes without filesystem paths.
- Dialog close, Escape, and browser Back restore a coherent route and focus state.
- Axe reports no serious or critical violations on the focal home, owner, local-only, request-failure, production home, and production search states.
- The tests report no page errors or console errors on the checked flows.
- The tests report no horizontal document overflow at the four named viewports or on the production path.
- Touch controls keep a minimum 44 px target. Reduced-motion CSS removes effective transition and animation duration.
- The player keeps the Cobalt Signal hierarchy at desktop and phone widths, scales media within a 16:9 stage, exposes native controls plus large explicit actions, and identifies the stream as local.
- The bundle gate keeps `Player` and `hls.js` in separate lazy chunks and rejects player implementation text in the initial entry chunk.
- Browser lifecycle evidence is paired with profile-bound backend contract tests for direct 206 ranges, HLS asset authority, seek replacement/reuse, lease expiry, and durable progress.

## Mismatch ledger

| Severity | Observed evidence | Accepted expectation | Cause | Resolution and recheck |
| --- | --- | --- | --- | --- |
| High | A fresh durable catalog returned `items: null` and the home route rendered a blank page. | Empty libraries show a deliberate empty state. | The SQLite browse result used a nil slice, and the client trusted the wire value. | Resolved in backend serialization and frontend normalization. Rechecked through the production executable. |
| High | Browser Back from a reloaded detail route left the dialog mounted; a query-string navigation could enter the loading route. | URL, modal state, Back, and search remain synchronized. | Route state tracked only the route class and parsed a path that still contained the query. | Resolved with location-path state and query-safe parsing. Rechecked in unit and production Playwright tests. |
| Medium | The native dialog appeared at the upper-left edge on desktop. | Details are a centered, bounded cinema overlay. | The dialog had no explicit automatic margin or viewport height bound. | Resolved with centered margin, bounded width/height, and scrolling. Rechecked at 1440×900 and 390×844. |
| Medium | Early mocked captures used broken artwork URLs and showed only the fallback gradient. | Visual evidence must use safe inspectable artwork. | The mock referenced files that did not exist. | Resolved with original inline geometric SVG artwork. Rechecked in all four generated home captures. |
| Low | A real local-only title has no provider artwork. | Offline records remain usable without enrichment. | No TMDB credential or cached provider match exists. | Expected fallback, not an open mismatch. Cards preserve kind, title, year when known, and local state. |
| High | FFmpeg could run faster than playback while deleting old segments from a sliding manifest. | A viewer can play the full bounded compatibility stream without FFmpeg outrunning the lease. | The command omitted realtime input pacing. | Resolved with `-re`; rechecked by real FFmpeg remux/transcode integration and production playback. |
| High | FFmpeg input depended on the public application URL and failed for TLS or non-loopback binds. | FFmpeg reads only a server-generated loopback URL independent of the LAN listener. | Playback reused the public listener address. | Resolved with a separate ephemeral loopback listener that shares the authenticated application handler. Rechecked through the embedded executable. |
| Medium | The first player capture used the video element's 320×180 intrinsic size and unstructured controls. | Playback is deliberate, legible, and responsive under Cobalt Signal. | Delivery Unit 2 initially had no player-specific layout. | Resolved with a bounded 16:9 stage, responsive header and toolbar, local-stream status, focus-safe actions, and desktop/phone production captures. |
| High | An owner-selected segment path could make startup cleanup remove unrelated directories. | Cleanup removes only Flixr-owned orphan generation directories. | The first cleanup accepted any absolute directory and removed every child directory. | Resolved with an ownership marker, refusal of non-empty unowned paths, token-shaped generation names, and lock-before-cleanup tests. |
| High | Nominal four-second arithmetic could misclassify a variable-duration remux segment as retained. | Reuse and seek follow the actual manifest window. | Remux boundaries follow source keyframes. | Resolved by accumulating observed `EXTINF` durations and forcing four-second transcode keyframes. Variable-duration sliding-manifest tests pass. |
| Medium | Playback shutdown drained the long-lived FFmpeg input response before terminating FFmpeg. | Signal shutdown remains bounded with an active compatibility stream. | Private-listener shutdown preceded generation shutdown. | Resolved by stopping public requests, terminating playback while loopback input remains available, then draining the private input listener. |
| Medium | Initial player proof omitted accessibility, lifecycle states, focus return, and lazy-bundle enforcement. | The signature player meets the same responsive, accessible, and bounded-loading contract as browse. | The first acceptance path proved startup only. | Resolved with four-viewport axe/overflow evidence, lifecycle state tests, deterministic focus entry/return, and the bundle boundary gate. |

## Result

The Delivery Unit 1 and Delivery Unit 2 mismatch ledger has no open blocker. The local Chromium/Firefox/WebKit mocked matrix, real FFmpeg media integration, and embedded production-executable direct/remux/transcode acceptance passed. CI preserves the mocked and production browser artifacts.
