# React viewer architecture validation

## Scope

Validated the accepted plan in `docs/features/frontend-ui-architecture/plan.md` against `DESIGN.md` and the existing Editorial Stream and Poster Theater behavior. No backend API, database, deployment, demo volume, or port `18789` change was made.

## Mismatch ledger

| Mismatch | Shared cause | Resolution | Evidence |
| --- | --- | --- | --- |
| Rail card width and JavaScript stride disagreed after responsive measurement | CSS card width changed while TanStack Virtual retained fallback size estimates | `MediaCollection` measures the container, owns card width and 16px gap, and resets virtual measurements when rail stride or grid row height changes | MediaCollection geometry/focus tests; real-browser adjacent rail and grid gap assertions before and after viewport resize |
| Grid rendered every catalog item | Grid bypassed virtualization | Grid virtualizes rows and keeps a bounded card DOM | 10,000-item component boundary test; 1,000-item browser scroll test in three engines |
| Directional focus used document-level card queries | Focus behavior lived outside collection ownership | Arrow movement is local to the active collection; route/player restoration uses one catalog-ID helper | component keyboard tests; embedded-production focus restoration |
| Search input, URL, and popstate could diverge | Browse mirrored route state and read `window.location` | App passes the parsed destination query; typing uses debounced `replaceState` | app history/query test |
| Old request responses and failed writes could replace valid state | Request ordering and mutation errors shared one page-level failure state | Latest-request guards and scoped preference/My List alerts | delayed-response and failed-write tests |
| Bright hero artwork could paint above the text protection layer | Negative scrim stacking | Hero creates an isolated stack with artwork, scrim at layer 0, and content at layer 1 | original bright SVG fixture and three-browser screenshot test |
| Wordmark markup drifted across features | Product identity was duplicated | Setup, profiles, owner, and viewer use `productChrome/Wordmark` | shared Wordmark test and source search |
| Unknown mocked HTTP calls silently returned `200 {}` | Mock routers had permissive defaults | App and Browse unit mocks reject unknown requests through `strictFetch` or explicit failures; the browser router returns status 599 with request details | strict router test; browser matrix exposed and repaired missing lifecycle routes |

## Automated evidence

- `npm run lint` — passed with `react-hooks/rules-of-hooks` and `react-hooks/exhaustive-deps` enabled.
- `npm run typecheck` — passed.
- `npm test` — 39 tests passed across 11 files.
- `npm run build` — passed.
- `npm run test:bundle` — passed; Player and hls.js remain outside the entry chunk.
- Mocked Playwright matrix — 30 tests passed in Chromium, Firefox, and WebKit, including a real-browser 1,000-item virtualized grid scroll and responsive rail/grid gap regression. During implementation, the strict router exposed an undeclared catalog request after Player exit; the route was declared before the final green matrix.
- Embedded-production Chromium — passed setup, scan, profile, browse, direct play, compatibility play, focus restoration, and detail flow against a freshly built `flixr` executable on temporary port `18790`.
- Bright artwork evidence — passed in Chromium, Firefox, and WebKit using an original test-only SVG. The artifact remains under ignored `frontend/test-results/`.
- `git diff --check` — passed.

## Residual risks

- React StrictMode remains intentionally disabled. Player lifecycle tests now cover final heartbeat/stop, pagehide beacon, stale post-import HLS attachment rejection, compatibility seek payload, and fatal HLS failure, but effect replay is not yet an accepted runtime contract.
- The known hls.js chunk-size warning remains. The bundle gate confirms that it is lazy and outside the initial entry.
- Grid virtualization uses an internal vertical scroll region. Browser evidence covers the accepted viewports; future remote-control work should revalidate focus when navigating beyond the rendered row window.
- Generated Playwright output and embedded assets remain ignored and are not publication content.
