# Playback startup and seek timing — 2026-09-09

This is a bounded local qualification run for issue #112. It compares the published
v0.12.4 baseline with the latest published 0.x release available at measurement time,
v0.14.0. It does not qualify physical input, background/resume, HDR, or A/V sync, and
it is not evidence that #112 is complete.

## Result

All v0.14.0 direct, remux, and transcode seeks presented a frame in all three cold and
three warm observations. The v0.12.4 baseline presented 15 of 18: one warm remux and
two warm transcode observations returned HTTP activity but did not present the sought
frame within 30 seconds. This is why the seek result below is based on a presented
video frame rather than the seek response alone.

Startup was in the same broad range between releases. Server plan generation dominated
compatibility startup: median first frames were about 3.6–4.0 seconds for remux and
5.0–5.4 seconds for transcode. Direct first frames ranged from 38 to 499 ms. Three
observations are enough to expose failures and large variance, but not to claim a
small release-to-release performance change.

Each cell is the median in milliseconds followed by the observed range. Seek medians
exclude timeouts. Direct playback has no seek HTTP request.

| Version | Path | Cache | Plan HTTP | Media HTTP | First frame | Seek HTTP | Seek frame | Seek pass |
| --- | --- | --- | ---: | ---: | ---: | ---: | ---: | ---: |
| v0.12.4 | direct | cold | 25 (22–326) | 30 (25–330) | 189 (189–467) | n/a | 79 (78–104) | 3/3 |
| v0.12.4 | direct | warm | 325 (324–328) | 330 (328–334) | 353 (352–353) | n/a | 109 (109–112) | 3/3 |
| v0.12.4 | remux | cold | 3805 (3803–3810) | 3838 (3836–3846) | 4000 (3986–4018) | 3366 (3363–3388) | 3566 (3564–3599) | 3/3 |
| v0.12.4 | remux | warm | 3508 (3506–3809) | 3543 (3540–3844) | 3569 (3569–3869) | 525 (8–530) | 572 (562–582) | 2/3 |
| v0.12.4 | transcode | cold | 5199 (4915–5204) | 5232 (4949–5233) | 5416 (5121–5417) | 5020 (4988–5031) | 5063 (5047–5099) | 3/3 |
| v0.12.4 | transcode | warm | 4929 (4902–5193) | 4963 (4936–5226) | 5004 (4972–5271) | 5 (4–130) | 183 (183–183) | 1/3 |
| v0.14.0 | direct | cold | 329 (27–331) | 334 (31–336) | 484 (172–499) | n/a | 92 (80–110) | 3/3 |
| v0.14.0 | direct | warm | 26 (25–326) | 31 (28–331) | 43 (38–351) | n/a | 104 (92–114) | 3/3 |
| v0.14.0 | remux | cold | 3801 (3501–3818) | 3825 (3530–3848) | 3983 (3691–4016) | 3397 (1905–3405) | 3563 (1936–3583) | 3/3 |
| v0.14.0 | remux | warm | 3799 (3503–3805) | 3832 (3538–3840) | 3867 (3570–3869) | 502 (25–3529) | 564 (64–3711) | 3/3 |
| v0.14.0 | transcode | cold | 5232 (5209–5238) | 5266 (5236–5272) | 5433 (5420–5449) | 5007 (4999–5021) | 5045 (5042–5071) | 3/3 |
| v0.14.0 | transcode | warm | 4904 (4902–5190) | 4934 (4932–5220) | 4972 (4971–5269) | 5138 (126–5167) | 5199 (172–5211) | 3/3 |

The streamed seek results are bimodal. A target already inside the live segment window
can present in tens or hundreds of milliseconds; an out-of-window target creates a new
generation and took roughly 1.9–5.2 seconds here. The raw observations, including the
three v0.12.4 timeouts and console errors, are in
[`playback-timing-2026-09-09.csv`](./playback-timing-2026-09-09.csv).

## Measurement contract

- **Cold:** first playback after restarting the isolated FlixR container, with a new
  Chrome process and browser context. The persisted test catalog remained in place;
  profile creation and navigation completed before the click timer started. The cold
  restart reclaimed prior playback generations; the final stopped cache directories
  contained no generation files.
- **Warm:** second playback on the same running FlixR process and in the same Chrome
  context, using a new profile so no saved position affected startup. A fresh page was
  used while the browser HTTP cache and application process stayed warm.
- The operating-system file cache was not dropped. These labels describe application
  and browser cache state, not cold-storage performance.
- Every path used three cold/warm pairs on the same host. Each numbered repetition ran
  direct, remux, then transcode, and a container restart preceded every cold observation.
- Startup begins immediately before the real detail-dialog Play button dispatch. The
  plan timestamp is the fulfilled `POST /api/v1/playback/plans`; media HTTP is the first
  playback manifest/media response; first frame is a `requestVideoFrameCallback`
  timestamp from the video element.
- Seek begins immediately before the real **Forward 10 seconds** button dispatch. The
  streamed HTTP timestamp is the matching `POST .../seek` response. Completion requires
  the next presented frame after direct `seeked`, or after the replacement streamed
  plan is attached. The displayed source-time slider was about 10 seconds in successful
  observations. The timeout was 30 seconds.
- Timing uses the browser's monotonic `performance.now()` clock. HTTP response timing
  uses same-origin Resource Timing where available and the Playwright response event for
  native direct-media responses that remain open during the observation.

## Environment

- Host: Apple M4 Pro, 12 physical/logical cores, 24 GiB RAM, arm64; macOS 26.6.2
  (25G83).
- Container runtime: Docker Desktop 4.52.0, engine 29.0.1; Linux arm64 VM with 12 vCPU
  and 4,108,660,736 bytes assigned memory.
- Browser: headless Google Chrome 152.0.7977.83 at 1440 × 900, driven by the repository's
  Playwright 1.62.1. The in-app Browser runtime reported no available browser, so the
  production Playwright path was used.
- Container media tools: FFmpeg/ffprobe 8.1.2 in both images.
- Baseline image: `ghcr.io/mopeyjellyfish/flixr:v0.12.4`, revision
  `1337348b5cc67a38a587a3f40d3077f092dcec7b`, arm64 digest
  `sha256:90d889ca0bd9d893a7df274024721d5a8b06da74c72152a0375638d948be8432`.
- Current image: `ghcr.io/mopeyjellyfish/flixr:v0.14.0`, revision
  `966de7dfeab369811eae3a07203b5fe4bcf37167`, arm64 digest
  `sha256:5a39830af00c49d74ebc87cdb9fcacbd83597068545edab6436ec9adee0a51ac`.
- Both containers used isolated writable config/cache directories, a read-only synthetic
  media bind, loopback port 18789, disabled remote metadata, a read-only root filesystem,
  dropped capabilities, and `no-new-privileges`. No household configuration or media was
  read or changed.

## Fixtures

The committed two-second production fixtures prove real codec paths but are too short
for an actual ten-second seek. These 60-second fixtures extend the same generator inputs
(`testsrc2` video plus `sine` audio), dimensions, and codec/container decisions:

| Path | Encoding | Bytes | SHA-256 |
| --- | --- | ---: | --- |
| direct | MP4, H.264/yuv420p + AAC, 640 × 360 at 24 fps, 60 s | 5,828,102 | `26dca473bf92327cce861d0bf24682264c14751923da5b2922acd07ed711a97d` |
| remux | Matroska, H.264/yuv420p + AAC, 640 × 360 at 24 fps, 60.021 s | 5,818,753 | `c13bed52dcd20966055c5aa4d50b58a6d81b03fc7256aa46fe2d672bce5f4c1f` |
| transcode | AVI, MPEG-4 Part 2 + MP3, 640 × 360 at 24 fps, 60.042 s | 12,308,024 | `c310feab0d55eb9e1979ab0f983e1d8073f45d99a5c2907efab35edbac578ed2` |

The MP4 and Matroska sources used two-second GOPs (`-g 48 -keyint_min 48
-sc_threshold 0`); the AVI used `-g 48`. The plan response confirmed `direct`, `remux`,
or `transcode` on every observation before its timing was accepted.

## Residual observations

The v0.12.4 failed warm seeks logged a 503 media-resource response and never produced a
qualifying frame. Several remux observations on both releases also logged a 403 for a
delayed chapter request after the seek replaced its session. That chapter race did not
block playback or any successful seek, but it remains console noise worth tracking.

This desktop, synthetic-SDR result does not establish physical remote focus behavior,
background restoration, HDR/Dolby Vision correctness, display cadence, or audible A/V
synchronization. Those portions of #112 still require their own device evidence.
