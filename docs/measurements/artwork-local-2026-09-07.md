# Local artwork measurement — 2026-09-07

This is local evidence for issue #56, not reference-host qualification.

- Host: macOS 26.6.1, Apple M4 Pro, 24 GiB RAM.
- Source for the decode harness run: `origin/main` revision `baa9c934da728e4b9f10729d75c523623d51f51c`.
- Server: an isolated `FLIXR_DEMO=true` binary on loopback, with a temporary data directory populated by a copy of the repository's local `.demo` cache. No user service, volume, or media directory was used.
Chromium against the same isolated server selected Alex and used a 1920 × 2160
viewport so the initial grid mounted 30 cards. The timing observer calls
`HTMLImageElement.decode()` on actual artwork responses, de-duplicates each
resolved image URL, and records only completed decodes.

- Initial browser-run first visible decoded image: 368.1 ms.
- Initial browser-run visible 30-image grid decoded: 597.6 ms.
- Repeat browser-run first visible decoded image: 405.0 ms.
- Repeat browser-run visible 30-image grid decoded: 465.6 ms.

These are local Chromium decode completions, not device first paint. The
isolated demo copy already had derivative cache state, so “initial” is not a
fresh-derivative cold-cache baseline. They are not reference-host qualification.

The concurrent case used a different, normal-mode loopback server with a
temporary data directory and a copied real fixture corpus. It played the real
600-second H.264/AAC `Long Duration Seek 2026.mkv` fixture through an active
fMP4-HLS remux session while an owner scan re-probed 303 files (the fixture
films plus 300 temporary hard-linked copies). It did not request an artwork
grid during this run. `ps` sampled the server and its children during the
8-second scan; the result was `scanned=303`, `failed=0`.

- Flixr server peak: 9.2% CPU, 27,504 KiB RSS.
- Active FFmpeg remux peak: 0.3% CPU, 18,816 KiB RSS.
- Server + active FFmpeg peak: 9.4% CPU, 46,320 KiB RSS.
- Including observed short-lived FFprobe children: 22.2% CPU, 74,224 KiB RSS.

CPU is the point-in-time `%CPU` reported by macOS `ps`; RSS is KiB. The latter
aggregate includes only processes observed at a sample, so it is an evidence
measurement rather than a machine-wide resource ceiling.

Reproduce browser decode timing from a disposable checkout with a populated
local `.demo` cache:

```sh
measure_dir="$(mktemp -d)"
cp -R .demo "$measure_dir/demo"
FLIXR_DEMO=true FLIXR_DATA_DIR="$measure_dir" FLIXR_LISTEN_ADDR=127.0.0.1:18989 ./flixr
```

Then run the checked-in harness against that isolated server:

```sh
FLIXR_MEASURE_URL=http://127.0.0.1:18989 node frontend/scripts/measure-artwork-local.mjs
```

The harness uses no API or artwork mock. It requires the frontend's existing
Playwright dependency and an isolated demo containing Alex. For the concurrent
case, use a separate normal-mode server with `FLIXR_DATA_DIR` and
`FLIXR_FILMS_ROOT` under a temporary directory; never point it at a live demo
or media root. The demo records remain deliberately non-playable. Remaining
issue #56 acceptance evidence is a freshly populated artwork-cache baseline
and a run that combines real playback, scan, and browser artwork-grid work.
