# Automatic Metadata Access Design

## Outcome

Official FlixR 0.x images enrich ordinary movie and TV libraries through a FlixR-owned TMDB application credential. A household can install the official Compose file without registering with TMDB. Local and source builds without an application credential report metadata as unavailable rather than implying that automatic access exists.

Publication remains blocked until the maintainer registers a FlixR application, establishes that the intended public binary distribution is permitted, and provisions the release secret. No other project's credential may be used.

## Credential resolution

The released binary accepts an application credential through a link-time variable. Docker BuildKit mounts the release credential only in the backend build step; it is not copied into an image layer or printed. The credential is nevertheless part of the distributed executable and must be described as an application identifier, not a secret.

Catalog keeps three pieces of state separate:

1. `remote enabled`, persisted for owner control and optionally locked by `FLIXR_METADATA_ENABLED`;
2. the saved owner credential, optionally replaced and locked by `FLIXR_TMDB_TOKEN` or `FLIXR_TMDB_TOKEN_FILE` at startup;
3. the nonpersistent application credential supplied by the binary.

Resolution is: disabled, explicit override, application default, unavailable. Deleting a saved override reveals the application default. Disabling metadata preserves both override and application credentials. An invalid explicit override remains an actionable error and never silently falls through to the application credential.

Credential bytes and their revision markers never appear in HTTP responses, logs, release output, or exported configuration. Status reports only `enabled`, `configured`, `source`, `state`, and a bounded human-readable message.

## Provider behavior and refresh

TMDB requests remain server-side. The existing scan worker limit bounds concurrency. The TMDB client classifies invalid credentials and rate limits, honors a bounded `Retry-After`, and performs at most one retry. Provider errors retain the previous catalog and artwork transaction and never block local playback.

The catalog stores a one-way revision of the effective credential and the time of the last successful provider refresh. When a configured credential revision has not completed a scan for the current catalog, startup starts one ordinary scan even when `FLIXR_SCAN_ON_START` is false. The same mechanism schedules refresh before TMDB's six-month cache ceiling. A successful complete scan records the revision and refresh time; partial or failed scans remain due. One scan covers fresh and upgraded libraries, uses the configured worker bound, and preserves #164's exact identity, field locks, local overrides, viewing history, and transactional artwork cache.

When remote metadata is disabled or unavailable, scans retain cached metadata and artwork and perform no provider calls. Documentation explains that stopping TMDB use requires purging provider-derived cache and that prolonged offline use cannot renew provider content beyond the provider's terms.

## Owner experience

Setup describes automatic metadata as included only when the running binary has a usable application credential. Server settings show automatic, personal override, environment override, disabled, rate-limited, invalid, and provider-unavailable states. The personal token form moves under Advanced settings. Owners can remove a personal override and toggle remote metadata independently.

An About/Credits section displays an approved TMDB logo, links to TMDB, and includes the required non-endorsement notice. Release and operator documentation records attribution, rotation, local-build behavior, outages, cache renewal, and the one-time maintainer provisioning procedure.

## Release boundary

Pull-request and local Docker builds work without a default and test the truthful unavailable state. The official main-branch release path refuses to prepare an image unless the dedicated release secret is present, passes it to BuildKit without logging it, and verifies inside a test image that the application source is selected without a household token. The existing 0.x-only release guard remains unchanged.

The issue stays open after plumbing lands. Closure additionally requires the registered FlixR credential and distribution permission record, a published multi-architecture image verified without household credentials, and private home-library verification without publishing titles or paths.

## Verification

Tests cover precedence, removal, disabled behavior, invalid override behavior, application rotation, revision-triggered upgrade refresh, rate-limit backoff, cache retention during outage, redaction, setup/settings copy, attribution, release-secret handling, and normal-mode fresh/upgrade acceptance. Repository checks follow `CONTRIBUTING.md`; heavy commands run through the coordination validator.
