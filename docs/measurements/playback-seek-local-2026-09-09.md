# Local playback seek measurement — 2026-09-09

This records reproducible local evidence for issue #196. It does not replace
qualification on the household's real media or a resource-constrained reference
host.

## Baseline and isolation

- Baseline: `f2e4b1610c632d7ad6d01efee1021ad63a94182b` (`main`).
- Candidate code: `f870a846babd898687fdd1d29cf9e9db59882ce6`.
- Host: macOS 26.6.2, Apple M4 Pro (12 cores), arm64, 24 GiB RAM,
  FFmpeg 8.0.1.
- The test creates a fresh temporary HEVC Main 10 and AAC Matroska fixture,
  serves it over loopback HTTP, starts a nonzero compatibility transcode at six
  seconds, and decodes the first two fMP4 media fragments with FFmpeg. The first
  fragment must decode to the requested yellow interval and the next to cyan.
- Each result below used a fresh playback generation. Filesystem and operating
  system caches were not purged. No household media, production configuration,
  or persistent data directory was read or written.

The exact same candidate test file was copied into a detached baseline checkout
before the baseline run. This lets the checked-in actual-frame assertion measure
both command implementations against the same generated fixture.

| Build | First segment | First decoded target frame | Continuing decoded frame |
| --- | ---: | ---: | ---: |
| Baseline | 6.903 s | 6.966 s | Test stopped at the 2 s regression assertion |
| Candidate, run 1 | 1.384 s | 1.445 s | 1.967 s |
| Candidate, run 2 | 1.406 s | 1.451 s | 1.932 s |

The candidate test also waited beyond the finite startup allowance. Segments
seven through nine represent four seconds of media and advanced in 4.002 seconds
in both recorded runs, demonstrating the return to the primary 1x input rate.

An independent browser trace of the current v0.15.0 household release measured
6.706 seconds for the out-of-window seek request and 6.895 seconds until the
requested frame was ready. That trace used real HEVC Main 10 media and agrees
with the isolated baseline, but it is not a candidate qualification run.

## Remux geometry and decoded output

The companion integration test generates a 20-second H.264 High/AAC MP4 with a
four-second GOP and starts a nonzero remux at eight seconds. It verifies the
original four-second segment duration and 15-entry, 60-second playlist geometry,
then decodes the requested cyan fragment and the following magenta fragment.

| Path | First decoded target frame | Continuing decoded frame |
| --- | ---: | ---: |
| Candidate remux | 1.119 s | 4.094 s |

Direct files and retained HLS generations do not create a new FFmpeg process and
their code paths are unchanged. The existing household trace measured a retained
seek request at 46 ms, but those paths were not rerun against the candidate
because this worktree was intentionally kept away from the household service.

## Resource boundary

The acceleration is limited to four unpaced media seconds per input. FFmpeg 8
then limits post-stall catch-up to 4x and returns to the primary 1x rate. The
manager's existing default maximum of two generations therefore permits at most
eight media seconds inside the combined unpaced allowances. Existing generation
count, byte accounting, replacement, cancellation, lease, and shared-generation
tests continue to pass.

A 100 ms `ps` sampler around the second candidate transcode run observed a peak
of **126.4% CPU** and **176,688 KiB RSS** for the FFmpeg process. This is an
observed single-generation peak on this host, not an enforced CPU or memory
ceiling. Playlist retention remains 60 seconds: transcodes use 30 two-second
entries, while remuxes keep 15 four-second entries so long-GOP sources do not
expand the retained window.

## Reproduce

Run the candidate checks from `backend/`:

```sh
FLIXR_REQUIRE_MEDIA_INTEGRATION=1 go test -tags=media_integration ./playback \
  -run 'TestRealFFmpegNonzero(Main10Transcode|Remux)' -v -count=1
```

To reproduce the baseline with the identical harness, create a detached baseline
worktree, replace only its media-integration test with the candidate version, and
run the transcode test. The failure is the expected baseline result:

```sh
git worktree add --detach /tmp/flixr-issue196-baseline \
  f2e4b1610c632d7ad6d01efee1021ad63a94182b
git show f870a846babd898687fdd1d29cf9e9db59882ce6:backend/playback/ffmpeg_media_integration_test.go \
  > /tmp/flixr-issue196-baseline/backend/playback/ffmpeg_media_integration_test.go
cd /tmp/flixr-issue196-baseline/backend
FLIXR_REQUIRE_MEDIA_INTEGRATION=1 go test -tags=media_integration ./playback \
  -run TestRealFFmpegNonzeroMain10TranscodeSeekPresentsFramesWithinTwoSeconds \
  -v -count=1
```

The automated suite was also run with `go test ./...`, `go test -race ./...`,
`go vet ./...`, `go build ./...`, and the required full media-integration suite.
