---
status: accepted
---

# Plan: React viewer architecture improvements

This plan delivers all seven accepted Blueprint Ledger findings without changing Flixr product behavior or its accepted Editorial Stream and Poster Theater direction. It improves request ownership, browser-history correctness, reusable media collection behavior, hook safety, test strictness, player lifecycle evidence, and proven Cobalt Signal chrome. It does not add a router dependency, a generic component kit, a one-consumer viewer service, or a speculative Player controller.

## Accepted architecture evidence

- **C-003:** one feature-local `MediaCollection` module owns rail/grid geometry, virtualization, poster loading, and directional focus.
- **C-007:** React hook lint guards every feature; findings are fixed without blanket suppressions. React StrictMode remains deferred until Player effect replay is explicitly safe.
- **C-001:** the URL owns search destination state. Input and results follow popstate, and typing uses debounced history replacement rather than one entry per keystroke.
- **C-006:** UI HTTP tests reject unknown calls, use current `ViewerModel` fixtures, and make overflow and seek assertions falsifiable.
- **C-002:** viewer requests use explicit latest-request and scoped-mutation error policy inside the browse feature. No one-consumer session/controller abstraction is introduced.
- **C-004:** one Wordmark and only repeated surface, border, and artwork-scrim tokens become shared Cobalt Signal chrome. Tailwind preflight changes remain out of scope.
- **C-005:** direct Player behavior tests prove HLS attach/error guards, pagehide progress, seek payload, final heartbeat, and stop ownership before any extraction is considered.
- **Interface authority:** `DESIGN.md`, Editorial Stream, Poster Theater, and the accepted viewer plan remain normative. Product behavior, copy intent, API contracts, lazy Player loading, and initial bundle boundaries remain unchanged.

## Review evidence

- **Applicability:** not applicable. This delivery changes React, TypeScript, CSS, frontend tests, and frontend build tooling only. It does not change Go code or public backend contracts.
- **Fixed document:** `docs/features/frontend-ui-architecture/plan.md` as approved for accept-all execution.
- **Status:** not applicable; no Go specification review is required.
- **Invalidation:** React/frontend-only scope remains fixed. Any backend contract, product behavior, visual direction, topology, or deployment change requires renewed approval.

## Execution mode

Accept-all implementation. Whole-plan approval authorizes continuous implementation of all five slices, their bounded atomic commits, fixed-diff review, and pull-request publication for this named plan. It does not authorize merge, release, deployment, port `18789` refresh, Docker demo reset, or unrelated work.

## Delivery topology

| Delivery unit | Topology | Stack position | Branch | Pull request base | Dependencies | Checks | Ownership | Integration point | CI fan-out | Cascade cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | sequential stack | 4/4 | `refactor/frontend-ui-architecture` | `feat/viewer-experience` / PR #4 | PR #4 | frontend lint/type/test/build/bundle, affected mocked browser matrix, embedded-production Chromium smoke where shared viewer behavior changes | one writer in `/Users/david/code/personal/flixr/.worktrees/refactor-frontend-ui-architecture` | PR #4 head | existing frontend and production CI; no new job | Medium: changes to PR #4 can require a rebase and repeated visual proof |

The plan and implementation share one pull request. The findings have one coherent review value: they make the shipped viewer architecture safer without changing backend contracts. Splitting them into sibling pull requests would repeatedly edit `Browse.tsx`, tests, CSS, and browser fixtures and would create more cascade cost than review value.

## Critical path, dependencies, and lanes

Critical path: strict test seams and Player lifecycle proof → truthful URL routing and hook lint → coordinated viewer request policy → unified media collection → shared Cobalt chrome → full visual and production validation.

There is one serial lane and one sole writer. Parallel writers would overlap `Browse.tsx`, `App.tsx`, test fixtures, and `style.css`. The forecast is five slices, one delivery unit, one pull request, four focused frontend gates, and one expensive affected browser/production gate at the stable boundary.

Invalidation map:

- Test-router or fixture changes invalidate affected Vitest and mocked Playwright tests.
- App routing or search changes invalidate app tests plus search history, detail-return, and navigation browser journeys.
- Hook dependency or cleanup changes invalidate lint, the owning feature tests, and affected browser lifecycle checks.
- Browse request-policy changes invalidate Browse tests, strict request fixtures, My List, preference, detail, search, and failure-state browser journeys.
- Media collection or focus changes invalidate Browse tests, keyboard/focus journeys, DOM-bound assertions, responsive screenshots, axe checks, and overflow checks.
- Wordmark, surface, border, or scrim changes invalidate profile/setup/owner/viewer visual states at TV, desktop, tablet, and phone viewports.
- Player lifecycle changes invalidate Player component tests, mocked playback browser tests, lazy bundle proof, and embedded-production playback acceptance.

## [x] 001 — Make frontend proof strict and cover Player lifecycle ownership

### Outcome and requirement trace

Unexpected UI HTTP calls, wrong methods, and wrong payloads fail with a named unmatched-request error instead of receiving `200 {}`. Unit and browser fixtures use the current `ViewerModel` shape. Overflow assertions detect real document overflow independently of CSS clipping, and compatibility-stream seek assertions verify the server request payload rather than only local `video.currentTime`.

Player component tests directly prove initial plan attachment, stale async HLS attachment rejection, fatal HLS visible failure, pagehide heartbeat beacon position, compatibility seek payload, explicit exit heartbeat-before-stop, unmount cleanup, and no duplicate finalization. The Player stays one component unless this proof exposes a smaller interface with clear independent value.

### Seam and files

- Test-only strict request utilities under `frontend/src/test/` and/or `frontend/e2e/support/`, with explicit path, method, query, and body matching.
- Current viewer fixtures shared only within the test layer.
- `frontend/src/app.test.tsx`, `frontend/src/features/browse/Browse.test.tsx`, and `frontend/e2e/mock-api.spec.ts` migrate away from permissive catch-all success responses and legacy viewer normalization.
- New focused `frontend/src/modules/mediaPlayer/Player.test.tsx` with bounded media, beacon, timer, dynamic HLS, and API fakes.
- Production `Player.tsx` changes only when a failing lifecycle test proves a defect. No controller extraction and no React StrictMode enablement are part of this slice.

### Dependencies

Accepted Blueprint Ledger findings C-006 and C-005.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

First add one failing proof at a time: an unknown request incorrectly succeeds; a wrong seek payload remains green; omitted pagehide heartbeat or stop remains green; a stale HLS import can attach after source replacement; a fatal HLS event has no direct test. Each red test must fail for the intended behavior, not for fixture setup order.

### Green proof and checks

Focused Vitest tests pass for each strict router and Player lifecycle behavior. Existing 24 tests remain green. Mocked playback browser checks still pass. Keep visible-behavior assertions independent of private request order where order is not part of the contract. Record any browser/media API limitation as a residual risk rather than weakening the assertion.

### Atomic commit and pull request

Atomic commit: strict frontend test seams and Player lifecycle proof. Delivery unit 1.

### Done when

Unknown HTTP calls and incorrect playback side effects fail deterministically, current fixtures replace dead compatibility shapes, and Player lifecycle ownership is directly observable without a speculative extraction.

## [x] 002 — Make navigation URL-owned and enforce hook safety

### Outcome and requirement trace

Search input, query results, and browser history always describe the same destination. Typing a query does not create one history entry per keystroke. Back and forward navigation between two searches update both input and results. Browse mode is derived from App routing rather than mirrored in component state.

ESLint enables the current React hooks recommendations. All findings are resolved through stable callbacks, correct dependencies, or narrower effect ownership without blanket suppressions. React StrictMode remains off during this delivery.

### Seam and files

- `frontend/src/app/App.tsx` parses a browse destination containing mode and decoded search query from the current path/search string, passes it to Browse, and keeps the existing small history router.
- `frontend/src/features/browse/Browse.tsx` receives query as route state, derives mode, debounces search URL replacement, and does not read `window.location` for initial feature state.
- `frontend/src/app.test.tsx` and Browse tests cover popstate, back/forward, query synchronization, and history length.
- `frontend/eslint.config.js`, `frontend/package.json`, and lockfile add and configure the React hooks ESLint plugin.
- Hook findings in App, Browse, Setup, Profiles, Owner, and Player are fixed only as required by the rules.

### Dependencies

Slice 001 strict test seams.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

A focused navigation test types `Signal`, proves history length grows incorrectly, changes to another search, then dispatches back/forward and initially observes stale input/results. A lint run initially reports the existing unguarded hook dependency risks.

### Green proof and checks

Focused app/Browse history tests pass; lint passes with React hook rules active; typecheck and all Vitest tests pass. Existing Home/Movies/TV/detail/player return paths remain unchanged. Any effect repair that touches Player reruns Slice 001 lifecycle tests.

### Atomic commit and pull request

Atomic commit: truthful search routing and React hook safety. Delivery unit 1.

### Done when

The URL is the single source of search destination state, navigation history is truthful, and hook dependency drift is automatically rejected.

## [x] 003 — Consolidate viewer request and mutation policy

### Outcome and requirement trace

Latest list, search, and detail requests win. A delayed old response cannot replace newer visible state. Direct detail routes avoid the current avoidable detail waterfall. Search does not reload all viewer rows for every settled query. Preference and My List write failures stay scoped to the affected control or dialog and do not replace a valid catalog with `Catalog unavailable`.

Server ordering remains authoritative. The client patches only the visible listed flag and the fixed My List section when this avoids a full reload; it does not invent genre, sorting, or row-order policy.

### Seam and files

- Feature-local request identities, abort/cancellation ownership, or equivalent latest-result guards in `frontend/src/features/browse/Browse.tsx` and small sibling files only when they create a materially better public test seam.
- Detail loading uses one detail route appropriate to the item/destination and ignores stale responses.
- Search listed state uses the already-held viewer/list state or one bounded list source rather than reloading all viewer rows on every query.
- Preference writes retain the valid model on failure and expose a scoped actionable error near controls.
- My List writes retain the valid catalog and expose a scoped dialog error; successful writes update all currently visible instances consistently.
- No `viewer session`, repository, controller, global store, or backend contract is introduced.

### Dependencies

Slices 001 and 002.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

Focused tests delay and reverse two detail responses, two destination loads, and two searches; they initially expose stale replacement. Separate tests reject preference and list writes and initially observe page-level catalog failure. A request-count assertion initially proves search reloads the full viewer model after each settled query.

### Green proof and checks

Focused Browse tests pass for latest-request-wins, direct detail load, bounded search requests, scoped write failures, My List consistency, and server-owned ordering. Strict request fixtures declare every request. Run lint, typecheck, all Vitest tests, and affected mocked browser journeys for search, detail, preferences, list mutation, retry, and focus restoration.

### Atomic commit and pull request

Atomic commit: feature-local viewer request policy. Delivery unit 1.

### Done when

Slow or failed requests cannot corrupt unrelated visible state, and the feature owns request transitions without a speculative service layer.

## [x] 004 — Give one media collection module layout, virtualization, and focus ownership

### Outcome and requirement trace

One `MediaCollection` interface accepts layout (`rail` or `grid`), label, items, and open action. Callers do not know card stride, transforms, overscan, focus data attributes, or poster loading mechanics. Rail and grid use measured responsive geometry from the same source. Both layouts keep a bounded DOM for large catalogs. Native buttons, visible focus, deterministic directional movement, focus restoration, and accepted poster-card content remain intact.

Phone cards no longer show the observed extra geometry gap. TV cards retain metadata. Focus rings do not clip. Intended card gaps are 16px. Poster images use browser loading behavior rather than CSS-only background fetches where compatible with the accepted tonal demo treatment and text scrim.

### Seam and files

- New focused module under `frontend/src/modules/mediaCollection/` owns `MediaCollection`, media-card rendering, virtualization, responsive measurement, and local directional focus.
- `frontend/src/features/browse/Browse.tsx` renders search results, Home sections, and grid through that public module.
- `frontend/src/modules/focusManager/rail.ts` is folded into the module or reduced to a module-private helper; global document queries are removed from directional movement.
- `frontend/src/style.css` keeps presentation and responsive rules but no longer contradicts JavaScript stride or uses grid `!important` rules to undo rail transforms.
- Focused unit/component tests cover keyboard movement, bounded node count, measurement changes, poster loading semantics, and focus restoration.

### Dependencies

Slice 003 stable viewer state contract.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

A viewport-geometry test initially exposes JS/CSS stride disagreement and the phone gap. A large-grid test initially renders the full catalog. A focus test initially demonstrates reliance on global card queries or clipped edge focus. A poster test initially cannot observe native lazy image loading.

### Green proof and checks

Focused MediaCollection and Browse tests pass. At TV 1920×1080, desktop 1440×900, tablet 1024×768, and phone 390×844, browser evidence proves no clipped card metadata or focus, intended 16px gaps, no horizontal document overflow, and bounded rail/grid nodes. Keyboard and directional paths remain deterministic. Axe reports no serious or critical violations. Run Chromium during iteration and Chromium/Firefox/WebKit at the stable visual boundary.

### Atomic commit and pull request

Atomic commit: unified virtualized media collection. Delivery unit 1.

### Done when

Rail and grid callers use one deep media-collection interface and responsive layout, loading, virtualization, and focus behavior have one owner.

## [x] 005 — Consolidate proven Cobalt Signal chrome and validate the fixed delivery

### Outcome and requirement trace

Setup, profiles, owner, and viewer use one shared `FlixR` wordmark with white `Flix` and cobalt `R`. Only repeated Cobalt Signal surface, border, and artwork-scrim values become semantic tokens. Hero and poster text remain protected over tonal and bright artwork. No generic component kit, spacing scale, radius scale, Tailwind preflight change, or visual redesign is introduced.

The complete delivery preserves accepted behavior, lazy Player loading, hls.js separation, accessibility, responsive composition, and production embedding.

### Seam and files

- A small shared product-chrome module for `Wordmark` only.
- `frontend/src/features/setup/Setup.tsx`, `frontend/src/features/profiles/Profiles.tsx`, `frontend/src/features/owner/Owner.tsx`, and Browse reuse it.
- `frontend/src/style.css` promotes only repeated surface, border, and artwork-scrim values and fixes hero scrim stacking.
- Add a test-only bright artwork fixture for contrast and stacking evidence; do not publish third-party artwork.
- Update frontend tests and visual evidence. Update durable design documentation only if implementation evidence resolves a previously unspecified normative token; otherwise leave `DESIGN.md` unchanged.

### Dependencies

Slices 001–004.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

A focused rendering test initially finds inconsistent wordmark markup across features. A bright-artwork browser state initially exposes missing or incorrectly stacked text scrim protection.

### Green proof and checks

- Focused component tests for shared wordmark and artwork semantics.
- ESLint with React hook rules, TypeScript, all Vitest tests, production build, and bundle boundary checks.
- Affected mocked Playwright matrix in Chromium, Firefox, and WebKit.
- Named visual states at TV, desktop, tablet, and phone: Home rows, Movies grid, TV grid, search, detail, profile chooser, setup, owner, demo artwork, and bright artwork.
- Accessibility: semantic controls, `aria-current`, 44px targets, visible focus, reduced motion, contrast, no hover-only actions, and no horizontal document overflow.
- Embedded-production Chromium smoke for setup/profile/viewer navigation, search/detail return, direct playback, compatibility playback, and console/network cleanliness.
- Fixed-diff review against this accepted plan and `DESIGN.md`; generated reports and Playwright output remain unpublished.

### Atomic commit and pull request

Atomic commit: shared Cobalt Signal product chrome and final validation evidence. Delivery unit 1. After fixed-snapshot review, publish one stacked pull request from `refactor/frontend-ui-architecture` to `feat/viewer-experience` using `open-pr`. Do not merge, release, deploy, reset demo data, or refresh port `18789`.

### Done when

All seven findings have objective proof, accepted UI behavior is unchanged, visual mismatches are resolved or recorded, required gates pass, and the reviewed branch is ready for a stacked pull request.
