# Specials and alternate TV numbering implementation plan

> **For agentic workers:** Use the installed subagent-driven-development and test-driven-development workflows. Backend and frontend ownership are disjoint; coordinate through the contract below. Do not delegate further.

**Goal:** Complete issue #42: explicit numeric spans, owner-selected aired/DVD/absolute order, repair of ambiguous mappings, and ordered Next Up without changing media identity or viewing history.

**Architecture:** Persist parsed source spans separately from a series' selected viewing order. Owner changes replace a revisioned mapping transactionally; the catalog remains the authority for browse order and Next Up. Provider groups are explicitly previewed, then saved locally, and are never needed for startup or playback.

**Tech stack:** Existing Go/SQLite catalog, HTTP API, React/TypeScript owner and browse UI, Vitest/Playwright.

**Spec:** https://github.com/mopeyjellyfish/flixr/issues/42 (all acceptance preserved).

## Global constraints

- Ship on 0.x. Preserve owner/profile authorization, local-only startup and media-root confinement.
- Never mutate catalog IDs, progress/history, media-version memberships, or canonical source season/episode when choosing a display order.
- Source spans and selected order survive restart, rescan, missing mounts and cancellation. Apply mapping changes in one context-aware transaction; optimistic revision rejects stale writes.
- Missing/overlapping/non-contiguous assignments require owner repair; do not silently infer DVD or absolute mappings. Duplicate coordinates in distinct existing edition/library contexts remain valid.
- Retain existing specials opt-in, playable/authorized filtering and version context. Multi-episode files advance past the entire mapped span.
- No production data edits or credential use in fixtures. No new dependency is necessary.

## Shared wire contract

`EpisodeOrderPosition`: `{position:number,end_position:number,season:number,episode:number,episode_end:number,special:boolean}`. Positions are positive integers; end >= start. Display season >= 0, episode >= 1, end >= episode. A file maps to one contiguous span. Canonical `CatalogItem.season/episode` remain source coordinates; add `episode_end?:number`, `absolute_episode?:number`, `episode_order?:EpisodeOrderPosition`, `order_needs_repair?:boolean`.

`EpisodeOrderEntry`: `{catalog_id:string,title:string,season:number,episode:number,episode_end:number,absolute_episode?:number,mapping?:EpisodeOrderPosition}`. Source coordinates and title in write payloads are informational; server validates IDs and derives source evidence itself.

`EpisodeOrderDetail`: `{series_id:string,title:string,order:'aired'|'dvd'|'absolute',revision:number,needs_repair:boolean,entries:EpisodeOrderEntry[]}`.

`EpisodeOrderGroup`: `{id:string,name:string,order:'aired'|'dvd'|'absolute'}`.

Owner-only routes (normal session/CSRF protections):

- `GET /api/v1/owner/episode-orders?q=&offset=0&limit=50` -> `{series:[{id,title}],total:number,next_offset?:number}`; local-only, bounded discovery, includes matched and unmatched series.
- `GET /api/v1/owner/episode-orders/{id}` -> `EpisodeOrderDetail`; no network.
- `PUT /api/v1/owner/episode-orders/{id}` body `{order,revision,entries}` -> `EpisodeOrderDetail`. Missing mappings may be saved as repair-required; malformed numbers/foreign IDs/stale revision reject atomically. Explicit aired mode with empty entries restores parsed source order. Nonempty aired entries support owner repairs.
- `GET /api/v1/owner/episode-orders/{id}/groups` -> `{groups:EpisodeOrderGroup[]}`; explicit provider lookup, bounded/cancellable. Missing credentials/provider support returns a useful error, without changing state.
- `POST /api/v1/owner/episode-orders/{id}/preview` body `{group_id:string}` -> `EpisodeOrderDetail`; validates group belongs to matched series and maps unique contiguous source spans. Preview does not save. Entries are sufficient for offline manual repair after import/save.

Errors: `episode_order_invalid` (400), `episode_order_conflict` (409), `catalog_not_found` (404), `metadata_unavailable` (503), normal `owner_required`/CSRF failures.

Public series adds `episode_order?:'aired'|'dvd'|'absolute'` and `order_needs_repair?:boolean`; retains source seasons and item fields. Each mapped item carries `episode_order`; UI can flatten mapped episodes by position for alternate order without changing season bulk actions. Unmapped items stay visible for manual playback with an honest warning. Next Up uses the saved mapping rather than rendered labels.

Provider facts: TMDB lists series groups at `/3/tv/{series_id}/episode_groups` and details at `/3/tv/episode_group/{id}`. Types 1,2,3 are original air date, absolute, DVD respectively. References: https://developer.themoviedb.org/reference/tv-series-episode-groups and https://developer.themoviedb.org/reference/tv-episode-group-details . Use trusted configured origin, bounded response/body/counts and request contexts.

## Task 1: Catalog, persistence, provider preview and protected API

Files: backend/catalog/episode_order.go and tests; catalog.go, queries.go, next_episode.go, identity.go/media_versions.go where required; backend/catalog/tmdb_episode_order.go and tests; backend/sqlite/migrations/032_episode_order.sql and migration tests; backend/web/episode_order.go and tests/server route registration.

- [x] Add failing parser fixtures: E100/E1000, S00E01, S01E01E02, S01E01-E03, absolute naming, malformed/descending spans. Verify baseline failures, then implement explicit parsing and persistence. Never silently truncate an unsupported span.
- [x] Add DB upgrade/reopen tests and canceled-write tests. Persist source spans without rewriting historical identities; hydrate legacy records safely.
- [x] Add failing order tests selecting aired versus DVD/absolute; mapping 1–2 advances to 3; incomplete/overlapping mapping returns context_unavailable; owner repair restores progression; ordering preserves IDs/history and authorized edition context.
- [x] Implement revisioned detail/save and shared sequence projection for persistent normal-mode catalogs. Memory-only fixture catalogs retain parsed source ordering. Bound all owner requests; reject invalid IDs/numbers and stale writes before commit.
- [x] Add httptest provider fixtures for TMDB group types, reordered groups, duplicate/missing episodes, cancellation and provider errors. Implement explicit preview with series-group membership validation; never fetch during startup/playback.
- [x] Add owner/CSRF/API tests for every route and profile visibility/Next Up tests. Keep path-free responses.
- [x] Run `go test ./catalog ./sqlite ./web`, targeted race tests, `go vet ./...`, and report exact outputs. No commits until the coordinator integrates and reviews the complete work.

## Task 2: Owner order editor and viewer order display

Files: frontend/src/features/owner/EpisodeOrder.tsx and tests; Owner.tsx integration; core/api.ts and api/client.ts; Browse.tsx and tests; e2e/episode-order.mock-api.spec.ts; relevant existing mock defaults.

- [x] Add failing owner tests: list/load a series, edit positions and display coordinates, switch mode, preview provider mapping, save, show conflict/provider failure, preserve draft on failure, cancel/reload without saving.
- [x] Implement a bounded searchable series picker under owner Metadata. Load on explicit action so opening unrelated settings introduces no mandatory API call. Provide aired/DVD/absolute selection and explicit provider-group preview. Human-readable per-file rows with numeric order/display span fields and a special checkbox; no JSON editor.
- [x] Save using revision and server detail response; do not claim success on rejected saves. Empty/missing mapping shows repair required and remains editable offline. Stale async responses cannot replace a newer selected series/draft.
- [x] Display specials and multi-episode spans clearly. For saved alternate order, flatten mapped episodes in server order; preserve canonical season mark-watched semantics by retaining those actions only in the canonical source-season view. Show repair warning for unresolved mapping, preserving manual playback.
- [x] Add browser regression: owner reviews/saves DVD order, series presents it and spans, then switches/reloads order with failure feedback. Include keyboard/mobile layout, no manual auth bypass or production data.
- [x] Run frontend lint/typecheck/tests/build/bundle and targeted Chromium/Firefox/WebKit checks. No commits; report changed files, exact tests and limitations.

## Task 3: Integration, review, documentation and shipping

- [x] Document file syntax, specials opt-in, order selection/repair, multi-file progress semantics and offline persistence in docs; document new API fields/routes.
- [x] Run real-media catalog scan fixture and end-to-end order/Next Up API acceptance, plus full relevant Go/frontend checks and migration/backup regression.
- [x] Independent complete-diff review; resolve material findings and reverify covering tests.
- [ ] Conventional commit, PR closing #42, full CI, squash merge and automatic 0.x release. Update Project 3 and roadmap/dependency readiness from live evidence. Home deployment follows the established pinned-image/snapshot/health workflow if included in the user's shipping intent.

## Verification record

Implementation and independent review completed on 11 September 2026. Local checks passed: full Go tests/race/vet/build, FFmpeg media integration, 255 frontend tests and lint/type/build/bundle, 54 mocked browser checks plus the expanded ordering flow in all three browsers, Docker image/Compose smoke, internet-isolated production acceptance, and release-policy tests. The dedicated real-media harness covers upgrade from v0.24.1, identity/history preservation, restart/rescan, selected orders, specials, stale writes and repair. Focused race tests cover the final order, provider and concurrent scan/browse fixes. PR CI and automatic release remain the shipping gates.

Order saves target the normal persistent server. Memory-only test/demo catalogs retain parsed order; no extra in-memory persistence was added. Provider imports use deterministic HTTP fixtures, while playback/upgrade checks use generated media and an actual server.
