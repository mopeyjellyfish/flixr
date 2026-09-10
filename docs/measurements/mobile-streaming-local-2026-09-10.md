# Mobile streaming throttle measurement — 2026-09-10

This measurement reproduces issue #207 with a generated 100-second 1280×720 MPEG-4/AAC film in isolated Flixr containers. Chromium used a 390×844 viewport, 100 ms latency, a 2 Mbps download ceiling, and a temporary 1 Mbps dip from observation seconds 20 through 35. No private media titles, production data, or credentials are present in the result.

| Build | Initial rendition | Adaptive rendition | Startup | Decoded advancement in 70 s | Stalled 1 s samples |
| --- | --- | --- | ---: | ---: | ---: |
| v0.19.0 baseline | 1280×720, 5 Mbps video, 128 kbps audio | none | 12.141 s | 23.877 s | 41 / 69 |
| issue #207 candidate | 640×360, 500 kbps video, 64 kbps audio | 852×480 at 2 Mbps; 640×360 during the dip | 6.435 s | 66.558 s | 2 / 69 |

The candidate starts Auto at a transport-safe rendition, then uses measured
fragment throughput to increase quality. At 2 Mbps it reached the 1 Mbps video
rendition at source position 13.798 seconds, then returned to the 500 kbps video
rendition during the 1 Mbps dip. This is a safe rendition restart with position
and play intent restoration, rather than seamless multi-rendition ABR.

The browser result passed the local threshold of at least 55 decoded seconds and at most 10 stalled samples. The baseline failed both thresholds. The candidate also passed the real FFmpeg rendition/seek integration suite. Physical mobile-data and VPN qualification remains separate because this run models bandwidth and latency on localhost.

A second run held the link at 0.8 Mbps for the full test. It started in 14.747
seconds from a cold route load, decoded 69.246 of 70 observed seconds, and had no
stalled samples. Auto remained at the 630 kbps declared transport bandwidth.
Cold-route startup includes the application assets as well as media transfer;
play-click timing is recorded separately when comparing a warm application.

Reproduce the candidate measurement with the repository-owned harness. It builds
an isolated image, generates the synthetic fixture, assigns an ephemeral local
port, and removes its container, volumes, image and fixture directory on exit:

```sh
bash scripts/mobile-streaming-acceptance.sh
cd backend && go test -tags=media_integration ./playback ./web
```

Set both ceilings to the same value for a sustained constrained-link check. For
example, this keeps the full run at 0.8 Mbps and verifies that Auto can reach
its 360p transport-safe floor:

```sh
FLIXR_TEST_NETWORK_MBPS=0.8 FLIXR_TEST_DIP_MBPS=0.8 \
  bash scripts/mobile-streaming-acceptance.sh
```

The JSON result records the exact Git revision or working-tree content hash and
Docker image digest used by the run.

The harness can also make Auto policy transitions part of its pass condition.
The following generated-real-media run requires an optimistic 720p start and a
downshift to 480p or below during the constrained interval:

```sh
FLIXR_TEST_NETWORK_MBPS=8 FLIXR_TEST_DIP_MBPS=0.8 \
  FLIXR_TEST_DIP_START=15 FLIXR_TEST_DIP_END=50 \
  FLIXR_TEST_EXPECT_INITIAL_HEIGHT=720 FLIXR_TEST_EXPECT_MIN_HEIGHT=480 \
  bash scripts/mobile-streaming-acceptance.sh
```

`FLIXR_TEST_EXPECT_RECOVERY_HEIGHT` optionally requires a later rendition at or
above the given height. Set `FLIXR_TEST_OBSERVATION_SECONDS` to extend the run
when using that assertion; Auto deliberately requires sustained recovery
evidence after a downgrade.

For a constrained cold start, keep the full run at 0.8 Mbps and set
`FLIXR_TEST_EXPECT_MAX_STARTUP_MS` to the local comparison budget. The assertion
uses route navigation through the first advancing frame, so it includes page
assets, server rendition preparation and media transfer rather than isolating
network throughput alone.
