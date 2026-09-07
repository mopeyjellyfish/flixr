# Local artwork measurement — 2026-09-07

This is reproducible local evidence for issue #56. It does **not** qualify the
issue's required 4-core, 8 GiB reference host.

## Baseline and isolation

- Source: exact `origin/main` release baseline
  `127cc67dcc56e99cc205a44ff6ebda153c7b5201`.
- Host: macOS 26.6.1, Apple M4 Pro, 24 GiB RAM.
- The reference 4-core, 8 GiB host was unavailable. These results must not be
  used as its qualification or as a cross-host performance claim.
- The checked-in harness copied the local demo cache into a `mktemp` data
  directory, imported it once, and then ran the server in normal mode. It did
  not write to the cache source, a user demo, a service, a Docker volume, or a
  media root.
- Before the timed browser visit, it deleted only
  `<mktemp>/data/artwork/derivatives`. Cached originals remained in the
  temporary data directory, which is the intended fresh-derivative baseline.
  The cold visit created 26 JPEG derivatives.

## Combined cold and warm result

The normal-mode server first scanned the real
`Long Duration Seek 2026.mkv` fixture. It then started a real fMP4-HLS remux
of that fixture, added 300 isolated fixture copies, and started a second
two-worker owner scan. Chromium selected the copied Alex profile and visited
the film grid at 1920 × 2160 while that scan was active. The scan completed
with `scanned=301`, `failed=0`, and `unmatched=0`.

The observer calls `HTMLImageElement.decode()` for actual authenticated artwork
responses. It counts a URL only after its image both decodes and intersects the
viewport; the grid had 45 visible decoded artwork images. It records the first
visible decode and the 30th distinct visible decode.

| Visit | First visible decoded | 30 visible decoded | Visible decoded / visible artwork |
| --- | ---: | ---: | ---: |
| Cold derivative cache | 375.0 ms | 471.5 ms | 45 / 45 |
| Warm repeat | 348.1 ms | 469.8 ms | 45 / 45 |

The warm repeat reloaded after the cold visit on the same isolated server. Both
timings are Chromium decode completions, not device first-paint measurements.

`ps` sampled the Flixr server and its immediate FFmpeg/FFprobe children every
100 ms from scan start until scan completion. Its point-in-time combined peak
was **64.3% CPU** and **136,512 KiB RSS**. This is an observed process-set peak,
not a machine-wide limit or a reference-host resource ceiling. The browser run
started at `2026-09-07T16:14:05Z` and finished at `2026-09-07T16:14:09Z`; the
concurrent scan finished at `2026-09-07T16:14:16Z`.

At the completion of each visit, the harness queried the owner-only scan and
playback status endpoints. Both checks recorded `scan.status=running` and the
same running `remux` generation for the real fixture. The emitted
`concurrency-cold.json` and `concurrency-warm.json` make that overlap explicit.

## Reproduce

Run from this checkout after the existing frontend dependencies and Playwright
Chromium are available. The cache input is copied read-only; pass the path to a
local cache that the operator is authorized to read.

```sh
FLIXR_MEASURE_DEMO_SOURCE=/absolute/path/to/local-demo-cache \
  bash scripts/measure-artwork-acceptance.sh
```

The script archives `origin/main`, runs `npm ci` and `npm run build:embed` in
that archive, and builds its server binary there. It copies the supplied demo
cache and the repository's committed media fixture into a temporary directory,
then removes the temporary directory on exit. It writes JSON timing and scan
results, phase timestamps, and the raw `ps` samples under ignored
`frontend/test-results/artwork-acceptance-*`.

It refuses an occupied measurement port before beginning work and checks that
the server process it started remains alive before accepting readiness. Each
browser visit is separately asserted against an active owner scan and an active
fixture remux.

Its useful controls are `FLIXR_MEASURE_BASELINE` (defaults to `origin/main`),
`FLIXR_MEASURE_PORT`, and `FLIXR_MEASURE_COPY_COUNT` (defaults to 300). No API
or artwork mock is used. The measured source SHA, fixture path, derivative
count, scan result, timing data, and resource peak are all emitted in
`summary.json`.
