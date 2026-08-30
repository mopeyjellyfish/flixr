---
status: accepted
---

# Plan: Flixr 2026 viewer experience and reusable demo catalog

This plan delivers the accepted viewer direction: Editorial Stream as the default shell, an optional full poster grid, profile-specific My List and view preferences, provider-free genre rows, a `FlixR` wordmark with white `Flix` and cobalt `R`, and a reusable `make demo` Docker Compose environment seeded with a dated 2026 filename-only catalog.

## Accepted interface evidence

- **Selected direction:** Editorial Stream, with the simplified full-width poster grid from Poster Theater as an alternate view.
- **Navigation:** Home, Movies, and TV remain visible in the top bar. Each destination filters to that media type.
- **Home row order:** Continue Watching first, New second, My List third, then available genre rows derived from the server catalog.
- **Poster grid:** full poster field without explanatory copy; sorting controls for title, year, recently added, and recently watched; preference persists per profile and media type.
- **My List:** working, profile-specific add/remove behavior.
- **Genres:** persisted local metadata; demo records include genres without requiring a provider credential.
- **Copy:** short labels and metadata only. Artwork, focus, and controls carry hierarchy.
- **Brand:** render `FlixR`, with `Flix` white and `R` cobalt.
- **Evidence:** `Editorial Stream` and `Poster Theater` specimens on the verified design board; the human explicitly confirmed the combined direction.

## Review evidence

- **Applicability:** Go-targeted. The plan adds SQLite schema, catalog behavior, HTTP APIs, bootstrap demo configuration, and container composition. The caller-resolved Go standard is `/Users/david/.pi/agent/git/github.com/mopeyjellyfish/pi-extensions/packages/go/skills/go/SKILL.md`; Cobra/Viper is not applicable because no CLI command framework is added.
- **Fixed document:** `docs/features/viewer-experience/plan.md` revision reviewed after all persistence and public-contract corrections.
- **Status:** Approved with Questions; no blockers. The parent resolved the remaining ownership/default/test questions before approval presentation.
- **Invalidation:** The approved review remains valid because the follow-up only assigns the existing progress timestamp write, migration defaults, and an already-required episode rejection test. Changes to persistence ownership, demo activation, public HTTP shape, or delivery topology require another review; wording-only edits do not.

## Execution mode

Checkpointed implementation. Approval authorizes bounded commits and pull-request publication for this plan only. It does not authorize merge, release, deployment, destructive cleanup, or unrelated work. The parent pauses after each slice for focused proof and diff inspection, then continues within this delivery unit unless the forecast or accepted boundary materially changes.

## Delivery topology

| Delivery unit | Topology | Stack position | Branch | Pull request base | Dependencies | Checks | Ownership | Integration point | CI fan-out | Cascade cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | sequential stack | 3/3 | `feat/viewer-experience` | `feat/playback` / PR #2 | PR #1 and PR #2 | Go race/vet, frontend lint/type/test/build/bundle, browser matrix, production executable, Docker demo smoke | one writer in `/Users/david/code/personal/flixr/.worktrees/feat-viewer-experience` | PR #2 head | existing CI plus one Docker build/config job | Medium: rebasing PR #2 cascades into this branch |

The planning document ships in the same implementation pull request. One delivery unit is appropriate because the schema/API, demo seed, and viewer UI are mutually dependent and have one end-to-end review value.

## Critical path, dependencies, and lanes

Critical path: schema and catalog contract → personalized viewer API → React viewer → Docker demo and live seed → full validation and publication.

There is one serial lane and one sole writer. Parallel implementation would overlap schema, catalog API, fixtures, and browser mocks without enough review value. The forecast is four slices, one delivery unit, one pull request, and one expensive full browser/production/container gate at the stable boundary.

Invalidation map:

- Schema or catalog-model edits, including the household progress timestamp write, invalidate migration, catalog, household, and web API tests.
- HTTP response or route edits invalidate frontend API tests, React tests, and mocked browser tests.
- viewer component or token edits invalidate frontend checks and desktop/TV/tablet/phone visual evidence.
- embed, Dockerfile, Compose, or Makefile edits invalidate the production executable and `make demo` smoke proof.
- demo title/genre edits invalidate exact seed-count, row-order, type-filter, category, and non-playable-title assertions.

## [x] 001 — Persist personalized catalog metadata and filename-only demo records

### Outcome and requirement trace

A migrated database can represent genres, added time, non-playable demo records, profile-specific My List membership, profile-specific view/sort preferences, and watched-time ordering. Demo activation inserts exactly 20 2026 films and 20 2026 series, with one `catalog_seasons` row and two filename-derived sample episodes per series, without media files, provider credentials, or artwork downloads. Repeated startup is idempotent and never modifies a non-demo catalog unless explicit demo activation is set. Demo rows carry `demo=true`; normal scans exclude them from missing-file deletion and orphan-series pruning. When scan persistence replaces in-memory maps, it re-merges demo items and series, so owner scans cannot erase demo titles from database rows or public catalog reads.

The fixed Home rows are returned even when empty. A fresh demo therefore shows an empty Continue Watching row, New with seeded titles, and an empty My List row until the selected profile adds titles.

The 2026 title list is a dated August 30, 2026 consensus snapshot derived from Rotten Tomatoes current 2026 movie/TV guides, IMDb 2026 popularity/calendar lists, and TMDB popularity semantics. Titles are factual test metadata only. Flixr ships no copyrighted media or poster art. Posterless demo records receive a deterministic, title-seeded tonal treatment in the client from a small Cobalt Signal palette; this is not stored or served as artwork.

### Seam and files

- New migration `backend/sqlite/migrations/008_viewer_catalog.sql` adds `genres_json`, `added_at`, `playable`, and `demo` to films/episodes and series; physical column `updated_at` to progress; two profile-list tables with real foreign keys; and profile view preferences. Existing playable media backfills/defaults `playable=1`; existing `added_at` backfills from `catalog_items.updated_at` and series from their earliest episode update. It adds browse indexes for year/added ordering and profile-progress update ordering; query plans remain covered by the catalog performance test.
- `profile_film_list(profile_id, catalog_id, added_at)` references top-level `catalog_items`; `profile_series_list(profile_id, catalog_id, added_at)` references `catalog_series`. Both use `(profile_id, catalog_id)` primary keys and cascade when either profile or title is deleted. Catalog methods reject episodes on the film route, with an explicit public-seam test because the table foreign key alone cannot distinguish them.
- `profile_view_preferences(profile_id, media, view_mode, sort_mode)` references profiles and uses `(profile_id, media)` as its primary key; validated values are `all|film|series`, `rows|grid`, and `title|year|added|watched`.
- `catalog.Item` and `catalog.Series` gain genres, added time, and non-omitted `playable` JSON fields. This additive field appears on existing home/search/detail responses in the same delivery unit as the frontend type update.
- `backend/catalog/demo.go` owns explicit seed records and idempotent insertion before catalog maps are loaded; `catalog.OpenDemo` seeds films, series, season rows, and episodes transactionally, then uses the normal open/load path. Blank demo fingerprints are valid under the existing partial unique index. Scan persistence re-merges the demo subset into `c.items` and `c.series` before replacing those maps. No new package or service/repository layer is added.
- `backend/household/household.go` owns the existing progress upsert and now writes `progress.updated_at` on insert and update; this is the physical source for Continue Watching and watched sorting.
- `config.Load` changes to `Load() (Bootstrap, error)`, returns `strconv.ParseBool` failures for invalid non-empty `FLIXR_DEMO`, and sets typed `Bootstrap.Demo`; `backend/main.go` handles the bootstrap error and only selects normal or demo catalog composition.
- Focused catalog, config, household, migration, scan-survival, and composition tests.

### Dependencies

Accepted direction and current migrations 001–007.

### Execution lane and ownership

Serial; sole writer in the active viewer-experience worktree.

### Red proof

A catalog test opens a migrated empty database, invokes explicit demo setup twice, and initially fails because the schema and seed seam do not exist. Assertions require 20 films, 20 series, 20 seasons, 40 episodes, persisted genres, deterministic IDs, `playable=false`, `demo=true`, and unchanged counts after the second invocation. A completed normal scan must preserve counts and post-scan visibility through public `Browse`, `Item`, and `Series` seams without restart. A normal-open test proves no demo rows appear without activation.

### Green proof and checks

Minimum schema, typed fields, config parsing, and idempotent transaction pass focused Go tests and `go test -race ./...`. No goroutines or new dependencies are introduced. Any schema or seed edit invalidates these focused tests.

### Atomic commit and pull request

Atomic commit: catalog persistence and explicit demo seed. Delivery unit 1; PR base `feat/playback`.

### Done when

The migrated database and catalog public seam expose the exact provider-free demo records, and normal production startup remains unchanged.

## [ ] 002 — Serve Home rows, filters, My List, and profile view preferences

### Outcome and requirement trace

Authenticated profiles receive a viewer model that supports Home, Movies, and TV; fixed Home row order (Continue Watching, New, My List), including empty fixed rows; genre rows only when the server has matching metadata; and a full-grid model sorted by title, year, added time, or watched time. Profiles can add and remove a film or series from My List and persist row/grid plus sort preference independently for `all`, `film`, and `series`.

Demo titles remain browseable and searchable but cannot create playback plans. The API reports `playable=false`; a playback attempt returns HTTP 409 with `playback_not_playable`, backed by `catalog.ErrNotPlayable`, so clients present a clear demo state instead of a broken Play action.

### Seam and files

- Extend `catalog` with one-level concrete query/mutation methods; no repository or service layer.
- `GET /api/v1/catalog/view?media=all|film|series` returns the persisted preference plus either ordered sections (`view=rows`) or ordered top-level items (`view=grid`). Stable sort ties are title `(title COLLATE NOCASE,id)`, year `(year DESC,title,id)`, added `(added_at DESC,id)`, and watched `(last_progress_at DESC,id)`, where `last_progress_at` is a query alias over `progress.updated_at`; series watched time is the maximum episode `progress.updated_at` for the selected profile.
- `PUT` and `DELETE /api/v1/catalog/list/{kind}/{id}`, where `kind` is `film` or `series`, add/remove membership idempotently, return HTTP 200 with `{ "listed": true|false }`, and validate the ID in the matching catalog table. An absent matching title returns 404 `catalog_not_found`.
- `GET` and `PUT /api/v1/catalog/preferences/{media}`, where `media` is `all`, `film`, or `series`, read/write `{ "view": "rows|grid", "sort": "title|year|added|watched" }`. Success returns HTTP 200 with the saved object; invalid enums return 400 `invalid_request`.
- The selected profile ID comes from the existing authenticated household session and is never client supplied.
- Existing `/catalog/home`, search, film, series, and item routes remain compatible; `playable` is additive.
- Add catalog and HTTP tests for authorization, cross-profile isolation, idempotent list updates, row ordering, empty fixed rows, category omission, filtering, stable sorting, preference validation, scan deletion cascades for real titles, and demo playback rejection.

### Dependencies

Slice 001.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

Public-seam HTTP tests initially fail for missing routes. They assert exact row order, empty fixed rows, profile isolation, GET/PUT preference round trips, valid sort modes, media-type filtering, named stable tie-breaks over physical `progress.updated_at`, `PUT`/`DELETE` list idempotency and 200 response bodies for both identity tables, 404 for unknown matching IDs, and HTTP 409 `playback_not_playable` for demo records.

### Green proof and checks

Focused catalog/web tests, complete backend race suite, and `go vet ./...` pass. SQL uses bound parameters, one serialized writer, bounded readers, and transactions for multi-row demo/list preference changes. API changes invalidate frontend contracts and all later slices.

### Atomic commit and pull request

Atomic commit: personalized viewer API. Delivery unit 1.

### Done when

Two profiles can hold different lists and display preferences while receiving deterministic, provider-free rows and filters from the same catalog.

## [ ] 003 — Build the accepted Editorial Stream viewer and simplified poster grid

### Outcome and requirement trace

The viewer journey uses the accepted combined direction across profile chooser, Home, Movies, TV, search, title details, and player entry:

- every viewer header renders `FlixR` with white `Flix` and cobalt `R`;
- Home, Movies, and TV are persistent top-level destinations;
- Editorial Stream uses one focal title and concise rows, with Continue Watching, New, My List, then genre rows;
- grid view removes the left explanation panel and gives the full width to posters plus labeled view and sort controls;
- list add/remove works from title detail and updates My List without a reload;
- demo titles use deterministic title-seeded tonal poster/focal treatments, show `Demo title · no media file`, and never offer a misleading Play action;
- helper copy, infrastructure wording, and duplicate metadata are removed where controls or state already communicate the outcome.

`frontend-development`, accepted `interface-design`, `react-interface`, and test-driven development govern implementation. Existing semantic controls, dialog behavior, virtualized rails, focus restoration, lazy Player loading, and bundle boundaries are preserved.

### Seam and files

- `frontend/src/core/api.ts` and `frontend/src/api/client.ts` for typed viewer contracts.
- Refactor `frontend/src/features/browse/Browse.tsx` into focused components only where repeated behavior proves a seam; do not add a generic component kit.
- `frontend/src/features/profiles/Profiles.tsx`, shared wordmark markup, and `frontend/src/style.css` semantic tokens/patterns.
- Unit tests and `frontend/e2e/mock-api.spec.ts` cover behavior at public DOM and HTTP seams.

### Dependencies

Slice 002 and accepted design evidence.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

One intended failing React/public-seam test at a time proves top navigation and row order, view/sort persistence, list mutation, demo non-playability, and focus restoration. Existing tests remain green after each minimal change.

### Green proof and checks

- ESLint, TypeScript, Vitest, production build, and bundle boundary.
- Mocked Chromium evidence during iteration, then Chromium/Firefox/WebKit at the stable boundary.
- Representative states: populated demo Home, Movies grid, TV grid, My List add/remove, search, details, empty My List, API failure, non-playable demo, and real playable media.
- Named viewports: TV 1920×1080, desktop 1440×900, tablet 1024×768, phone 390×844.
- Accessibility: semantic navigation, `aria-current`, labeled view/sort controls, visible focus, 44px targets, predictable rail/grid focus, no hover-only action, reduced motion, WCAG 2.2 AA contrast, and no horizontal overflow.
- `visual-validation` maintains a mismatch ledger and resolves shared causes before acceptance.

Any component, CSS token, or response-shape edit invalidates affected unit/browser evidence.

### Atomic commit and pull request

Atomic commit: Editorial Stream viewer experience. Delivery unit 1.

### Done when

A new household member can identify the current destination, choose a row or grid, sort, add a title, inspect it, and understand whether it can play without reading instructions.

## [ ] 004 — Package and prove `make demo`, then refresh the local preview

### Outcome and requirement trace

`make demo` builds and starts a reusable Docker Compose demo environment with the 40-title catalog. The Dockerfile uses explicit Node and Go build stages, builds the pure-Go SQLite executable with `CGO_ENABLED=0`, and uses the smallest practical runtime base that still satisfies readiness: Alpine Linux with only CA certificates, FFmpeg/ffprobe, and their required runtime libraries. Node, npm, Go, and compiler packages remain in build stages only.

The demo uses a named data volume, publishes port 8787, explicitly sets `FLIXR_LISTEN_ADDR=0.0.0.0:8787` and `FLIXR_DEMO=1`, exposes startup logs (including the first-run token), and supports `make demo-down`. Demo activation is explicit; normal binaries and existing data directories are not seeded.

After container proof, refresh the existing port 18789 preview with the verified binary and explicit demo activation. Preserve its current database before any reset or reseed. Because deployment and destructive reset are not authorized by plan approval, pause for explicit confirmation immediately before replacing preview data unless the human separately authorizes that action.

### Seam and files

- New root files: `Dockerfile`, `compose.yml`, `.dockerignore`, and `Makefile`.
- README demo instructions, exact image/runtime tradeoff, first-run token flow, volume reset command, and factual-title/no-media disclaimer.
- `.github/workflows/ci.yml` gains a Docker config/build check; end-to-end container startup remains a local required smoke because nested service timing does not add enough CI value.
- Production browser and container smoke evidence.
- `docs/features/viewer-experience/validation.md` records source links, exact counts, view states, responsive captures, accessibility results, and residual limitations.
- A proposed `DESIGN.md` update is presented separately for explicit approval before writing durable design changes.

### Dependencies

Slices 001–003.

### Execution lane and ownership

Serial; same sole writer.

### Red proof

Before-state: no `make demo` target and no Compose service. The deterministic smoke script requires a clean static build, externally reachable healthy setup status on published port 8787, `ffmpeg=true`, `ffprobe=true`, a printed setup token, exact seeded counts after first-run profile selection, scan survival, and unchanged counts after restart.

### Green proof and checks

- `docker compose config` and `make demo` build/start/health/restart smoke.
- Inspect final image layers to confirm Node, npm, Go, and compiler packages are absent from the runtime stage; report image size rather than claiming an absolute global minimum.
- Full backend race/vet/media-integration gates.
- Full frontend unit/build/bundle and three-browser mocked matrix.
- Embedded-production Chromium acceptance with real fixtures plus demo non-playability.
- Browser visual validation at four named viewports, console/network review, accessibility scan, overflow check, focus path, and mismatch ledger.
- Clean Git status except intended source/docs changes; generated Playwright output and local volumes remain unpublished.

### Atomic commit and pull request

Atomic commit: reusable Compose demo and validation evidence. Delivery unit 1. After fixed-snapshot review, publish one stacked PR from `feat/viewer-experience` to `feat/playback` using `open-pr`; link it into the existing stack. Do not merge or release.

### Done when

A developer can run `make demo`, complete first run, browse 20 films and 20 series with genre rows, exercise list/grid preferences and My List, and tear down the container without fake data entering normal Flixr startup.
