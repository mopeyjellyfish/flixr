# Container release and regression review

This pass turns recurring home-media failure modes into concrete safeguards. The
[feedback research](2026-09-06-tv-movie-feedback.md) is a sample of the preceding six
months, not an exhaustive issue census or a claim of Plex/Jellyfin feature parity.

| Failure mode | Flixr safeguard and runnable evidence |
|---|---|
| WAN outage prevents local use | Local accounts, catalog/artwork and playback; `make test-offline` runs setup, scanning and real playback with no server default route, then checks restart persistence. |
| Missing mount erases a library | Directory-walk errors abort catalog replacement; `TestUnavailableRootCannotEraseCatalog` preserves the prior IDs after a root disappears. A mounted empty share remains an operator precondition. |
| Wrong remake or truncated episode number | Exact normalized title/year matching leaves ambiguous results unmatched; catalog tests cover remakes and episode numbers above 99. |
| Large library overwhelms browser or repeated scans | Virtualized DOM tests, 10,000-item database budget and unchanged-scan no-reprobe regression checks. These do not certify a 100,000-title library or every NAS. |
| Failed progress request leaves playback running | Player stop follows both successful and failed final heartbeat; browser tests cover expiry, interruption and recovery states. |
| Startup loses keyboard focus | Player focus waits until the loading overlay is gone; three-browser acceptance checks initial focus and keyboard navigation. |
| Container recreation loses accounts or resets credentials | `/config` persists separately from disposable `/cache`; `scripts/compose-smoke.sh` exercises env_file, file secrets, initial profile, playback limits and owner persistence after recreation. |
| Tiny image lacks required playback capabilities | `scripts/image-smoke.sh` verifies non-root runtime, FFmpeg H.264 encode/ffprobe, UI/readiness, writable volumes on a read-only root and graceful shutdown. Release runs it on AMD64 and ARM64. |
| Untested merge publishes a broken release | Main-only publication depends on application, browser, media, Docker, vulnerability and release-policy gates. Versioned images pass platform smoke before Git tag/release and moving aliases. |

Subtitle/audio selection, alternate episode orders, NFO/field locks, deterministic
next-episode behavior, HDR/device certification and native TV clients remain work.
They are documented product gaps, not problems solved by a metadata-only demo.

## Security pass

A single-pass source audit reviewed primary authentication, profile/screen authority,
filesystem confinement, metadata ingress, subprocess, configuration and release
boundaries. It did not audit every vendored component or certify deployment hosts.
The formal scan records the initial working-tree snapshot; hardening edits made
during the pass were separately checked. No authentication bypass, path escape or
command injection was demonstrated in the reviewed paths.

The remaining source finding is conditional cleartext LAN HTTP exposure. Published
Compose binds loopback by default; `compose.https.yml` supplies direct TLS for LAN
access, and a regression test checks Secure/HttpOnly/SameSite cookies. Explicit HTTP
LAN deployment still exposes credentials to an on-path observer.

Go was upgraded to **1.27.1**, `golang.org/x/crypto` to **v0.56.0**, and the pinned
Alpine 3.24.1 runtime upgrades its distribution packages during build. The native
image scan on 6 September reported no Alpine vulnerabilities and one module-level
`GO-2026-5932` advisory. `govulncheck` reported zero affected imported packages or
reachable vulnerabilities; Flixr uses bcrypt and does not import OpenPGP. See the
[upstream module-wide advisory discussion](https://github.com/golang/go/issues/80347).
The advisory remains visible rather than globally suppressed.

CI checks reachable Go vulnerabilities, production npm dependencies and HIGH/CRITICAL
image vulnerabilities. Digests pin base images and Actions; Dependabot proposes
updates. A clean scan is point-in-time evidence, not a permanent security guarantee.
Formal scan usage reported 7,029,878 tokens, including 6,415,744 cached input tokens,
across four threads; these are tool-reported totals, not a billing estimate.

For deployment, backup, first publication and failure recovery, see
[Docker](../docker.md) and [releases](../releases.md).

## Final local checks

- Go 1.27.1: package tests, race checks, vet and required FFmpeg integration pass.
- Frontend: 57 tests, lint, typecheck and production bundle boundary pass; initial
  entry is 218,689 bytes and HLS remains lazy-loaded.
- All 30 mocked acceptance cases pass across Chromium, Firefox and WebKit.
- Internet-isolated production acceptance passes setup, scan, profile selection,
  direct/remux/transcode playback, screen control and restart persistence.
- Native release image smoke, Compose provisioning/recreation and trusted HTTPS pass.
- Live demo smoke verifies 50 films, 50 series, cached artwork and non-playability;
  the Vite client remains available for live reload.
- Local screenshots/traces are under `output/playwright/offline` and
  `frontend/test-results`; CI uploads fresh acceptance artifacts.

Actual GHCR publication, multiarchitecture registry manifests and generated GitHub
release notes still require the first successful merge to main. This branch does
not merge or publish itself.
