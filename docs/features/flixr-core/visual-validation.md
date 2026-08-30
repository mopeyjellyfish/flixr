# Delivery Unit 1 visual validation

## Authority

- Accepted direction: `DESIGN.md` — Cobalt Signal.
- Product states: setup, owner operations, profile selection, home, search, film detail, series detail, local-only metadata, partial scan, and request failure.
- Media and artwork: generated synthetic fixtures and original geometric SVG artwork only.

Local Chromium mocked and production-executable evidence passed. Firefox and WebKit projects are defined in CI, but their runs are pending. No Firefox or WebKit pass is claimed until CI uploads an artifact.

## Evidence matrix

| Route or flow | Viewport | State and interaction | Evidence | Status |
| --- | --- | --- | --- | --- |
| `/setup` → `/owner` | 1920×1080, 1440×900, 1024×768, 390×844 | Claim the first owner, including missing-tool readiness | Local Chromium mocked pass; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| `/profiles` | All four named viewports | Select an open profile; reject an invalid PIN; show rate limiting | Local Chromium mocked pass; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| `/home` | All four named viewports | Populated film and series rails; focal title and visible details action; film and ordered episode details; close and restore focus | Local Chromium `populated-home.png` output; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| `/search` | All four named viewports | Empty query, no results, and one result after debounce | Local Chromium mocked pass; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| `/owner` | All four named viewports | Missing ffprobe, persisted roots, configured TMDB state, and partial scan | Local Chromium mocked pass; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| `/home` | All four named viewports | Local-only metadata and request failure | Local Chromium mocked pass; Firefox/WebKit CI projects pending | Local Chromium Pass; CI pending |
| Production executable | 1920×1080, 1440×900, 1024×768, 390×844 Chromium | Claim, save roots, scan three generated media files, create/select a profile, capture focal home, reload a series deep link, use browser Back, and search | `frontend/e2e/production.spec.ts`; `production-home-*.png`; `production-search-desktop.png` | Pass |
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

## Mismatch ledger

| Severity | Observed evidence | Accepted expectation | Cause | Resolution and recheck |
| --- | --- | --- | --- | --- |
| High | A fresh durable catalog returned `items: null` and the home route rendered a blank page. | Empty libraries show a deliberate empty state. | The SQLite browse result used a nil slice, and the client trusted the wire value. | Resolved in backend serialization and frontend normalization. Rechecked through the production executable. |
| High | Browser Back from a reloaded detail route left the dialog mounted; a query-string navigation could enter the loading route. | URL, modal state, Back, and search remain synchronized. | Route state tracked only the route class and parsed a path that still contained the query. | Resolved with location-path state and query-safe parsing. Rechecked in unit and production Playwright tests. |
| Medium | The native dialog appeared at the upper-left edge on desktop. | Details are a centered, bounded cinema overlay. | The dialog had no explicit automatic margin or viewport height bound. | Resolved with centered margin, bounded width/height, and scrolling. Rechecked at 1440×900 and 390×844. |
| Medium | Early mocked captures used broken artwork URLs and showed only the fallback gradient. | Visual evidence must use safe inspectable artwork. | The mock referenced files that did not exist. | Resolved with original inline geometric SVG artwork. Rechecked in all four generated home captures. |
| Low | A real local-only title has no provider artwork. | Offline records remain usable without enrichment. | No TMDB credential or cached provider match exists. | Expected fallback, not an open mismatch. Cards preserve kind, title, year when known, and local state. |

## Result

The Delivery Unit 1 mismatch ledger has no open blocker in the passed local Chromium mocked and production evidence. Firefox and WebKit CI evidence remains pending; the automated suites are defined to run against the production-style embedded executable for acceptance behavior.
