# Local artwork measurement — 2026-09-07

This is local evidence for issue #56, not reference-host qualification.

- Host: macOS 26.6.1, Apple M4 Pro, 24 GiB RAM.
- Server: an isolated `FLIXR_DEMO=true` binary on loopback, with a temporary data directory populated by a copy of the repository's local `.demo` cache. No user service, volume, or media directory was used.
- Workload: select the preseeded Alex profile, fetch 30 distinct local cached poster URLs at `w=240`, then repeat the same URLs. Requests were sequential HTTP requests, so this measures server artwork handling rather than browser paint.
- Cold 30: 1.636 s.
- Warm 30: 0.405 s.
- Server RSS: 161,824 KiB before requests and 171,792 KiB after them.

Chromium against the same isolated server selected Alex through the rendered
profile page and reached its first rendered artwork card in 2,073 ms and the
30th rendered card in 2,110 ms. These are DOM visibility times, not image
`decode()` completion or a device-first-paint metric.

Reproduce from a disposable checkout with a populated local `.demo` cache:

```sh
FLIXR_DEMO=true FLIXR_DATA_DIR="$(mktemp -d)" FLIXR_LISTEN_ADDR=127.0.0.1:18989 ./flixr
```

Copy the cache into `$FLIXR_DATA_DIR/demo`, select a profile through `/api/v1/profiles/{id}/select`, then request the first 30 poster URLs from `/api/v1/catalog/home?limit=30` twice with the session cookie.

Browser first-visible/full-grid timing, browser CPU/peak-memory, and concurrent real playback plus scan remain unmeasured. The real-fixture playback/scanning case requires a separate normal-mode isolated server because demo records are deliberately non-playable.
