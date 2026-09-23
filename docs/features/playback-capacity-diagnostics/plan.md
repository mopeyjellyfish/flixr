---
status: accepted
---

# Plan: Issue #50 — explain active playback and server capacity

Intent: https://github.com/mopeyjellyfish/flixr/issues/50, Project 3 Ready implementation item with delivery order 027. Prerequisites #27 and #30 are closed. Branch `issue-50-playback-capacity-diagnostics` starts at `origin/main` `b73b714`. Preserve the established owner-only Playback & screens surface and `DESIGN.md`; do not redesign the player, change media roots, or add client/device registration. User decisions: Stop targets an individual direct/remux/transcode session through a separate non-bearer owner handle; show a known connected screen only if already bound, otherwise “Unknown device” rather than guessing.

## Review evidence

- **Applicability:** Go-targeted: playback manager lifecycle and owner HTTP authorization. Follow the installed `go` skill; Cobra/Viper does not apply.
- **Fixed document:** `docs/features/playback-capacity-diagnostics/plan.md`, revised after the first review's handoff/capacity findings.
- **Status:** Approved in replacement fixed-document Go-spec review (`/tmp/flixr-issue-50-go-spec-replacement.md`); no material blockers or questions.
- **Invalidation:** Any change to owner-handle authority, stop semantics, paging, or the public owner activity response requires a replacement review.

## Execution mode

Checkpointed implementation. Whole-plan approval authorizes one named delivery unit's bounded commit, push, and ready PR; it does not authorize merge, release, deployment, branch deletion, worktree cleanup, issue closure, or unrelated changes.

## Delivery topology

| Delivery unit | Topology | Stack position | Branch | Pull request base | Dependencies | Checks | Ownership | Integration point | CI fan-out | Cascade cost |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 1 | One standalone PR, three serial slices | Standalone | `issue-50-playback-capacity-diagnostics` | `main` | #27, #30 complete; slices 001 → 002 → 003 | Focused playback/web/UI tests; full Go test/race/vet/build; frontend lint/type/test/build/bundle; mocked desktop/phone and existing CI real-media checks | Sole writer in issue-50 Worktrunk worktree | PR to `main` | Existing six required CI jobs | Low: one branch, review boundary and PR |

The plan and behavior share this PR. No parallel writers: the owner response, playback session lifecycle and owner UI overlap.

## Critical path, dependencies, and lanes

Prove the current owner status includes compatibility generations but not individual direct sessions → add safe bounded activity and stop seam → render it in the incumbent owner panel → verify authorization, recovery, accessibility and CI. One serial lane, one PR. Expensive gates: Go race, page bounds under many direct sessions, and browser/real-media acceptance. No SQL migration or new persistent device identity. Invalidation: manager changes rerun manager/web lifecycle and race tests; HTTP contract changes rerun owner/profile auth and typed client; UI changes rerun owner tests plus desktop/phone browser checks; the final frozen diff receives independent security/lifecycle review. The CI release job is a post-merge gate, not authorization to merge.

## [ ] 001 — Provide bounded, owner-only playback activity

### Outcome and requirement trace

An owner can see why a session uses direct, remux or transcode, its selected audio/subtitle and effective output quality, start time, a safe device label, and bounded cache/generation usage. A profile or unauthenticated caller cannot enumerate it. Owner activity must not return stream/session bearer tokens, source paths, input URLs, private media paths, credentials or raw FFmpeg logs. Existing `GET /owner/playback/status` and playback plan responses remain compatible.

### Seam and files

Add `GET /api/v1/owner/playback/activity?limit=<bounded>&cursor=<opaque>` under the existing `s.owner` guard. Return a capacity summary (configured generation count and cache budgets, active/starting counts), a page of sanitized live session summaries, and a continuation cursor only when more exist. Default page 24, maximum 100; order by immutable session creation time (store separately from renewed expiry) plus independent owner handle as tie-breaker. An omitted cursor starts the first page; malformed cursor returns `400 invalid_request`; a live session change between pages is not a snapshot. Catalog titles can be looked up only for the bounded page; use a neutral label if unavailable. Use the existing playback manager for an in-memory snapshot; never hold its mutex during title I/O or directory walks. Reuse existing `Plan` facts to derive bounded reason codes/labels rather than serializing internal plan or FFmpeg diagnostics. Device is “Unknown device” unless an existing explicit binding is proven; do not infer it from User-Agent or a connected screen merely being present. Owner handles are independently random, stored only in memory, never equal to or derived from playback bearer IDs, and rotated for replacement sessions. No new durable record or polling service. Candidate files: `backend/playback/manager.go`, `backend/playback/sessions.go`, `backend/web/playback_handlers.go`, `backend/web/server.go`, new focused tests, `frontend/src/core/api.ts` and `frontend/src/api/client.ts`.

### Dependencies

Existing manager and owner status endpoint; #27 and #30 done.

### Execution lane and ownership

Serial, sole writer in the issue-50 worktree.

### Red proof

A manager/web test first fails because a direct session is absent from owner activity and a non-owner can have no authorized equivalent; a many-session fixture asserts page bounds and no raw bearer token/path in encoded JSON.

### Green proof and checks

Test direct/shared-generation/expired/replaced sessions, reason/track/quality mapping, missing title, cursor tampering, 100+ direct sessions, owner/profile/anonymous status, no secrets/paths and no directory walk under a manager lock. `cd backend && go test ./playback ./web`; contract/schema typecheck on frontend. Changes to snapshots or DTO invalidate these proofs.

### Atomic commit and pull request

Manager snapshot and owner activity HTTP contract with focused tests; delivery unit 1, PR base `main`.

### Done when

The owner gets a bounded, path-free session page and capacity facts; no other principal can see them and existing plan/status contracts still work.

## [ ] 002 — Stop one active session safely and explain capacity

### Outcome and requirement trace

The owner can stop a selected direct/remux/transcode session without seeing or supplying its playback token. A stop cannot accidentally stop a replacement session or leave a generation consuming capacity. A capacity error offers an actionable, non-sensitive remedy to the affected viewer and actionable current limits to the owner.

### Seam and files

Add `POST /api/v1/owner/playback/sessions/{owner_handle}/stop`; require same-origin and owner authentication before handle lookup. The manager resolves only an exact live owner handle under its mutex; it must not follow a resolved-handoff alias or stop a newer replacement. Atomically invalidate/remove the selected session's pending and resolved handoff associations and owned replacement credit, preserving the other live member, its lease and a normal expiry; then revoke only the selected session through existing lease/generation cleanup. A later handoff attach, reject or expiry must not revoke that survivor. A stale/expired handle returns not-found; stopping revokes future access, not media bytes already buffered by an in-progress `ServeContent`. Do not expose a lookup oracle to non-owners, reuse viewer-scoped stop authorization, log bearer IDs, or stop by catalog ID. Keep existing profile `playback_capacity` status/code; improve its generic UI text with “stop another compatibility stream or ask the owner to review playback limits,” without leaking limits or household activity. Show configured max generation slots, starting reservations, per-generation and total cache usage as separate owner context. Admission `ErrCapacity` is a slot reservation failure under valid settings (global budget already covers all configured slots); do not claim measured cache bytes caused admission refusal. Cache overflow is handled separately by sweep. Candidate files: manager/session and web handler/router tests, `frontend/src/core/api.ts`, owner client, player error text tests.

### Dependencies

Slice 001 handle and activity contract.

### Execution lane and ownership

Serial, same sole writer/worktree.

### Red proof

A focused manager/web test first fails to stop a direct session by owner handle while rejecting a profile, bad origin and stale handle. A client error test first expects an actionable capacity remedy.

### Green proof and checks

Cover direct and shared generation leases, a pending handoff stopped from either side, subsequent attach/reject/expiry preserving the other member, resolved handoff stale handles, pending preparation reservation, replacement races, duplicate stop, expired session, restart (handles do not survive), cross-profile owner authority, CSRF/owner guards, process cleanup and capacity released. `go test ./playback ./web`, `go test -race ./playback ./web`; frontend focused error tests. Manager or authorization edits invalidate lifecycle/security proofs.

### Atomic commit and pull request

Owner stop route and capacity remedy with regression tests; delivery unit 1.

### Done when

Only owners stop the exact live session they select; all other clients see no owner handles or diagnostics, and capacity failure has a useful next action.

## [ ] 003 — Show owner activity and recovery in the incumbent interface

### Outcome and requirement trace

The existing Playback & screens section displays a concise capacity summary and paged active sessions with kind, reason, selected streams, effective quality, safe device label, and Stop. Empty/loading/error states are explicit. After a confirmed Stop, refresh the page and restore focus; a stale handle produces a safe refresh prompt. No token, path or credential is rendered.

### Seam and files

Extend `frontend/src/features/owner/Playback.tsx`, `Owner.tsx`, typed API, owner tests, `frontend/e2e/mock-api.spec.ts`, and `docs/development.md`. Keep the existing resource-limit form separate from activity cards; use native headings, list, buttons and the existing confirmation pattern. Keep `DESIGN.md` owner-only direction and semantic colors/focus tokens. Avoid a new dashboard, animation or global store. The activity API only loads while the owner surface is active; a manual Refresh activity and Load more use cursor state. Represent unbound device as “Unknown device” rather than a claimed screen name. Handle pending stop, load-more failure, stale handle, empty state, and keyboard focus. Representative desktop and phone browser screenshots plus an explicit mismatch ledger; real media/device evidence is CI-limited and not inferred from mocks.

### Dependencies

Slices 001–002.

### Execution lane and ownership

Serial, same sole writer/worktree. Use `interface-craft` clarify, `frontend-development`, `react-best-practices`, and `visual-validation` against the accepted incumbent owner UI.

### Red proof

A focused React test first fails to show a direct session and offers no safe Stop; a deferred-response test shows an old page overwriting the refreshed activity after Stop.

### Green proof and checks

React tests cover owner-only load, stopped/expired session refresh, cancellation and error, cursor append, confirmation and focus, empty/unknown-device cases and absence of sensitive strings. Browser checks inspect desktop/phone focus and error states; compare screenshots to `DESIGN.md`, log mismatches and recheck. Run frontend lint/type/test/build/bundle and full backend test/race/vet/build, plus affected mocked browser tests; existing required CI covers production media/offline acceptance after PR publication. UI state edits invalidate visual and browser checks.

### Atomic commit and pull request

Owner panel, typed client, browser proof and docs; delivery unit 1, ready PR to `main` only after fixed review and final gates.

### Done when

An owner can understand and resolve an active capacity problem without disclosing private playback data, and the interface works with keyboard/touch at desktop and phone sizes.
