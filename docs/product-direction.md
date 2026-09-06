# Flixr product direction

Research checked 6 September 2026 against official product documentation and the
current Flixr source tree. This is a product comparison, not a source audit or a
performance benchmark of competitors.

| Product | Useful lesson | Flixr decision |
| --- | --- | --- |
| Netflix | Its TV redesign prioritises title information and visible shortcuts to reduce discovery friction. | Keep Home, Movies, TV, and Search visible; foreground artwork, a title, and a clear viewing action. Keep administration separate. |
| Plex | Local network authentication and server discovery have configuration and connectivity considerations. Plex documents local authentication exceptions; offline access is not categorically impossible. | Keep owner credentials, profile PINs, sessions, discovery between Flixr browsers, and playback authority on the local server. Never require an authentication bypass to survive an internet outage. |
| Jellyfin | It is free software with an active volunteer community providing development, documentation, translations, and support. It is not an unsupported project. | Publish usable setup and recovery instructions, runnable checks, and honest compatibility limits. Open source alone does not guarantee either polish or sustained maintenance. |

Sources: [Netflix TV experience](https://about.netflix.com/en/news/unveiling-our-innovative-new-tv-experience),
[Plex local authentication](https://support.plex.tv/articles/200890058-authentication-for-local-network-access/),
[Plex internet requirements](https://support.plex.tv/articles/200484903-internet-and-network-requirements/),
[About Jellyfin](https://jellyfin.org/docs/general/about/).

## The six product requirements

1. **Local first.** Local Go HTTP server, SQLite, Argon2 credentials, same-origin
   sessions, local FFmpeg, embedded web assets and fonts. TMDB is optional and
   artwork is cached on disk. Installation can download dependencies; an installed
   system must start, authenticate, browse, and play without the internet.
2. **Maintainable stack.** Retain Go, SQLite, TypeScript, React, and Vite. Avoid a
   hosted control plane, extra database services, and a rewrite without measured
   evidence. The age of Plex is not proof of a particular performance defect.
3. **Supportability.** A clear walkthrough, recoverable setup, distinct owner
   operations, readable errors, documented backups, and explicit limitations.
   Flixr cannot promise a support organisation merely by shipping software.
4. **Free and open.** MIT-licensed Flixr source; no accounts, subscriptions, or paid
   feature gates. FFmpeg and other dependencies retain their own licences.
5. **Beautiful.** Preserve the Cobalt Signal direction in `DESIGN.md`, local fonts,
   artwork-led browsing, prominent primary actions, comfortable spacing, visible
   keyboard focus, and reduced-motion support. No copied Netflix branding or
   borrowed film artwork in the repository.
6. **Fast and easy.** `make start` builds and starts an actual media installation.
   Read-only media mounts, durable server data, and three-step setup. Direct play
   avoids unnecessary conversion; lazy player/HLS bundles and virtualized media
   collections bound frontend cost. Existing checks enforce a 300 kB raw entry
   bundle and a 200 ms p95 browse budget on a 10,000-title synthetic catalog.

## This implementation

- Add production Compose startup with configurable media folder, bind address,
  and port, a durable volume, health check, and restart policy. Keep the existing
  metadata-only demo independent.
- Resume interrupted owner setup from authenticated, persisted roots. Owner login
  returns households without a profile to the walkthrough.
- Organize owner settings into Libraries, Household, Playback & screens, and
  Metadata; keep resource limits in an expandable section and expose activity
  refresh without overwriting unsaved form edits.
- Strengthen browsing composition, add a direct focal Play action only for titles
  explicitly marked playable, and show existing local artwork in details.
- Surface native video errors and flush progress when playback ends. Recover bookmarked routes after the local server becomes available again.
- Run production acceptance against an internet-isolated Docker server in CI; reuse the installed image when starting offline.

## Release boundary

The existing local browser playback and Flixr screen control paths are
implemented. Google Cast and native AirPlay in the older release plan remain
unfinished, including receiver-specific planning, expiring external URLs,
adapters, and physical-device validation. Native television/mobile applications,
multiple libraries of each type, music/photos, scheduled filesystem watching,
full track/subtitle selection, and WAN sharing are also outside the current
implementation. Do not mark those complete based on browser fakes or a demo.

The product should earn broader scope through real household usage and measured
failures. It must not claim general Plex/Netflix/Jellyfin parity or performance
superiority without evidence.

## Validation evidence

Checked against this working tree on 6 September 2026:

- Go unit, race, vet, and required FFmpeg integration checks passed. The local
  shell exported a mismatched GOROOT; checks used `env -u GOROOT` to select the
  matching installed toolchain without changing system configuration.
- 50 frontend tests passed, along with TypeScript, ESLint, the embedded production
  build, and the bundle check. Initial JavaScript: 200,018 bytes, below 300,000.
- `make start` was exercised with a fresh Compose project and no prebuilt project
  image: automatic image build, health check, and unclaimed setup status passed.
- `make test-offline` passed against the production image: first owner setup,
  four-file scan, profile selection, search, direct playback, remux, transcode,
  progress, two-browser screen control, accessibility checks at four viewports,
  and claimed-state persistence after restart. The server had no default route;
  the primary browser context rejected external-origin requests.
- Owner desktop and phone screenshots were inspected. Browser screenshots and
  traces are generated, ignored artifacts under `output/playwright/`.

The browser matrix initially exposed an occupied preview port, transient contrast
during a fade, invalid synthetic media in state-only tests, and a profile-selection
race in screen acceptance. Those were corrected. A subsequent concurrent matrix
passed 28 of 30 cases, with two intercepted-request timeouts in Firefox/WebKit;
serial rerun results are recorded below. Do not describe this as an uninterrupted
30/30 matrix pass.

Serial verification passed all four selected cases (Firefox and WebKit, desktop
and tablet), including both previously timed-out cases. Command:
`FLIXR_TEST_PORT=19880 npm --prefix frontend run test:e2e -- --project=firefox --project=webkit --grep 'mocked delivery-unit states: (desktop|tablet)' --workers=1`.
