---
status: accepted
---

# Plan: Flixr core

This plan delivers every accepted release-one vertical slice from `docs/features/flixr-core/pitch.md`. It keeps one serial critical path because the catalog, household, playback, and screen-control contracts are sequentially dependent. It uses three reviewable delivery units so catalog/browse, media playback, and screen/casting risk do not accumulate in one unreviewable pull request.

## Review evidence

- **Applicability:** Go-targeted. The plan creates a Go 1.25 module, HTTP server, SQLite persistence, filesystem scanner, FFmpeg process manager, WebSocket screen control, and lifecycle/concurrency behavior.
- **Fixed document:** `docs/features/flixr-core/plan.md`, accepted replacement with the `backend/` and `frontend/` repository split.
- **Status:** replacement fixed-document review **Approved with Questions** with no blocking issues. The user approved this complete replacement plan and authorized Delivery Unit 1. The non-blocking Playwright target question is resolved below: acceptance browser suites run against the production-style `flixr` executable serving copied, embedded frontend assets.
- **Invalidation:** the accepted repository/module structure changed after implementation evidence showed `go test ./...` traversing `web/node_modules`; this invalidated the prior review and whole-plan approval. Wording-only changes after replacement review do not invalidate it.

## Execution mode

Execution is **checkpointed implementation**. Whole-plan approval authorizes only Delivery Unit 1. After each stable unit is verified, reviewed, committed, and published, Flixr pauses before starting the next unit and offers **Continue**, **Review next unit**, or **Discuss**.

Approval never authorizes merge, release, deployment, destructive cleanup, hosted infrastructure, paid services, secrets creation, publication of review-only copyrighted artwork, or unrelated work.

## Repository and implementation conventions

- Keep the accepted branch and worktree for planning and Delivery Unit 1: branch `feat/product-pitch`, worktree `/Users/david/code/personal/flixr/.worktrees/feat-product-pitch`.
- Keep one Go module under `backend/` with module path `github.com/mopeyjellyfish/flixr/backend`, and one React application under `frontend/`. Root product documentation remains outside both. Vite proxies `/api` to Go during development. Release packaging builds `frontend/`, copies its output into `backend/web/assets/` without deleting the committed non-dotfile `placeholder.txt`, then runs `(cd backend && go build -o flixr .)`. The placeholder makes every backend Go gate compile independently on a clean checkout without a prior frontend build. Generated embedded assets are ignored except for the placeholder. Do not create a workspace/package graph for hypothetical native clients.
- Compose concrete `Catalog`, `Household`, `Playback`, `Screens`, and `Web` values in `backend/main.go`; it contains lifecycle and wiring, not business behavior. Consumer-owned interfaces are permitted only for accepted real alternatives: metadata lookup and process execution in tests.
- Inside `backend/`, use one-level domain packages: `catalog/`, `household/`, `playback/`, `screens/`, and `web/`. Keep focused `config/` and `sqlite/` mechanism packages at the same level. Do not add `internal/`, `service/`, `repository/`, `controller/`, `domain/`, `utils/`, or `helpers/` directories. The `sqlite` package owns connection policy, transaction helpers, and the single ordered embedded migration set under `backend/sqlite/migrations/`; domain DDL enters that directory while domain query SQL and decisions remain in the owning package. The `config` package reads only bootstrap environment values; owner settings persist through the household/owner surface.
- Keep browser-independent TypeScript contracts and state machines under `frontend/src/core/` only where release-one behavior exercises them. Keep DOM, HLS, Cast, AirPlay, and focus implementations behind frontend modules. Do not create React Native Web or native packages.
- Use npm with a committed lockfile under `frontend/`. Use React 19, TypeScript, Vite, Tailwind CSS v4, Vitest, React Testing Library, and Playwright. Use `@tanstack/react-virtual` only if focused profiling confirms it satisfies directional focus restoration; otherwise use the smallest equivalent virtualizer.
- Use Go's standard library first, `modernc.org/sqlite` for SQLite, `github.com/gofrs/flock` for cross-platform advisory data/segment locks, `github.com/coder/websocket` for context-aware WebSockets, `golang.org/x/crypto/argon2` for memory-hard owner/PIN hashing, `golang.org/x/sync/errgroup` for bounded cancellable scan work, and `github.com/stretchr/testify` assertions without suites. Use a dedicated server-derived WebSocket context rather than the upgraded request context; keep same-origin rejection or an explicit trusted-origin allowlist, and treat I/O context cancellation as connection-fatal. Record why each non-standard dependency is required.
- Playback's concrete generation manager mints each short-lived loopback input URL from a loopback base address and signing key supplied by `main`. Web validates the opaque generation token through Playback before opening the catalog ID through Catalog. Playback never imports Web, and Web remains the HTTP/file-delivery owner.
- Playback jobs, generations, leases, and screen presence/control sessions are memory-only runtime state. Only catalog, owner/settings, profiles, and viewing progress persist in SQLite. Restart invalidates runtime sessions; locked startup reclamation removes orphaned generation directories without reconstructing jobs from database rows.
- Required command definitions are `(cd backend && go test ./...)`, `(cd backend && go test -race ./...)`, and `(cd backend && go vet ./...)`; `npm --prefix frontend run lint`, `npm --prefix frontend run typecheck`, `npm --prefix frontend run test -- --run`, and `npm --prefix frontend run build`; and `npm --prefix frontend run test:e2e -- --project=chromium`, `--project=firefox`, or `--project=webkit` for the named browser matrix. The required media job runs `(cd backend && FLIXR_REQUIRE_FFMPEG=1 go test -tags=integration ./...)` after installing FFmpeg/ffprobe and treats a required skip as failure.
- Playwright acceptance suites build the frontend, copy its output while preserving `backend/web/assets/placeholder.txt`, build `flixr`, and run against that executable serving the embedded assets. Vite with `/api` proxy is a development-only loop and may support component exploration, but it is not acceptance evidence. Browser CI may perform these build steps itself and remains independent of the backend unit/race/vet job.
- Use `DESIGN.md` as accepted seed authority. It contains no TheTVDB artwork. Implementation screenshots and fixtures use only original, generated, public-domain, or properly licensed content.

## Delivery topology

| Delivery unit | Topology | Stack position | Branch | Pull request base | Dependencies | Checks | Ownership | Integration point | CI fan-out | Cascade cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 — Local catalog and household browse | ordered stack | 1/3 | `feat/product-pitch` | `main` | accepted pitch and design guidance | Go unit/race/vet; frontend lint/type/unit/build; setup/catalog/profile browser suite; axe; visual ledger | current Worktrunk worktree; direct parent is sole writer; review follows the slice-level atomic commits in order | reviewed DU1 tip becomes DU2 base | 3 independent jobs | Changes to JSON contracts, schema, or frontend shell cascade to units 2 and 3; forecast moderate |
| 2 — Modern local playback | ordered stack | 2/3 | `feat/playback` | `feat/product-pitch` | Delivery Unit 1 | all DU1 checks; playback unit/race; required FFmpeg media job; player browser suite; playback visual ledger | new Worktrunk child worktree after DU1 publication; one sole writer | reviewed DU2 tip becomes DU3 base | 4 jobs | Playback URL or session changes cascade to screens/casting; forecast moderate |
| 3 — Screens, casting, and release gate | ordered stack | 3/3 | `feat/screens` | `feat/playback` | Delivery Unit 2 | complete Go/frontend/browser suite; FFmpeg required job; two-client screen suite; Cast/AirPlay adapter tests; physical-device evidence; performance and final visual gates | new Worktrunk child worktree after DU2 publication; one sole writer | stack head is release-one review point | 5 jobs plus manual device gate | Final unit has no dependent branch; lower-stack fixes can cascade through both upper branches; forecast low late churn after frozen contracts |

The pitch, this plan, and `DESIGN.md` share Delivery Unit 1's implementation publication. They do not receive a documentation-only pull request. Every pull request uses `open-pr`; this sequential chain uses `gh stack`. The first pull request is useful by itself as a secure local catalog and household browser. The second adds complete local playback. The third adds remote screens and platform casting.

## Critical path, dependencies, and lanes

The critical path is serial:

1. owner claim and lifecycle foundation;
2. deterministic catalog and offline browse;
3. household profiles and the responsive Cobalt Signal web shell;
4. direct playback and progress;
5. bounded remux/transcode generations;
6. Flixr screen control;
7. Cast/AirPlay and release-wide gates.

There are **no planned parallel writer lanes**. Go and React work meet at versioned JSON contracts, and early parallelism would require duplicate fixtures, temporary APIs, and integration branches. Read-only checks can run concurrently after each diff freezes.

Forecast:

- active writer lanes: 1;
- delivery units and pull requests: 3 in one ordered stack;
- integration points: reviewed tips of Delivery Units 1 and 2;
- expensive gates: FFmpeg integration on bounded committed fixtures, Playwright visual/accessibility matrices, the 10,000-item performance budget, and physical Google Cast plus Safari/AirPlay evidence;
- likely cascade cost: moderate during the first two units, then low if versioned media/session contracts freeze before Delivery Unit 3.

Pause before further publication if coordination requires an extra writer lane, an extra pull request, a hosted service, a different casting architecture, or more than one compatibility rendition. A changed branch boundary or authority requires a revised plan and fresh approval.

### Invalidation map

- **Focused slice proof** remains reusable only while that slice's public seam and fixtures are unchanged.
- **Affected-boundary checks** rerun when a domain model, schema, JSON contract, authentication rule, player state, or generated asset boundary changes.
- **Integration proof** reruns from the earliest affected delivery unit when a lower stack branch changes an interface consumed above it.
- **Visual evidence** reruns for every changed route, state, viewport, typography/token rule, or shared component. Unchanged captures remain evidence only for untouched surfaces.
- **FFmpeg evidence** reruns for changes to planner keys, commands, input URLs, leases, process termination, segment accounting, or media fixtures.
- **Security evidence** reruns for changes to authentication, cookies, origin checks, signed URLs, path handling, locks, provider inputs, or WebSocket authority.
- **Final required gates** always run once against each delivery unit's frozen tree before publication. Do not run a composite gate beside its own constituent jobs.

## [x] 001 — Secure local first run and owner claim

### Outcome and requirement trace

A clean installation starts one local server, exclusively locks its data and any distinct segment directory, opens SQLite with the accepted WAL policy, reports FFmpeg/ffprobe readiness, and serves a first-owner claim flow. The one-time console token can create exactly one owner and cannot be reused. The owner can recheck tool readiness without restarting.

Trace: AC-001, AC-015, AC-016, and the startup/shutdown portion of AC-017.

### Seam and files

Public seams:

- `flixr` process bootstrap environment: data directory, listen address, optional TLS certificate/key;
- `POST /api/v1/setup/claim`, `GET /api/v1/setup/status`, and owner-authenticated readiness/settings routes;
- stable JSON error envelope and HttpOnly same-site session cookie.

Likely files:

- root `.gitignore`; `backend/go.mod` with module path `github.com/mopeyjellyfish/flixr/backend`; `backend/go.sum`; `backend/main.go`; `backend/config/bootstrap.go`;
- `backend/sqlite/db.go`, `backend/sqlite/migrations/*.sql`, lock implementation and tests;
- `backend/household/owner.go`, `backend/household/auth.go`, `backend/household/store.go`;
- `backend/web/server.go`, `backend/web/errors.go`, `backend/web/auth.go`, setup/settings handlers, `backend/web/assets/placeholder.txt`, and embedded-asset support;
- `frontend/package.json`, lockfile, Vite/Tailwind/TypeScript/test configuration;
- `frontend/src/core/api.ts`, `frontend/src/api/client.ts`, setup and owner-readiness routes/components;
- build/embed scripts and `.github/workflows/ci.yml` foundation.
- explicit preserved-scaffold migration: root `go.mod`/`go.sum` and `cmd/flixr/main.go` move to `backend/` with imports rewritten to the new module path; root `config/`, `sqlite/`, `catalog/`, and `household/` move under `backend/`; `webserver/` moves to `backend/web/` and changes package name to `web`; React `web/` moves to `frontend/`; `.gitignore` changes from `web/node_modules/` and `web/dist/` to the new frontend and generated embed paths.

### Dependencies

Accepted pitch, accepted `DESIGN.md`, Go 1.25, Node/npm, and no prior implementation slice.

### Execution lane and ownership

`serial`, Delivery Unit 1, current worktree, direct parent sole writer.

### Red proof

Start with an HTTP integration test that proves a clean server rejects owner creation without the generated token, accepts one valid claim, rejects token reuse, and leaves no usable default credential. Add focused tests for lock contention, WAL/busy-timeout/reader-limit setup, missing-tool readiness, `http.Server.Shutdown`, and the bounded in-flight HTTP drain before implementing each behavior. Do not create a WebSocket registry in Delivery Unit 1; Slice 006 introduces and proves tracked upgraded connections when the first WebSocket behavior exists.

The React red proof mounts the setup route against recorded API responses and fails until missing-tool, invalid-token, successful-claim, loading, and server-error states are reachable by keyboard.

### Green proof and checks

- Focused Go tests pass under `(cd backend && go test -race ./...)` for claim, authentication, locking, connection policy, readiness recheck, and graceful process shutdown.
- Frontend unit tests pass for setup state and semantic form behavior.
- A Playwright path claims a clean server, confirms token reuse fails, signs in through the cookie session, and operates the owner readiness screen without pointer-only controls.
- Same-origin protection, cookie attributes, memory-hard password hashing parameters, rate limits, stable error mapping, server timeouts, and redacted logs receive focused checks.
- `(cd backend && go vet ./...)`, frontend lint, TypeScript, unit tests, production frontend build, asset copy/embed check, and backend build pass.

Changes to bootstrap values, owner authority, session cookies, error shape, locks, or database opening invalidate this slice and every later HTTP/browser proof.

### Atomic commit and pull request

Atomic commits may separate repository/toolchain bootstrap from the complete first-owner vertical behavior, but both remain in Delivery Unit 1. Pull request base is `main`; stack position 1/3.

### Done when

A clean binary and web build prove the one-time owner claim, exclusive startup, actionable degraded readiness, authenticated owner surface, and bounded clean shutdown without an undocumented credential or external database.

## [x] 002 — Deterministic film and TV catalog with offline browse

### Outcome and requirement trace

The owner configures one film root and one TV root, manually scans bounded synthetic media, and receives stable film/series/season/episode records with probed tracks, local fallback titles, optional TMDB enrichment, cached artwork, unmatched items, and partial failure. Known content remains browseable when provider access is blocked. Rescans update only affected records and cannot escape configured roots.

Trace: AC-002, AC-003, AC-004, the scan/readiness portion of AC-015, the scan concurrency/cancellation portion of AC-017, and catalog/path portions of AC-016.

### Seam and files

Public seams:

- owner library-root and scan routes;
- paginated `/api/v1/catalog/home`, `/api/v1/catalog/search`, film detail, series detail, and scan-status responses;
- catalog IDs are the only client media identifiers.

Likely files:

- `backend/catalog/models.go`, `roots.go`, `scanner.go`, `probe.go`, `matching.go`, `tmdb.go`, `queries.go`, `store.go`, and domain errors;
- catalog DDL in the single ordered `backend/sqlite/migrations/*.sql` set and captured `ffprobe` JSON;
- `backend/web/catalog_handlers.go`, `scan_handlers.go`, route contract tests;
- module-root `backend/testdata/media/` and `backend/testdata/ffprobe/` for the bounded generated fixtures genuinely shared by Catalog, Playback, and Web integration tests; `backend/scripts/generate-media-fixtures.sh`; and fixture licensing notes. Package-specific fixtures stay in the owning package's `testdata/` directory.
- `frontend/src/core/catalog.ts`, catalog API functions, owner scan/readiness views, and basic browse data loaders.

### Dependencies

Slice 001 owner authority, settings persistence, JSON envelope, SQLite policy, and process lifecycle.

### Execution lane and ownership

`serial`, Delivery Unit 1, current worktree, same sole writer.

### Red proof

Begin with one scanner acceptance test against generated fixture descriptors that fails until it creates one film and one series with ordered seasons/episodes and recorded container/track properties. Add table tests for idempotent rescan, add/change/move/remove, partial ffprobe/provider failure, ambiguous filename matching, provider outage, traversal, and symlink escape. Add deterministic tests that enforce the configured worker bound, cancel an in-flight scan, and shut down during a scan while preserving the last valid catalog and leaving no scan worker or probe process behind.

Add handler contract tests that fail until paginated browse/search/details return only catalog IDs and cached/local metadata while external provider calls are blocked.

### Green proof and checks

- Scanner tests pass with fake process execution and captured probe JSON without local FFmpeg.
- Containment tests use `os.Root` and prove traversal and symlink escape are rejected at the open operation, not only through string checks.
- Provider calls and scan workers use bounded server-derived contexts, status/body limits, and credential redaction. Cancellation or provider outage preserves the last valid local records and terminates in-flight probe/provider work within the shutdown bound.
- The committed fixture set records its source/generation/license and stays within 10 seconds and 1 MiB per file and 5 MiB total.
- A Playwright owner flow configures roots, starts a scan, observes empty/progress/partial/failure/success states, and browses cached/local results while TMDB traffic is blocked.
- Catalog package tests, HTTP contract tests, race checks, and Delivery Unit 1's current required checks pass.

Changes to media identity, schema, root containment, probe representation, pagination, or JSON catalog contracts invalidate slices 002–007.

### Atomic commit and pull request

One atomic behavior commit includes scanner source, domain SQL, fixtures, focused tests, owner scan UI, and required contract updates. Delivery Unit 1, pull request base `main`, stack position 1/3.

### Done when

Repeated scans are deterministic, path-safe, partially failure-tolerant, and observable through the owner UI; films and episodic TV remain available through local metadata and cached provider data without internet access.

## [x] 003 — Household profiles and responsive cinematic browse

### Outcome and requirement trace

The owner creates, renames, protects, and unprotects profiles. A household member selects a profile, enters a PIN when required, browses and searches films and TV, opens film and episode details, and sees isolated progress placeholders. The Cobalt Signal hierarchy works across named TV, desktop, tablet, and phone viewports with deterministic directional/keyboard focus.

Trace: AC-005, AC-006, AC-007, the browse/search portion of AC-018, and accepted interface criteria in the pitch and `DESIGN.md`.

### Seam and files

Public seams:

- profile-management, profile-selection, PIN verification, home/search/detail routes;
- browser-independent catalog/profile view state under `frontend/src/core/`;
- deep `focusManager` module used through intents rather than page-level DOM traversal.

Likely files:

- `backend/household/profiles.go`, `progress.go`, `store.go`, profile/PIN tests;
- `backend/web/profile_handlers.go` and catalog/profile response assembly;
- `frontend/src/app/`, route definitions, layout/navigation, `frontend/src/core/session.ts`;
- `frontend/src/features/profiles/`, `browse/`, `search/`, `details/`;
- `frontend/src/modules/focusManager/`, shared semantic controls, token CSS, self-hosted font assets with licenses;
- Playwright fixtures, accessibility checks, screenshots, and mismatch ledger.

### Dependencies

Slices 001–002 and accepted root `DESIGN.md`.

### Execution lane and ownership

`serial`, Delivery Unit 1, same sole writer. Follow `frontend-development`, `interface-design`, and `react-interface`; use `visual-validation` after behavior stabilizes.

### Red proof

Start with an HTTP test proving one profile cannot read or update another profile's state and a protected profile rejects an invalid/rate-limited PIN. Start React tests for route state, semantic controls, focus entry/exit, rail movement, detail close-and-restore, reduced motion, and no hover-only action.

The initial Playwright evidence is expected to produce a mismatch ledger against `DESIGN.md` at:

- TV: 1920×1080;
- desktop/laptop: 1440×900;
- tablet: 1024×768;
- phone: 390×844.

Representative states: first-run handoff, profile choice, PIN invalid/rate-limited, populated home, search empty/results, film detail, episode detail, loading, local-only metadata, and request failure.

### Green proof and checks

- Profile isolation, PIN hashing/rate-limit, and session authority tests pass under race detection.
- React unit/integration tests prove route state, virtualized rails, semantic controls, focus restoration, reduced motion, and lazy route boundaries.
- Playwright exercises keyboard and directional paths, captures all named viewports, runs axe with no serious or critical focal-flow findings, checks console/runtime errors and overflow, and confirms no clipped focal action or hover-only behavior.
- Visual validation compares hierarchy, typography, colors, spacing, focus, content density, and states. Every mismatch is resolved or recorded as explicit unmet proof; review-only TheTVDB assets are absent.
- A 10,000-record query fixture records browse/search p95 at or below 200 ms on the named reference machine, excluding artwork transfer; the Delivery Unit 1 checkpoint pauses if this budget is missed. The home route also proves it does not create DOM nodes for the full catalog.
- Complete Delivery Unit 1 Go/frontend/browser gates pass against the frozen tree.

Changes to shared navigation, tokens, profile session state, catalog cards, route composition, or viewport behavior invalidate the corresponding unit/browser/visual evidence.

### Atomic commit and pull request

Keep domain profile behavior and its React client in one observable commit; visual corrections may be a following atomic commit in the same unit. Delivery Unit 1, pull request base `main`, stack position 1/3.

### Done when

A new household can securely select profiles and browse a responsive, accessible, viewing-first film/TV catalog on TV, desktop, tablet, and phone. Delivery Unit 1 is then frozen, formally reviewed for security/domain/UI risk, published through `open-pr`, and becomes the verified base for Delivery Unit 2.

## [ ] 004 — Capability-planned direct playback and cross-client resume

### Outcome and requirement trace

A client reports exact playback capabilities, while the server supplies current execution readiness. Playback deterministically returns a direct/remux/transcode/unsupported plan or an actionable `ffmpeg`-unavailable error. A compatible catalog item direct-plays through authenticated byte ranges even when FFmpeg is absent. Seek uses range delivery, progress belongs to the active profile, and another client resumes from the saved position.

Trace: AC-008, AC-009, direct-play portions of AC-016 and AC-017, and the player-bundle portion of AC-018.

### Seam and files

Public seams:

- plain-value `playback.MediaProperties`, `ClientCapabilities`, `ServerReadiness`, `Plan`, and stable unsupported/capacity/FFmpeg-unavailable errors;
- `POST /api/v1/playback/plans`, short-lived playback-session routes, authenticated catalog-ID range delivery, and profile progress routes;
- deep web `mediaPlayer` module with a capability adapter and plain state/actions.

Likely files:

- `backend/playback/plan.go`, `capabilities.go`, `errors.go`, planner table tests;
- `backend/web/playback_handlers.go`, range delivery, signed/session authority, contract tests;
- `backend/household/progress.go` updates;
- `frontend/src/core/playback.ts`, `frontend/src/modules/mediaPlayer/`, player route/controls, lazy import boundary;
- direct-play browser and HTTP range fixtures.

### Dependencies

Accepted/published Delivery Unit 1 contracts and catalog IDs. Create and activate `feat/playback` from the verified DU1 tip through Worktrunk before edits.

### Execution lane and ownership

`serial`, Delivery Unit 2 child worktree, one sole writer. Follow `go`, `test-driven-development`, `frontend-development`, `interface-design`, `react-interface`, and `visual-validation`.

### Red proof

Start with planner table tests that fail until media, client capability, and server-readiness combinations deterministically choose all four plan outcomes or the actionable FFmpeg-unavailable domain error. Prove that FFmpeg absence leaves a compatible item on direct play while a remux/transcode candidate returns the disabled state before process start. Add an HTTP test that fails until an authenticated compatible item returns correct 200/206 range behavior without restarting at byte zero and rejects unknown IDs, traversal, stale sessions, and cross-profile progress writes.

Start a player state test that fails until load/play/pause/seek/progress/error intents are independent of the page component and the player bundle is absent from profile selection.

### Green proof and checks

- Planner and range tests pass with no FFmpeg process for direct play. With FFmpeg absent, direct play remains available and remux/transcode candidates map the Playback-owned FFmpeg-unavailable error to one actionable Web response before any process launch.
- Web opens the indexed relative path through Catalog/`os.Root`; clients never submit a path.
- Progress writes are bounded, profile-authorized, debounced without losing final stop/unload state, and resumable on a second browser context.
- Player controls are semantic, keyboard/directional/touch reachable, reduced-motion safe, and show startup/playing/paused/buffering/actionable-error states.
- Browser evidence covers direct start, range seek, stop, resume on another client, expired authority, and network interruption at named viewports.
- Bundle analysis proves player/casting code is absent from initial profile selection.

Changes to planner keys, capability representation, range authority, progress model, or player state invalidate all later playback and screen proofs.

### Atomic commit and pull request

One direct-play vertical commit in Delivery Unit 2. Pull request base is `feat/product-pitch`; stack position 2/3.

### Done when

A compatible local item direct-plays, seeks, records isolated progress, and resumes on another client through the accepted authenticated seams without invoking FFmpeg.

## [ ] 005 — Bounded remux, transcode, seek generations, and media gate

### Outcome and requirement trace

A container-only mismatch remuxes through stream-copy fragmented-MP4 HLS. One incompatible file transcodes through the H.264/AAC fMP4 HLS fallback. Playback sessions lease generation-owned work, share only a matching live generation, seek inside retained segments, create a new generation outside the window, and release processes/directories after explicit stop or lease expiry without interrupting another viewer.

Trace: AC-010, AC-011, AC-019, AC-020, plus FFmpeg/security/shutdown portions of AC-016 and AC-017.

### Seam and files

Public seams:

- Playback job/generation/session state and observable capacity/unsupported errors;
- generation-scoped manifest, segment, heartbeat, stop, and internal loopback range-input routes;
- owner settings/status for concurrency, segment directories, byte bounds, active generations, and FFmpeg readiness.

Likely files:

- `backend/playback/jobs.go`, `generation.go`, `leases.go`, `segments.go`, `process.go`, `commands.go`, `testing/synctest` timing tests, and process fakes;
- consumer-defined process-execution interface in backend Playback tests only;
- `backend/web/hls_handlers.go`, loopback input authorization/range handling, heartbeat/stop routes;
- `frontend/src/modules/mediaPlayer/hls.ts`, lease heartbeat/state integration, owner playback status;
- bounded synthetic source/remux/transcode fixtures, generation script/license;
- `.github/workflows/ci.yml` named `media-integration` job.

### Dependencies

Slice 004 planner, authenticated media open, player state, progress, and Delivery Unit 1 owner settings/locks.

### Execution lane and ownership

`serial`, Delivery Unit 2 child worktree, same sole writer.

### Red proof

Build one intended failing test at a time around `testing/synctest` and a fake process executor; do not add a production clock interface unless a named case cannot be made deterministic with `synctest`:

- identical complete rendition keys attach to one live generation;
- detach of one viewer does not cancel another;
- paused heartbeat interval is below half TTL and preserves the lease;
- abandoned lease expires and terminates work;
- in-window seek reuses retained segments;
- out-of-window seek creates a second generation while an old lease can keep the first alive;
- every generation counts as one process and one bounded directory;
- per-generation/global bounds return capacity instead of exceeding limits;
- cancellation sends graceful termination, then bounded forced kill;
- orphan cleanup runs only after exclusive data/segment locks.

Add command-construction tests that fail on any client-controlled argument, protocol, path, or shell text. Then add a real FFmpeg test that initially fails until remux and fallback outputs are browser-consumable fMP4 HLS.

### Green proof and checks

- Unit/race tests prove the job/generation/lease invariants with `testing/synctest` timing and process fakes, without adding a production clock abstraction.
- Playback's generation manager mints each discrete FFmpeg command and short-lived generation-scoped loopback URL from its composition-supplied base/signing key. Web validates the opaque token through Playback, then serves a loopback-only, range-capable, catalog-ID-based, `os.Root`-contained input without giving Playback file access.
- Segment files live in one generation directory, count once globally, use a sliding window, never outlive the generation, and reclaim after completion/cancellation/expiry or prior unclean exit.
- The named CI `media-integration` job installs `ffmpeg`/`ffprobe`, sets a require flag, fails on any required skip, and proves direct, remux, transcode, both seek classes, heartbeat/TTL, cancellation, abandonment, shared lifetime, range input, lock contention, limits, directory lifetime, and orphan cleanup.
- Local unit tests remain green without FFmpeg through captured probe JSON and fakes.
- Browser evidence covers remux/transcode startup, buffering, seek, paused heartbeat, capacity, expiry/recovery, and stop. Visual mismatch ledger is resolved for player and owner-generation states.
- Complete Delivery Unit 2 gates pass against the frozen tree; formal review covers concurrency, filesystem/process security, cancellation, cleanup, and player state.

Any change to FFmpeg arguments, rendition keys, generation ownership, lease timing, segment accounting, input URL authority, fixtures, or HLS player behavior invalidates the named media job and corresponding browser proof.

### Atomic commit and pull request

Use coherent behavior commits for generation lifecycle/storage and for real FFmpeg/player integration, both inside Delivery Unit 2. Pull request base `feat/product-pitch`, stack position 2/3.

### Done when

Representative direct, remux, and fallback media paths pass the required non-skippable CI gate; seeks and concurrent viewers obey the accepted lifetime/resource bounds; Delivery Unit 2 is frozen, formally reviewed, published through `open-pr`/`gh stack`, and becomes the verified base for Delivery Unit 3.

## [ ] 006 — Authorized Flixr-to-Flixr screen control

### Outcome and requirement trace

Two supported Flixr web clients on the LAN can identify one as an available screen, establish short-lived authorized control, start/pause/seek playback, hand playback back, and recover visibly when either side disconnects. Flixr screens remain the owned casting path without a hosted service.

Trace: AC-012, WebSocket and local-authority portions of AC-016 and AC-017, and accepted screen-readiness interface states.

### Seam and files

Public seams:

- screen presence, short-lived control-session, and owner connected-screen HTTP routes;
- origin-checked authenticated WebSocket protocol with versioned message types;
- deep web `screenCoordinator` state/actions consumed by pages and player.

Likely files:

- `backend/screens/presence.go`, `sessions.go`, `commands.go`, domain errors/tests;
- `backend/web/screen_handlers.go`, WebSocket registry/origin/auth/shutdown tests and owner connected-screen route;
- `frontend/src/core/screens.ts`, `frontend/src/modules/screenCoordinator/`, chooser/readiness/player integration, and owner connected-screen view;
- two-browser Playwright fixtures and mismatch ledger.

### Dependencies

Accepted/published Delivery Unit 2 playback-session, progress, and player state. Create and activate `feat/screens` from the verified DU2 tip through Worktrunk before edits.

### Execution lane and ownership

`serial`, Delivery Unit 3 child worktree, one sole writer. Follow Go/TDD and the accepted React/frontend/visual methods.

### Red proof

Start with a protocol test that fails until unauthorized, wrong-origin, expired, and cross-profile screen sessions cannot upgrade or command playback. Add an integration test with two in-process clients that fails until presence, play, pause, seek, handoff, disconnect, reconnect, process restart invalidation, and server shutdown have deterministic state transitions and no leaked WebSocket.

Start a React coordinator test that fails until chooser/readiness/connecting/playing/disconnected/error states are page-independent and focus returns after the chooser closes.

### Green proof and checks

- Screen domain and WebSocket tests pass under race detection with short-lived authority and stable protocol errors.
- Web tracks every upgraded connection, rejects bad origins/authority before control, and closes connections during bounded shutdown.
- Two Playwright browser contexts complete discovery, remote start/pause/seek, handoff, sender loss, receiver loss, and recovery on one LAN server.
- Screen availability remains a quiet playback-adjacent status in the viewing flow, not a device dashboard. The separate owner-operations view lists connected screens and their current state. Keyboard/directional/touch paths and focus restoration pass at named viewports.
- No hosted account, relay, WAN discovery, permanent control token, or exposed media path enters the implementation.

Changes to screen protocol, playback command state, session authority, origin rules, or coordinator state invalidate slice 006 and platform-casting integration evidence.

### Atomic commit and pull request

One Flixr screen-control vertical commit in Delivery Unit 3. Pull request base is `feat/playback`; stack position 3/3.

### Done when

Two real browser contexts control and hand off playback over the LAN with authorized, expiring sessions and visible disconnect recovery; shutdown leaves no tracked WebSocket or playback lease behind.

## [ ] 007 — Google Cast, AirPlay, and release-wide acceptance

### Outcome and requirement trace

Supported Chromium clients expose Google Cast under documented trusted-HTTPS prerequisites and plan media against the fixed Cast receiver profile. Supported Safari clients expose the native AirPlay picker only when a route is available. Both use short-lived planner-approved URLs, keep the sender responsible for profile progress, and report unavailable/rejected/disconnected/expired/playback errors through the shared state model. The complete release meets lifecycle, security, responsiveness, accessibility, performance, offline, and media gates.

Trace: AC-003, AC-006, AC-007, AC-013 through AC-020, plus all accepted boundary and design requirements.

### Seam and files

Public seams:

- fixed Cast receiver capability profile and short-lived signed external media URLs;
- web Cast and AirPlay adapters behind `screenCoordinator`/`mediaPlayer` intents;
- documented owner-managed trusted HTTPS and runtime prerequisite reporting;
- release CI, performance report, device-evidence checklist, and final mismatch ledger.

Likely files:

- `backend/playback/cast_profile.go`, external signed media authorization tests;
- `backend/web/external_media_handlers.go`, TLS/prerequisite reporting;
- `frontend/src/modules/screenCoordinator/cast.ts`, `airplay.ts`, lazy SDK loader and capability tests;
- owner/help documentation, browser/device test fixtures, performance harness and reports;
- final `.github/workflows/ci.yml` gates and release-one README instructions.

### Dependencies

Slice 006 screen state and all Delivery Unit 2 playback/media URL behavior.

### Execution lane and ownership

`serial`, Delivery Unit 3 child worktree, same sole writer. Physical device validation is read-only evidence after the implementation diff freezes.

### Red proof

Start adapter contract tests that fail until:

- Cast requests the fixed receiver profile rather than sender-browser capabilities;
- unsupported media receives the H.264/AAC fallback or actionable unsupported/capacity state;
- Cast and AirPlay controls remain absent when runtime prerequisites are unmet;
- signed URLs expire, cannot change catalog/profile/generation identity, and never expose filesystem paths;
- receiver status updates drive sender-side progress and route/playback errors.

Add browser tests with deterministic Cast/AirPlay API fakes before implementation. Record the physical-device before-state as unmet proof until a supported Chromium + Google Cast target and Safari + AirPlay route complete the named checklist.

### Green proof and checks

- Unit and browser adapter tests prove capability planning, lazy SDK loading, runtime visibility, sender progress, signed URL expiry, and common error states.
- Trusted-HTTPS setup is documented and tested locally without introducing a hosted service or automatic WAN/DNS control.
- Physical Google Cast evidence proves detection, start, planner-approved direct/remux/fallback playback as applicable, progress, disconnect, rejection/error, and URL expiry behavior.
- Physical Safari/AirPlay evidence proves picker visibility only with an available route, start, progress, route loss, unsupported/expired behavior, and absence of a dead control in other browsers. Playwright WebKit is not represented as physical AirPlay proof.
- Final visual validation covers profile, setup, owner scan/settings including connected screens, home, search, film details, episode details, player, screen chooser/readiness, Cast/AirPlay unavailable, loading, empty, partial, buffering, disconnect, and error states at 1920×1080, 1440×900, 1024×768, and 390×844. The final mismatch ledger has no unresolved serious product mismatch.
- Axe has no serious or critical findings in focal flows; keyboard/directional focus and reduced motion pass.
- The documented reference machine with 10,000 indexed items records browse/search p95 at or below 200 ms excluding artwork transfer; virtualized rails and route bundle boundaries meet AC-018.
- Blocking external provider traffic leaves local profile/catalog/cached-artwork/search/detail/playback behavior working.
- Full Go unit/race/vet, frontend lint/type/unit/build, browser, media-integration, security, shutdown, and stack-base checks pass once against the frozen Delivery Unit 3 tree.
- A fixed-diff formal review covers intent, architecture, security, casting authorization, lifecycle, accessibility, maintainability, and boundary compliance before publication.

Any change to signed URLs, Cast profile, adapter state, progress reporting, HTTPS prerequisites, shared navigation/player UI, final fixtures, or lower-stack contracts invalidates the corresponding focused evidence and final gate.

### Atomic commit and pull request

Use one platform-adapter behavior commit and one bounded release-hardening/documentation commit in Delivery Unit 3. Pull request base `feat/playback`, stack position 3/3.

### Done when

Flixr screens, Google Cast, and AirPlay satisfy their owned/runtime-qualified paths; all 20 accepted criteria have traceable green evidence; no review-only artwork is published; the three-PR stack is verified and Delivery Unit 3 is ready for `open-pr` publication. Merge, release, and deployment remain outside authority.
