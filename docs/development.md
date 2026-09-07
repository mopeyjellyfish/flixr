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
records its last run outcome, removes stale temporary files, and evicts overflow
entries oldest-first above 128 MiB or 64 entries. This does not touch SQLite, settings,
history, media roots, or playback segments. SQLite remains WAL-backed; use normal
backups and do not run a blocking `VACUUM` while playback is active.

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
