# Configuration

Flixr resolves a setting in this order: explicit environment value, persisted owner setting, then the listed default. `*_FILE` is supported only for the two secret values, and cannot be combined with its direct environment variable. Environment-managed settings are read-only in the owner UI.

| Setting | Default | Scope / persistence | Environment | Restart |
| --- | --- | --- | --- | --- |
| Data directory | `./flixr-data` | server / environment | `FLIXR_DATA_DIR` | yes |
| Listen address | `127.0.0.1:8787` | server / environment | `FLIXR_LISTEN_ADDR` | yes |
| TLS certificate and key | empty | server / environment | `FLIXR_TLS_CERT`, `FLIXR_TLS_KEY` | yes |
| Demo | `false` | server / environment | `FLIXR_DEMO` | yes |
| Owner password | unset | server / startup only | `FLIXR_OWNER_PASSWORD`, `_FILE` | yes |
| Initial profile | unset | server / startup only | `FLIXR_INITIAL_PROFILE` | yes |
| Film and TV roots | empty | server / database | `FLIXR_FILMS_ROOT`, `FLIXR_TV_ROOT` | no |
| TMDB token | unset | server / database | `FLIXR_TMDB_TOKEN`, `_FILE` | no |
| Scan on start, workers | `false`, `4` | server / environment | `FLIXR_SCAN_ON_START`, `FLIXR_SCAN_WORKERS` | yes |
| Segment directory | `<data-dir>/segments` | server / sidecar and database | `FLIXR_SEGMENT_DIR` | yes |
| Generation/global bytes, generation count | 256 MiB, 512 MiB, 2 | server / database | `FLIXR_GENERATION_BYTES`, `FLIXR_GLOBAL_BYTES`, `FLIXR_MAX_GENERATIONS` | no |
| Lease TTL, heartbeat, segment window, process grace | 45s, 15s, 60s, 2s | server / environment | `FLIXR_LEASE_TTL`, `FLIXR_HEARTBEAT_INTERVAL`, `FLIXR_SEGMENT_WINDOW`, `FLIXR_PROCESS_GRACE` | yes |

The owner Configuration panel provides a searchable Basic view, an Advanced view, portable non-secret export, and import preview. Imports use the same validated library or playback setter as the owner form and accept exactly one scope per request, so each applied change is atomic at that scope. Mixed scopes, network and access settings require explicit review. Exports omit all secret values; previews reject environment-managed values.

## Metadata edits and refresh

In Server settings → Metadata, expand **Edit metadata** for a film or series. Edit its title, synopsis, year, cached artwork references, tags, or local content rating, select the fields to lock, and choose **Save metadata**. Locked values survive rescans and provider refresh. Tags and content ratings remain local and are never replaced by a provider refresh.

To refresh a locked field, clear **Lock this field** and save first. **Preview provider refresh** shows the proposed values and their source; **Refresh unlocked fields** applies that provider snapshot while checking the current locks again. Previews last one minute, and the server retains the four most recent title previews. After expiry, eviction, or a restart, refresh fetches a new snapshot. Matching or unmatching a title invalidates older snapshots of its identity.

Metadata and refreshed artwork references commit together. Provider, artwork-cache, cancellation, and database failures leave the previous committed values and artwork intact. Cached images remain local; failed refresh objects are removed, and bounded background maintenance collects objects abandoned by an interrupted process.
