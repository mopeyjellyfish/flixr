# Automatic Metadata Access Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make official FlixR installations automatically enrich ordinary libraries through project-owned application access while preserving overrides, offline playback, truthful status, and provider obligations.

**Architecture:** Catalog resolves disabled/override/application state without exposing credentials and records successful credential revisions for bounded upgrade refresh. Official release builds inject the FlixR application token through BuildKit, while local builds stay unconfigured. Existing scan and artwork transactions remain the only enrichment publisher.

**Tech Stack:** Go 1.27, SQLite, React/TypeScript, Docker BuildKit, GitHub Actions, Node release-policy tests, Playwright.

**Spec:** `docs/superpowers/specs/2026-09-08-automatic-metadata-access-design.md`

## Global Constraints

- Never reuse or invent a provider credential, and never describe a distributed credential as secret.
- Do not require household TMDB registration.
- Preserve local playback, owner field locks, metadata history, exact identity, and cached artwork on every provider failure.
- Keep releases on 0.x; do not merge or close #176 before independent review, green CI, official access, published-image acceptance, and private home verification.

---

### Task 1: Credential policy and status

**Files:** `backend/catalog/settings.go`, `backend/catalog/catalog.go`, `backend/catalog/settings_test.go`, `backend/web/server.go`, `backend/web/metadata_settings_test.go`

**Interfaces:** Catalog gains application credential, enabled state, effective source, and revision methods. The owner endpoint accepts token changes and an independent enabled toggle and returns redacted status.

- [ ] Write failing catalog and HTTP tests for application fallback, override precedence/removal, disable preservation, invalid explicit override, and redacted status.
- [ ] Run targeted tests and confirm failures describe the missing policy.
- [ ] Implement the minimal resolver, persistence, environment lock integration, and status contract.
- [ ] Run the targeted tests and refactor while green.

### Task 2: Provider failure and refresh lifecycle

**Files:** `backend/catalog/tmdb.go`, `backend/catalog/tmdb_test.go`, `backend/catalog/catalog.go`, `backend/catalog/enrichment_test.go`, `backend/main.go`, `backend/main_test.go`

**Interfaces:** TMDB returns typed invalid/rate-limit/unavailable errors. Catalog decides whether the current credential/catalog revision needs refresh and records only a fully successful scan.

- [ ] Write failing tests for bounded Retry-After, error classification, credential rotation, an upgraded credential-less catalog, no provider calls while disabled, and preservation after failure.
- [ ] Run each focused test and confirm the intended red state.
- [ ] Implement typed errors, bounded retry, revision/age persistence, and startup scan selection through the existing scan worker path.
- [ ] Run catalog and main tests, then refactor while green.

### Task 3: Configuration and owner UI

**Files:** `backend/config/environment.go`, `backend/config/environment_test.go`, `backend/config/inventory.go`, `backend/config/inventory_test.go`, `backend/web/settings_handlers.go`, `frontend/src/core/api.ts`, `frontend/src/features/setup/Setup.tsx`, `frontend/src/features/setup/Setup.test.tsx`, `frontend/src/features/owner/Owner.tsx`, `frontend/src/features/owner/Owner.test.tsx`

**Interfaces:** `FLIXR_METADATA_ENABLED` is an optional boolean environment lock. UI status distinguishes automatic access from personal/environment overrides and keeps the personal credential form in Advanced settings.

- [ ] Write failing Go and React tests for environment parsing, truthful setup state, source labels, independent disable, and advanced override controls.
- [ ] Run focused tests and verify the missing behavior fails.
- [ ] Implement configuration, API types, setup copy, and owner controls.
- [ ] Run focused backend/frontend tests and refactor while green.

### Task 4: Official attribution and operator guidance

**Files:** `frontend/src/features/owner/Owner.tsx`, `frontend/src/features/owner/Owner.test.tsx`, `frontend/public/tmdb-logo.svg`, `frontend/THIRD_PARTY_LICENSES.md`, `README.md`, `docs/configuration.md`, `docs/docker.md`, `docs/releases.md`, `docs/publication.md`, `flixr.env.example`

**Interfaces:** About/Credits contains the approved unmodified TMDB mark, TMDB link, and required notice. Documentation names exact registration, permission, rotation, cache, outage, and local-build procedures.

- [ ] Add a failing UI test for accessible TMDB attribution and the required notice.
- [ ] Implement the About/Credits presentation using an approved TMDB asset and update notices.
- [ ] Document the one-time maintainer and operator procedures without credentials or private household data.
- [ ] Run the focused UI test and frontend checks.

### Task 5: Release wiring and normal-mode acceptance

**Files:** `Dockerfile`, `.github/workflows/ci.yml`, `scripts/release-image.sh`, `scripts/release.test.mjs`, `scripts/metadata-acceptance.sh`, `scripts/metadata-acceptance.test.sh`, `backend/web/metadata_acceptance_test.go`, `frontend/e2e/metadata-provider.spec.ts`

**Interfaces:** Release prepare requires `FLIXR_TMDB_APPLICATION_TOKEN`, forwards it as BuildKit secret `tmdb_application_token`, and embeds it only in the backend executable. Acceptance supplies a fake application credential at link time and uses no household token.

- [ ] Write failing release-policy and normal-mode acceptance tests for credential-optional compatibility, explicit distribution-gate refusal, non-leaking BuildKit forwarding, fresh install, upgrade refresh, and offline cached browsing.
- [ ] Run the script/unit tests and confirm the expected failures.
- [ ] Implement Docker/release/CI wiring and extend acceptance fixtures.
- [ ] Run release, backend, frontend, browser, Docker, and offline checks required by `CONTRIBUTING.md` through the coordination validator where heavy.

### Task 6: Review and delivery

**Files:** all changed files and GitHub PR metadata.

- [ ] Inspect the complete diff for credentials, private media, debug output, and accidental unrelated changes.
- [ ] Run fresh targeted and full verification and record exact results.
- [ ] Commit with Conventional Commits, push, create a PR linked to #176 without closing it, and request independent review.
- [ ] Keep the Project item In Progress and report the exact external provisioning and home-verification blockers.
