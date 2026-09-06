# Viewer experience validation

Validated on 2026-08-30 from branch `feat/viewer-experience`.

## Demo catalog evidence

- Explicit activation: `FLIXR_DEMO=true` in `compose.yml`.
- Seeded catalog: 20 films, 20 series, 20 seasons, and 40 episodes.
- Demo records use filename-only metadata. They have no external artwork or provider IDs.
- The container smoke selected a profile and verified exactly 20 films and 20 series in the `New` viewer row.
- Every returned demo title had `demo=true` and `playable=false`.
- A real `POST /api/v1/playback/plans` attempt returned HTTP 409 with `playback_not_playable`.
- An owner scan completed with zero failures and did not remove demo records.
- Counts and non-playable state remained unchanged after a container restart.

The title set is a presentation fixture, not a metadata-provider cache or a release-status guarantee. Official source spot checks used during selection included:

- Amazon MGM Studios, *Project Hail Mary*: https://www.aboutamazon.com/news/entertainment/project-hail-mary-ryan-gosling-amazon-mgm-studios
- Universal Pictures, *The Odyssey*: https://www.universalpictures.com/movies/the-odyssey
- Marvel Studios film slate: https://www.marvel.com/movies
- HBO and Max press materials: https://press.wbd.com/

## Container evidence

Commands:

```text
docker compose config --quiet
make demo-reset
make demo-smoke
make demo-down
docker image inspect flixr-demo-flixr:latest
docker run --rm --entrypoint sh flixr-demo-flixr:latest -c 'id; command -v node; command -v npm; command -v go; command -v gcc; command -v ffmpeg; command -v ffprobe'
```

Results:

- Compose configuration was valid.
- The multi-stage image built the React application and the static `CGO_ENABLED=0` Go executable.
- The service became healthy through the published port at `http://127.0.0.1:8787`.
- The runtime process ran as `uid=100(flixr) gid=101(flixr)`.
- `ffmpeg` and `ffprobe` were present under `/usr/bin`.
- Node, npm, Go, and GCC were absent from the runtime image.
- `docker image inspect --format '{{.Size}}'` reported 56,197,551 bytes.
- `make demo-down` removed the container and network but retained the named `flixr-demo-data` volume.

## Interface and browser evidence

The mocked viewer matrix passed in Chromium, Firefox, and WebKit. It covers Home rows, Movies and TV grids, independent preferences, My List, demo details, route restoration, accessibility, overflow, and console errors.

Named evidence paths are generated and intentionally ignored by Git:

- `frontend/test-results/mock-api-mocked-viewer-jou-8f02c-ort-demo-detail-and-My-List-chromium/movies-grid.png`
- `frontend/test-results/mock-api-mocked-viewer-jou-8f02c-ort-demo-detail-and-My-List-chromium/tv-grid.png`
- `frontend/test-results/mock-api-mocked-viewer-jou-8f02c-ort-demo-detail-and-My-List-chromium/demo-detail.png`
- `frontend/test-results/production-built-binary-co-c284f-file-browse-and-detail-flow-chromium-production/production-home-tv.png`
- `frontend/test-results/production-built-binary-co-c284f-file-browse-and-detail-flow-chromium-production/production-home-desktop.png`
- `frontend/test-results/production-built-binary-co-c284f-file-browse-and-detail-flow-chromium-production/production-home-tablet.png`
- `frontend/test-results/production-built-binary-co-c284f-file-browse-and-detail-flow-chromium-production/production-home-phone.png`

The embedded-production Chromium flow passed against the real `flixr` executable and generated media fixtures. It completed setup, scanning, profile selection, responsive Home checks, direct play, transcode, remux, seek, detail reload/history, search, axe checks, horizontal-overflow checks, and console-error review.

## Residual limitations

- Demo artwork is deterministic tonal artwork, not external provider artwork.
- Demo titles are intentionally non-playable because no media files ship with the fixture.
- Container startup smoke remains a required local check. CI validates the Compose configuration and image build but does not run the timed service workflow.
- Vite reports the known large lazy `hls.js` chunk warning. The bundle gate confirms that Player and `hls.js` stay outside the initial entry chunk.
- The existing port `18789` preview was not changed. Refreshing or resetting it still requires separate explicit approval.
