---
status: accepted
---

# Plan: Issue #53 — bounded catalog queries and browse payloads

Intent: https://github.com/mopeyjellyfish/flixr/issues/53. Base: `origin/main` at `5b5c501`; prerequisite #40 is complete. Keep the existing Editorial Stream / poster-grid direction in `DESIGN.md`. Do not change playback, household setup, or media roots.

## Review evidence

- **Applicability:** Go-targeted: `backend/catalog`, `backend/web`, SQLite browse indexes, and their public HTTP contract. Read installed `go` skill; Cobra/Viper is not applicable.
- **Fixed document:** `docs/features/bounded-catalog-queries/plan.md`, revised §001 cursor contract after the first review.
- **Status:** Approved in the replacement fixed-document `go-spec-reviewer` pass (`632f62d0`); no remaining material blockers or questions.
- **Invalidation:** A change to the proposed query boundary, authorization model, cursor contract, or acceptance requires a new review; wording-only edits do not.

## Execution mode

Checkpointed implementation (default). Whole-plan approval permits one named delivery unit's bounded commit/push/ready PR, not merge, release, deployment, cleanup, or unrelated work.

## Delivery topology

| Delivery unit | Topology | Branch | PR base | Dependencies | Checks | Ownership | Integration | CI fan-out | Cascade cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | Standalone, one PR, three serial slices | `issue-53-bounded-catalog-queries` | `main` | #40 done; slices 1 → 2 → 3 | Targeted Go/React tests, Go test/race/vet, frontend lint/type/test/build, representative browser checks, existing CI | Sole writer in `.worktrees/issue-53-bounded-catalog-queries` | PR against `main` | Existing CI only | Low: one branch and review boundary |

The plan and code share this PR. No parallel writer: query, HTTP, and UI contracts overlap. No stack or separate planning PR.

## Critical path, dependencies, and lanes

Critical path: prove current whole-library view → bounded policy-aware catalog page → HTTP contract and section navigation → incremental virtualized client and browser proof → frozen diff, review and CI. One active lane, one PR, no integration merge. Expensive gates are the 10k/100k fixture proof, Go race and multi-browser evidence. Preserve old profile/list/watched semantics and empty fixed Home rows. Before implementation, record a failing focused test at the public API. If the policy-to-SQL parity cannot be proven within this boundary, stop and revise the plan rather than paginate before authorization.

Invalidation map: catalog/query changes invalidate catalog and HTTP access/count/order tests and fixture budget; HTTP contract changes invalidate typed client, browse tests, and docs; UI state changes invalidate focus, cancellation, desktop/mobile browser checks; schema changes invalidate SQLite upgrade tests. Run full affected gates once on the frozen unit, not after each slice.

## [x] 001 — Return bounded, stable, authorized viewer pages

### Outcome and requirement trace

A 10k/100k-title library never yields an entire catalog or genre rail in one view response. Rows/grid retain profile preferences, My List and Continue Watching semantics. Each page uses a stable ordering key including catalog ID; unauthorized titles do not affect counts, section names or page boundaries. Request cancellation stops database work. Existing profile access, local-only operation and media-root confinement remain intact.

### Seam and files

`GET /api/v1/catalog/view?media=all|film|series&section=<name>&cursor=<opaque>&limit=<bounded>` returns preference, bounded items for a requested section/grid, and a `next_cursor` only when more authorized items exist. Omitted cursor starts the first page, including on the existing `?media=all` URL; a supplied malformed or incompatible cursor returns `400 invalid_request`. Home's fixed section descriptors and bounded genre descriptors are available without materializing their contents. Preserve the existing route and preference names; document the response shape change. Use `backend/catalog/viewer.go`, `backend/catalog/access.go`, `backend/web/viewer_handlers.go`, `backend/web/access.go`, `backend/sqlite/migrations/` only if query-plan evidence requires indexes; `backend/catalog/*test.go`, `backend/web/viewer*_test.go`, `backend/web/profile_access_test.go`, `docs/development.md` for proof/contract. Keep policy evaluation before count/order/page. Implement it at the catalog query boundary using the actual library IDs, tags and rating rules; verify parity with `access.Policy.Allows` on unrestricted and restricted fixtures. Do not filter an already-limited page in the HTTP handler. Use `QueryContext`/`QueryRowContext`. Cursor position is the last sort tuple plus ID and kind; bind its version to the requesting profile, normalized policy fingerprint, media, section and current sort. Reject mismatched bindings, including changed profile policy or preference. Validate and parameterize all cursor fields; do not put credentials or private paths in cursors. For an unchanged catalog, keyset pages cannot skip or repeat IDs. A scan or independent metadata/progress edit can reorder titles between requests: this is a live view, not a cross-request snapshot. Refresh from the first page after client-owned mutations; independent concurrent edits may require a manual refresh. Tie-break on ID (and kind if needed) across films and series. Do not return a full total by scanning/materializing every item on each page.

### Dependencies

#40 complete. No other issue is an implementation prerequisite.

### Execution lane and ownership

Serial; sole writer in the active issue-53 worktree.

### Red proof

Existing `GET /catalog/view` returns all items. A new HTTP/catalog test with repeat-sort ties and restricted policies first fails on response size, first-page request without a cursor, page continuation, and allowed-only boundaries; an interrupted query test verifies cancellation.

### Green proof and checks

Tests cover title/year/added/watched, duplicate sort keys, media filters, My List/Continue Watching, empty fixed rows, version members, malformed/incompatible cursors, profile and policy/preference changes, 10k/100k fixtures and authorization before pagination. Explicitly test stable pages with unchanged ordering data; test a concurrent edit as live-view behavior, not a snapshot guarantee. `go test ./catalog ./web ./sqlite` from `backend`; query-plan and bounded-payload evidence at both sizes. If schema changes, test upgrade from the prior version. Query edits invalidate these tests and budgets.

### Atomic commit and pull request

Atomic commit: bounded catalog and HTTP page contract. Delivery unit 1, standalone PR to `main`.

### Done when

The server returns at most the validated page limit per section, no denied title/section/count leaks, repeated requests use consistent order, and request cancellation propagates to SQL.

## [x] 002 — Load section pages into the existing virtualized viewer

### Outcome and requirement trace

Home, Movies and TV render an initial bounded page; scrolling or explicit navigation loads more without resetting keyboard focus. Obsolete section/search requests abort on navigation, preference change and profile change. The viewer still supports detail, My List, watched and dismissal actions even for an item not yet loaded elsewhere.

### Seam and files

`frontend/src/core/api.ts`, `frontend/src/api/client.ts`, `frontend/src/features/browse/Browse.tsx`, `frontend/src/features/browse/Browse.test.tsx`, `frontend/src/modules/mediaCollection/MediaCollection.tsx` and its tests, `frontend/e2e/mock-api.spec.ts`. Reuse `@tanstack/react-virtual`, existing cards, native buttons and accessible status messages; do not redesign the UI or add a global store. Use a cursor for each section/grid, deduplicate IDs during paging, and retain focus on the existing card when appending. Remove search's full-viewer preload; obtain needed item/profile state from bounded page/detail or a bounded state seam. Keep errors local to the affected section and offer retry without discarding previous pages.

### Dependencies

Slice 001 page contract and authorization.

### Execution lane and ownership

Serial, same worktree/writer. Existing `DESIGN.md` and viewer direction are accepted visual evidence; use `frontend-development`, React/TypeScript methods, and `visual-validation` for material UI changes.

### Red proof

A Browse test first shows only the first page and loses later items/focus; a reversed-response test shows an obsolete request winning; a search test proves it still downloads the whole viewer model.

### Green proof and checks

Focused Vitest tests cover per-section page append, cancellation, retries, keyboard focus after fetch, profile/preference/list changes and no full-model search fetch; mocked browser checks on desktop and mobile inspect scrolling and focus. Record any visual mismatch and recheck it. `npm ci` in fresh worktree before first frontend test; then `npm test -- --run src/features/browse/Browse.test.tsx`, `npm run lint`, `npm run typecheck`, `npm run build`. Client changes invalidate browser and type proofs.

### Atomic commit and pull request

Atomic commit: incremental viewer and focused accessibility proofs. Delivery unit 1.

### Done when

A large catalog is discoverable without full-library downloads and loading does not steal keyboard focus or show stale results.

## [x] 003 — Verify production contract, scale and recovery

### Outcome and requirement trace

Both 10k and 100k fixtures show bounded response bytes/items; filters and sort remain stable; failures, canceled requests and server restart yield a usable fresh page. No acceptance claim relies on the metadata demo.

### Seam and files

`backend/catalog/performance_test.go`, `backend/web/viewer_contract_test.go`, affected frontend/browser tests, `docs/development.md` and the PR evidence. Preserve existing direct URL/detail/playback access rules.

### Dependencies

Slices 001–002.

### Execution lane and ownership

Serial, same worktree/writer. Parent owns final verification and independent review of the frozen diff.

### Red proof

Before-state 10k/100k response-size and restriction measurements show the unbounded behavior; failing restart/cancellation tests establish the relevant recovery gap.

### Green proof and checks

Record fixture size, exact command, latency and response bytes. Run affected Go tests plus `go test ./...`, `go test -race ./...`, `go vet ./...` from `backend`; frontend `npm test`, lint/typecheck/build, browser tests for desktop/mobile focus and failed fetch. Use existing CI for further platform proof. Risk-select independent fixed-diff review for policy, cursor and public-contract changes; document residual real-media/device limits in PR. A revision to catalog, API or UI invalidates the matching proofs above.

### Atomic commit and pull request

Atomic commit: fixture/recovery evidence and docs. Delivery unit 1. After final checks and review, commit and open one ready PR to `main`; do not merge or publish a release without separate authority.

### Done when

Every issue acceptance criterion has targeted evidence or an explicit blocker; checks are green and the PR states the measured limitations.
