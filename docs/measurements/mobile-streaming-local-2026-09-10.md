# Mobile streaming throttle measurement — 2026-09-10

This measurement reproduces issue #207 with a generated 100-second 1280×720 MPEG-4/AAC film in isolated Flixr containers. Chromium used a 390×844 viewport, 100 ms latency, a 2 Mbps download ceiling, and a temporary 1 Mbps dip from observation seconds 20 through 35. No private media titles, production data, or credentials are present in the result.

| Build | Initial rendition | Adaptive rendition | Startup | Decoded advancement in 70 s | Stalled 1 s samples |
| --- | --- | --- | ---: | ---: | ---: |
| v0.19.0 baseline | 1280×720, 5 Mbps video, 128 kbps audio | none | 12.141 s | 23.877 s | 41 / 69 |
| issue #207 candidate | 1280×720, 2.5 Mbps video, 128 kbps audio | 852×480, 1 Mbps video, 96 kbps audio | 11.893 s | 66.844 s | 2 / 69 |

The candidate observed two media stalls within 15 seconds and prepared a capped Data saver generation at source position 1.925 seconds. Playback then stayed ahead by roughly 6–10 seconds through the simulated dip. This is a safe rendition restart with position and play intent restoration, rather than seamless multi-rendition ABR.

The browser result passed the local threshold of at least 55 decoded seconds and at most 10 stalled samples. The baseline failed both thresholds. The candidate also passed the real FFmpeg rendition/seek integration suite. Physical mobile-data and VPN qualification remains separate because this run models bandwidth and latency on localhost.

Reproduce the candidate measurement with the repository-owned harness. It builds
an isolated image, generates the synthetic fixture, assigns an ephemeral local
port, and removes its container, volumes, image and fixture directory on exit:

```sh
bash scripts/mobile-streaming-acceptance.sh
cd backend && go test -tags=media_integration ./playback ./web
```

The JSON result records the exact Git revision or working-tree content hash and
Docker image digest used by the run.
