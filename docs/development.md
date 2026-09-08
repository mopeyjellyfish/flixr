# Development and demo

[Back to the README](../README.md) · [Docker setup](docker.md)

Run these commands from the repository root unless a command changes directory.

## Build from source

Install these tools:

- Go 1.27.1;
- Node.js 22 and npm;
- FFmpeg and ffprobe.

## Build and run

Build the frontend and copy it into the Go embed directory:

```bash
npm --prefix frontend ci
npm --prefix frontend run build:embed
```

Build and start Flixr:

```bash
cd backend
go build -o flixr .
./flixr
```

Flixr prints a one-time setup token. Open http://localhost:8787 and use that token to claim the owner account.
Native builds store data in `./flixr-data` and listen on `127.0.0.1:8787` by default. Set `FLIXR_LISTEN_ADDR=0.0.0.0:8787` to allow LAN devices to connect.

## Support diagnostics

An owner can download **Support diagnostics** from Server settings. The local ZIP
contains the Flixr build/runtime version, tool and scan health, active playback
count, and the latest 100 failure IDs. It contains no media names, filesystem
paths, passwords, tokens, configuration values, or database content. A failed
browser request shows its support ID; include that ID only when you choose to
share the archive with support. The records are in memory, so restarting Flixr
clears them. See [SUPPORT.md](../SUPPORT.md) before opening a public issue.

## Development demo

For UI development with live reload and the same 50-film / 50-show catalogue:

```sh
make demo-dev
```

Open **http://localhost:19879**. Vite provides React Fast Refresh and CSS updates;
the demo API and cached artwork run on port 19880 using the existing showcase
volume. Keep the command running. Stop Vite with Ctrl-C. Backend changes require
a rebuild/restart; frontend changes appear automatically. The regular `make demo`
command below serves a production build instead (stop Vite before using it).

The demo banner links to `/dev/interior`, a development-only component playground.
All 54 Interior components are vendored with their MIT license and pinned source
revision in `frontend/src/vendor/interior/README.md`. The playground is a lazy
chunk and is available only in Vite development or an explicit demo server.

The startup splash waits for profile/catalogue data and visible images, then
reveals the page. Missing profile pictures are deterministic SVGs generated locally;
there is no avatar-service or AI-service dependency. Reduced-motion preferences
skip the zoom transition. A failed request reveals a retry screen.


```bash
make demo
```

Open **http://localhost:19879** and select **Alex**. This dedicated development
showcase has its own `flixr-showcase-data` volume and no media mounts. It does not
change the real installation or the older `flixr-demo` container, if one exists.

Python 3 prepares **50 movies and 50 TV shows** with real posters, TV backdrops
where available, descriptions, genres, years, and up to 24 episodes from the
first two seasons. No video or audio files are downloaded. The default selection
is curated from well-known titles, **not a live top-rated chart**. Film metadata
comes from [Wikipedia via prust/wikipedia-movie-data](https://github.com/prust/wikipedia-movie-data);
TV metadata comes from [TVmaze](https://www.tvmaze.com/api). Credits appear in the
demo banner. Original artwork remains its owners' property. Downloaded assets stay
in the ignored local `.demo/` cache and are excluded from the runtime image; the
README banner and screenshots display sample titles for demonstration.

New showcases have **Alex** and **Guest** profiles without PINs, and **Sam** with
PIN **`2468`**. The demo-only owner password is **`flixr-demo-only`**. Existing owner
credentials and profiles are never overwritten. Sample My List and Continue
Watching entries are editable and persist. Expand the **Development demo** banner
for navigation and credentials. Try profiles, PINs, search, movie/TV filters,
rows/grids, sorting, lists, episode details, and owner settings. Playback and
remote playback are unavailable without media; the backend rejects demo playback.

For a TMDB top-rated snapshot, set `TMDB_READ_ACCESS_TOKEN` in your local
environment, run `python3 scripts/prepare-demo.py --refresh`, then `make demo`.
This downloads the first 50 top-rated results per category with available
backdrops and first-season episodes. The token is never written to the snapshot
or passed into the container.

- `make demo-down` stops the showcase and preserves its data.
- `make demo-reset` deletes only the showcase volume; the next start recreates the
  sample household. Downloaded artwork remains cached.
- `make demo-smoke` verifies counts, cached artwork, profiles, and rejection of
  playback. It requires `curl`, `jq`, and the Alex sample profile.
- `FLIXR_DEMO_PORT=19881 make demo` changes the local port.

Initial preparation requires internet access. Later runs reuse `.demo/`. To start
the existing image offline without rebuilding, run
`docker compose up --detach --wait --no-build`. All cached demo assets are served
locally. Normal startup never imports `.demo/` or creates sample credentials.

## Checks

Run the Go checks:

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
```

Run the frontend checks:

```bash
npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run build
```

The committed media acceptance corpus is generated locally, with no downloads:

```bash
bash backend/scripts/generate-media-fixtures.sh
cd backend
FLIXR_REQUIRE_MEDIA_INTEGRATION=1 go test -tags=media_integration ./playback
```

Review `backend/testdata/media/manifest.json` after regeneration. Its hashes describe the committed bytes from FFmpeg 8.0.1; a different generator version may preserve the declared streams while changing bytes.

Run the mocked browser matrix after Playwright Chromium is available:

```bash
npm --prefix frontend run test:e2e -- --project=chromium
```

The production browser suite runs against the built `flixr` executable. See `.github/workflows/ci.yml` for the complete fixture and environment setup.

To verify the local-first contract, run `make test-offline` after `npm --prefix
frontend ci` and installing Playwright Chromium. This builds a disposable Docker
installation on an internal network with no default internet route and runs the
production setup, scan, playback, and screen-control flow through a local TCP
relay. The main browser context rejects requests to external origins. It also checks durable
owner state after restart. The command removes only its own temporary containers,
network, image, and data volume. Screenshots and traces go to
`output/playwright/offline/`.

### Artwork cache maintenance

Downloaded originals live under the persistent data directory and are never removed
by artwork maintenance. Requests from catalog cards include a bounded display width;
the server keeps JPEG derivatives separately, deduplicates identical concurrent
requests, and limits resize work to two jobs. Failed, unsupported, or pressured
derivative creation serves the original cached image instead. Derivatives are
atomic temporary-file writes keyed by the original hash and one of eight width
buckets. Startup and six-hour maintenance sweep at most 256 derivative entries,
records its last run outcome, removes stale temporary files, and evicts expired or
over-budget entries as encountered above 128 MiB or 64 entries. This does not touch SQLite, settings,
history, media roots, or playback segments. Database and deployment-log retention
are documented separately in [the Docker guide](docker.md#database-and-log-maintenance).

If port 4173 is already occupied, use `FLIXR_TEST_PORT=4180 npm --prefix frontend
run test:e2e -- --project=chromium`. Tests refuse to reuse an unrelated server.

### Responsive verification

Start `make demo-dev`, then run the reusable check through Playwright CLI:

```sh
npx --package @playwright/cli playwright-cli --session flixr-responsive open http://localhost:19879
npx --package @playwright/cli playwright-cli --session flixr-responsive run-code "$(cat scripts/check-responsive.js)"
```

The check requires the isolated demo and its Alex profile. It exercises rows, grids,
detail dialogs, profile selection and sign-in from 320px through 8K, then resizes
back to desktop to catch stale measurements. It checks navigation/page bounds and
poster scaling without hiding failures behind screenshot-only assertions.

## Repository layout

- `backend/` — Go server, SQLite migrations, catalog, household, and embedded web assets.
- `frontend/` — React, TypeScript, Vite, Tailwind CSS, unit tests, and Playwright tests.
- `docs/features/flixr-core/` — accepted pitch, delivery plan, and validation evidence.
- `DESIGN.md` — accepted Cobalt Signal interface direction.

### Progress ordering and watched state

Each admitted playback plan acquires a durable, server-issued generation for its profile
and catalog item. A newer plan supersedes previous playback sessions for that
item. Failed session creation preserves the current generation; a manual action
during preparation defeats the candidate plan. Mark watched or start over advances the same generation atomically for
all selected items, so old playback messages cannot undo the action. Starting
a plan preserves completion until the new playback reports an observation;
completed items start at zero. Completion is set by `ended: true` or a position
at or beyond 90% of the catalog duration, and remains set within that playback.

Heartbeat and seek requests accept a positive integer `observation`, incremented
by the player for every seek, heartbeat, ended event, page-hide beacon, and final
write. The server compares it only within the session's generation. Duplicate or
lower observations and messages from superseded generations are acknowledged
without changing progress. HLS session replacement preserves that generation.
`updated_at` and `completed_at` always use server time. Client `observed_at` is
accepted for older clients but ignored. Older players without `observation`
continue in receipt order within their generation; upgrading the client is
required for ordering delayed messages within one playback. Lease expiry, stop,
and shutdown never resave a cached position.

### Continue Watching curation

Each profile can hide a film or series from Continue Watching without changing
playback progress, history, or My List. The card action removes the logical title
from that profile's row and offers Undo. A hidden title can also be restored from
its detail view. These choices survive server restarts and do not affect another
profile.

`DELETE /api/v1/catalog/continue-watching/{film|series}/{catalogID}` hides a
logical title for the authenticated profile; `PUT` restores it. A newly admitted
user playback plan also restores the title. Playback-plan recovery and automatic
episode advancement preserve the dismissal, so a series cannot reappear merely
because its next episode was selected.

The player retries transient server failures, expired playback leases, and
compatibility-stream network errors with bounded delays. A reconnect event can
advance a scheduled retry, while the retry timer also works without that event.
Every replacement plan starts from the server's last acknowledged position;
unsent browser time is not treated as durable progress. Revoked authorization,
unsupported media, unavailable FFmpeg, and exhausted capacity stop recovery.
Leaving playback, selecting another profile, or logging out cancels outstanding
retries and releases replacement sessions that finish after cancellation. On
player exit or unmount, the final progress acknowledgement gets at most two
seconds before the player requests session stop; a hung request cannot retain
the client or delay navigation.

Text subtitle discovery, filename rules, profile selection behavior, extraction
limits, and the downloaded-content qualification gap are documented in
[Text subtitles](subtitles.md).

The legacy `GET /api/v1/progress/{id}` also returns `generation`. A legacy `PUT`
must echo that value; each accepted write advances it. An omitted generation
means zero and works only for initial or migrated progress that has not yet
changed. A stale generation returns HTTP 409 `progress_conflict`; read the latest
state and let the viewer decide whether to retry. Never automatically retry an
old position with a newly read token. The shipped player uses playback sessions
rather than this legacy route.

### Episode sequence and autoplay

When an episode ends and its completed heartbeat is acknowledged, Flixr asks the
server for the next playable, unwatched episode for the active profile. Episodes
are ordered by numeric season and episode numbers. Gaps in the files are skipped,
as are episodes that this profile has already completed. Season 0 specials are
excluded from the normal queue; API clients must opt in with
`include_specials=true`.

Automatic advancement stays within the current TV root and edition/version
folder. If the next number has multiple playable files in that same context, or
only a different cut/context is available, Flixr does not guess and shows that no
next episode is available in this version. A ten-second countdown offers Play now
and Cancel autoplay. Pause, a hidden tab, leaving the player, or cancellation
prevents a pending advance. The player stops the completed session before opening
a fresh playback session for the next episode; profile-scoped playback preferences
are selected again for that new session.

## Personal history and ratings

Personal ratings are profile-scoped local records. They are never provider metadata. The viewing ledger is immutable and records a snapshot of the catalog ID, title, and kind without a foreign key to a catalog row, so a temporary scan removal does not erase history.

`GET /api/v1/history?limit=25&before=<cursor>` returns at most 100 events per page. `PUT` or `DELETE /api/v1/ratings/{catalogID}` changes the selected profile's rating. `POST /api/v1/history/import` accepts source events; an omitted `source_time` remains unknown rather than becoming an invented time. The server assigns internal event IDs and receipt timestamps; imports retain their original identity and timestamp in `source_id` and `source_time`. Completion writes use a random token persisted for the playback generation, so repeated observations deduplicate while later playbacks still create events after a catalog removal and re-add.

`POST /api/v1/history/clear` hides the current profile's prior events while retaining the ledger. Its response has an ID and five-minute `undo_until`; `POST /api/v1/history/clear/{id}/undo` restores that clear within the window. Explicit watched/unwatched actions remain current-state progress actions and do not rewrite history.

## Logical titles and identity repair

Catalog IDs identify logical titles. Migration 019 preserves existing IDs and
adds private physical-source records, including a full SHA-256 digest. First
indexing and changed bytes require a full read; unchanged files reuse persisted
proof and probe results. An unchanged pre-019 source receives its full digest on
first playback admission, without requiring a library-wide rescan; cancellation
or changed file evidence prevents admission. macOS and Linux also compare file
identity and change time to detect equal-size replacements that preserve
modification time. Other platforms use size and modification time for cache
validation.

Renames and moves retain an unambiguous title only with matching full-content
proof. Sampled fingerprints and provider IDs alone never merge titles. A legacy
file that disappeared before receiving a full digest needs an owner decision.
A same-path replacement retains its title unless valid provider or episode
evidence contradicts it. Missing files leave unavailable titles and their
progress, My List membership, viewing ledger, and owner metadata intact.
Byte-identical duplicates retain their physical sources and keep the existing
primary while it is present.
Each configured film or TV location records whether its last trusted scan completed
and whether the location was available. If a populated root becomes empty, or a scan
loses at least 10 files and half of that root, the scan stops before publishing any
catalog changes and records an owner review. A later complete scan clears stale review
state when the files return. The owner can instead confirm the exact captured review;
that marks its missing physical sources unavailable without deleting logical titles or
household state. Missing or unreadable roots and cancellation retain the last complete
location and catalog state.
Admitted playback pins a private source version; a changed source requires a new
playback plan instead of changing bytes under an existing session.

In **Server settings → Metadata → Identity repair**, review the conflict and
explicitly choose which title keeps its identity. Merge retains both original
anchors and metadata, snapshots each profile's progress, and resolves immutable
history events to the survivor once. Unmerge restores the original mapping and
unchanged progress; newer playback or a manual reset remains intact and is
reported in the repair history. Series repairs list each affected episode.
Original event title/kind and `original_catalog_id` remain available in history.

The owner-only HTTP contract is `GET /api/v1/owner/identity/repairs`,
`POST /api/v1/owner/identity/merges` with `kind`, `survivor_id`, and `source_id`,
and `POST /api/v1/owner/identity/merges/{id}/unmerge`. Overlapping active repairs
or repeated undo return HTTP 409 `identity_conflict`. Responses contain public
title details and reconciliation decisions, never filesystem paths or digests.

Library scan status adds per-location availability through
`GET /api/v1/owner/scan/status`. Confirm a pending review with
`POST /api/v1/owner/scan/removals/confirm` and the exact `scan_id` and `root_kind`
returned by that status. Stale confirmations return HTTP 409
`removal_review_changed`; physical candidate paths are never returned.

Player controls and local chapter/preview limits are described in [Watching with FlixR](player.md).
