# TV and movie feedback: evidence and delivery backlog

Research window: **6 March–6 September 2026**. Reviewed Plex forums, Jellyfin server/web issues, community reports, and official metadata documentation. This is a qualitative sample, not a prevalence study. Reports describe particular clients, releases and hardware; they do not establish that every installation is affected. Closed upstream issues remain useful regression examples, not evidence of an unresolved upstream defect. Jellyfin is actively maintained.

## What to build from the evidence

Priority means impact on a household trying to watch its own media. “Existing” describes code already present; it is not a claim of broad device certification. “This pass” identifies the changes associated with this research.

| Priority / user problem | Recent evidence | Flixr response and remaining acceptance check |
|---|---|---|
| P0: cloud outage blocks household viewing | [Plex outage discussion, 22 July](https://www.reddit.com/r/PleX/comments/1v3n84s/opening_my_reddit_app_this_morning_and_seeing/) | Existing local accounts, sessions, catalog and artwork cache. Plex has offline configuration options; avoid claiming it never works offline. Keep WAN-blocked cold-start, login, browse and playback as a release gate. |
| P0: wrong title/remake silently matched | [Jellyfin metadata changes, 9 August](https://www.reddit.com/r/jellyfin/comments/1vjwqj7/metadata_changing/) | This pass: require matching title and supplied year; ambiguous results stay unmatched. Existing metadata survives ordinary rescans. Next: explicit identify, lock fields, local NFO overrides and provenance. Acceptance: manually selected match survives rescan and provider outage. |
| P0: TV episode numbering breaks past 99 | [Jellyfin #16406, 12 March; now fixed upstream](https://github.com/jellyfin/jellyfin/issues/16406) | Found a separate Flixr parser defect: two-digit truncation. This pass supports up to four episode digits in both S02E123 and 2x123, reads the filename rather than a misleading parent directory, and tests title cleanup. Absolute anime numbering, multi-episode files and alternate orders still need explicit support. |
| P0: stale resume / wrong next episode | [Plex Fire TV feedback, 26 July](https://forums.plex.tv/t/fire-tv-plex-app-lot-of-problems-after-last-update/941041) | Existing per-profile progress and stable catalog IDs. Still needs end-to-end proof with actual videos: end episode, immediately select Continue Watching, reload, switch profile, confirm correct episode and timestamp. Add deterministic next-episode behavior before promising TV parity. |
| P0: playback fails after network interruption | [Jellyfin web #8206, 12 July; closed not planned](https://github.com/jellyfin/jellyfin-web/issues/8206) | Treat the reporter's failed HLS-segment recovery as a test candidate, not a maintainer-validated root cause. Existing playback error UI is insufficient proof. Inject segment 503s, disconnect/reconnect LAN, pause/resume and seek; preserve progress and offer bounded retry. |
| P1: slow large-library browsing | [Jellyfin #17405, 22 July; open](https://github.com/jellyfin/jellyfin/issues/17405) | Existing virtualized rails and a 10,000-item DOM-bound test. This pass preloads only the next hero image, stops rotation offscreen/in background and uses cached artwork. Still benchmark database queries, cold login and search against 100,000+ real catalog rows; a 100-title demo cannot prove this. |
| P1: scanning makes the server unusable | [Jellyfin #17192, 26 June; open](https://github.com/jellyfin/jellyfin/issues/17192) | Existing scan status UI. Next: measure browse latency and memory during a large scan, then bound work/transactions where measurements show contention. Do not add a distributed queue preemptively. |
| P1: extra navigation, lost focus, awkward rails | [Plex UI feedback, 4 May posts](https://forums.plex.tv/t/new-ui-is-an-awful-experience/931048?page=37); [Jellyfin web #8266, 23 July; closed duplicate](https://github.com/jellyfin/jellyfin-web/issues/8266) | Existing direct Movies/TV destinations, visible rail arrows, keyboard navigation and detail-close focus restoration. This pass adds controllable discovery with concise summaries. Keep remote-control navigation and older TV browser testing outstanding. |
| P1: subtitles are hard to select or out of sync | [Jellyfin community, 10 April](https://www.reddit.com/r/JellyfinCommunity/comments/1shmwff/what_jellyfin_quirks_bugs_bother_you_the_most/) | Next: visible audio/subtitle controls, per-profile language preferences, subtitle offset, external SRT/WebVTT and ASS compatibility tests. No claim of universal subtitle support from the metadata demo. |
| P1: HDR/transcode and aspect-ratio surprises | [Jellyfin #16687, 23 April; open](https://github.com/jellyfin/jellyfin/issues/16687); [Plex Fire TV feedback, 16 August](https://forums.plex.tv/t/new-plex-ui-interface/941722) | Existing video uses contain sizing. Still needs SDR/HDR/Dolby Vision, audio passthrough and device capability fixtures, plus an understandable reason when transcoding is chosen. |
| P1: stuck splash / failed startup | [Jellyfin web #8090, 22 June; closed completed](https://github.com/jellyfin/jellyfin-web/issues/8090) | Existing splash follows actual loading, bounds artwork decoding, respects reduced motion and reveals errors with retry. Preserve slow-response and failed-request checks; old iOS/TV startup remains a device test. |
| P2: Continue Watching becomes cluttered | [Plex archive/snooze request, 17 April](https://forums.plex.tv/t/feature-request-dedicated-archive-or-snooze-for-continue-watching-row/938078) | Next: hide a title without deleting progress or marking it watched; undo, profile isolation and reappearance after new viewing must be explicit. |
| P2: missing useful scan progress | [Jellyfin web #7944, 21 May; open](https://github.com/jellyfin/jellyfin-web/issues/7944) | Existing administrator scan state. Next: per-library counts and actionable unmatched/failed items; avoid fabricated percentages when total work is unknown. |

## Metadata sources and ownership

**TMDB is the primary optional online movie/TV provider**, consistent with [Infuse's documented metadata approach](https://support.firecore.com/hc/en-us/articles/215090947-Metadata-101). Search is identification, not proof: use title/year now, and explicit provider IDs and user selection next. Keep requests server-side, bounded and cached; browsing must not wait for an online lookup. TMDB image sizes should follow its [image documentation](https://developer.themoviedb.org/docs/image-basics). Provider credentials and applicable attribution/terms still matter.

**Local metadata should have final authority.** [Kodi's NFO workflow](https://kodi.wiki/view/NFO_files) and [Jellyfin's NFO priority](https://jellyfin.org/docs/general/server/metadata/nfo/) are strong precedents for portable, editable libraries. Flixr does not yet import NFO files: build this with field locks and a visible source, rather than introducing several competing automatic providers first.

**TVmaze remains useful for this credential-free TV demo.** Its [official API](https://www.tvmaze.com/api) provides show, episode and image data; preserve attribution and respect its usage conditions. It is not a film provider. [Jellyfin's provider documentation](https://jellyfin.org/docs/general/server/metadata/) also shows where optional specialty providers belong; do not silently combine conflicting episode orders.

The running demo contains **50 curated films and 50 curated TV shows**, local cached assets and no media files. It is not a live chart of the world's current top 50. Film demo assets come from the existing Wikipedia/Wikimedia preparation path; TV data comes from TVmaze. The optional TMDB importer can prepare a top-rated snapshot when configured with credentials. Featured discovery preserves catalog order, interleaves films and shows, and invents neither ratings nor popularity. Original demo teaser copy is separate from full provider descriptions.

## Changes in this pass

- Rotating featured artwork, short story summaries, previous/next and pause controls. Rotation stops during hover/focus, detail viewing, hidden tabs, offscreen browsing and reduced-motion preference. No autoplaying trailers. Only the next image is prefetched.
- Conservative movie/TV title matching with optional filename year. Wrong first search result and ambiguous remakes no longer silently win. Exact matching intentionally leaves unusual release filenames and aliases unmatched until an explicit identify flow exists.
- Episode parsing beyond 99 in the common season/episode filename formats, including protection from episode-like parent folder names.
- Regression tests for matching, episode parsing, carousel suspension/manual pause and bounded summary copy.

## Next delivery order

1. Metadata repair: explicit identify, visible source/year, local NFO import and locked overrides.
2. Trustworthy TV continuation: resume/next episode, completed state, hide-from-continue without deleting history; verify across profiles and restarts.
3. Playback resilience and language controls using real media fixtures on target browsers/devices.
4. Large-library and concurrent-scan measurement, followed by fixes to measured bottlenecks.
5. Remote navigation, compatibility matrix and household setup/recovery walkthrough.

A release should report which combinations passed, not promise that one service has already solved every problem reported against mature media servers.

## Verification recorded for this pass

- Frontend: 56 tests pass; TypeScript, ESLint, production build and bundle boundary checks pass. Initial entry remains 218,679 bytes; the larger HLS chunk stays lazy-loaded.
- Backend: `env -u GOROOT go test ./...` passes; additional TV original-name/first-air-year matching test passes in the catalog suite.
- Browser: desktop and 390px mobile inspected; manual next switches series to film; automatic rotation advances after nine seconds; reduced-motion reload holds the title and removes the autoplay control. No browser console errors in that check.
- Live demo smoke: 50 films, 50 series, cached artwork, profiles and non-playability verified after backend rebuild. Vite client remains available on port 19879 for live reload.
- Evidence screenshots: `output/playwright/featured-desktop.png`, `featured-next.png`, `featured-mobile.png` (local ignored artifacts).
- These checks do not establish real-media playback resilience, native TV compatibility or large-database performance.
