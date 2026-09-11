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
| Initial Films and TV locations | empty | server / database | `FLIXR_FILMS_ROOT`, `FLIXR_TV_ROOT` | no |
| Remote metadata | `true` | server / database | `FLIXR_METADATA_ENABLED` | no |
| Personal TMDB override | application default when present | server / database | `FLIXR_TMDB_TOKEN`, `_FILE` | no |
| Scan on start, workers | `false`, `4` | server / environment | `FLIXR_SCAN_ON_START`, `FLIXR_SCAN_WORKERS` | yes |
| Scan schedule override | unset | server / database | `FLIXR_SCAN_SCHEDULE` | no |
| Backup folder and schedule | unset / off | server / database | `FLIXR_BACKUP_DESTINATION`, `FLIXR_BACKUP_SCHEDULE` | no |
| Backup retention | 7 copies / 30 days / 10 GiB | server / database | `FLIXR_BACKUP_RETAIN_COUNT`, `FLIXR_BACKUP_RETAIN_AGE`, `FLIXR_BACKUP_BUDGET_BYTES` | no |
| Segment directory | `<data-dir>/segments` | server / sidecar and database | `FLIXR_SEGMENT_DIR` | yes |
| Generation/global bytes, generation count | 256 MiB, 512 MiB, 2 | server / database | `FLIXR_GENERATION_BYTES`, `FLIXR_GLOBAL_BYTES`, `FLIXR_MAX_GENERATIONS` | no |
| Lease TTL, heartbeat, segment window, process grace | 45s, 15s, 60s, 2s | server / environment | `FLIXR_LEASE_TTL`, `FLIXR_HEARTBEAT_INTERVAL`, `FLIXR_SEGMENT_WINDOW`, `FLIXR_PROCESS_GRACE` | yes |

The owner Libraries panel supports any number of named film or TV libraries and
folders. The two environment roots remain compatible as read-only locations in
the default Films and TV libraries. An environment root initializes an empty
location immediately. If it differs after that location contains indexed media,
Flixr keeps the proven root active and shows the requested move or removal in
Named libraries for explicit owner confirmation. The confirmed value becomes
the saved root and matches the environment on the next restart. Adding a folder rejects the same directory,
nested directories, parent directories, and symlink aliases already covered by
another location. Moving or removing a folder first creates an exact, durable
preview of affected sources and titles; confirmation fails if a scan changed that
snapshot. Logical titles, profile progress, lists, ratings, history, and owner
metadata remain stored after removal.

## Scheduled library scans

Each named library has its own disabled-by-default interval or daily schedule in
the owner Libraries view. Interval schedules run from the next recorded due time.
Daily schedules use an IANA timezone such as `Europe/London`. During a spring
daylight-saving gap, Flixr runs at the first valid local minute after the requested
time. When a local time occurs twice in autumn, it uses the first occurrence.
After downtime, any number of missed times becomes one queued scan and the next
future due time is recorded.

The single scan runner serializes manual, startup, scheduled, and retry requests.
Another request for a library that is queued or running coalesces into its existing
job. Running jobs become **interrupted** after a restart and can be retried. Transient
per-file failures retry at one minute and then five minutes, up to three attempts;
permission and unavailable-folder failures wait for an owner fix or the next normal
schedule. Cancelling a run leaves the last committed catalog intact.

Exclusions are newline-separated relative glob patterns. `*` matches within one
path segment and `**` spans directories, for example `Extras/**` or
`**/Samples/**`. Excluded files are not opened or probed. Titles already admitted
from an excluded path remain available; exclusions do not delete catalog, history,
ratings, or list data.

`FLIXR_SCAN_SCHEDULE` applies one schedule to every library and makes schedule
timing controls read-only; owners can still save per-library exclusions. Accepted
values are `off`, `every:<duration>` (at least one
minute), and `daily:<HH:MM>@<IANA timezone>`, such as
`daily:03:30@Europe/London`. `FLIXR_SCAN_WORKERS` remains the 1–32 concurrency
bound inside the one active library scan. Each library reports its last successful
completion independently of the retained job history. FlixR embeds the IANA time
zone database so daily schedules also work in minimal containers.

The owner Configuration panel provides a searchable Basic view, an Advanced view, portable non-secret export, and import preview. Imports use the same validated library or playback setter as the owner form and accept exactly one scope per request, so each applied change is atomic at that scope. Mixed scopes, network and access settings require explicit review. Exports omit all secret values; previews reject environment-managed values.

## Verified backups and offline restore

Configure an existing absolute backup folder in **Server settings → Backups**.
The folder must be outside the data, media, and playback-cache trees. Flixr claims
an empty folder with its own marker and never rotates unrelated files. Each backup
uses SQLite's online snapshot API and includes the database plus only the original
downloaded artwork referenced by that snapshot. Media, playback segments, derived
artwork, lock files, partials, and environment secrets are excluded.

Flixr checks every entry checksum, the database integrity and foreign keys, and the
schema version before publishing the archive. Retention runs only after a new verified
archive exists and always keeps at least the newest verified copy. The owner page shows
the last verified time, next due time, failures, and interrupted or cancelled jobs.
Environment schedules accept `off`, `every:24h` (minimum one hour), or
`daily:03:00@Europe/London`.

Restore only while Flixr is stopped:

```sh
flixr restore-backup --data-dir /absolute/path/to/flixr-data \
  --archive /absolute/path/to/flixr-backup-….flixr-backup
```

The command locks the target, rejects corrupt or newer-format archives before changing
live files, stages and migrates older compatible data locally, remaps disposable playback
paths, and journals publication. Re-running the command after interruption recovers a
deterministic prior state. Replacing an existing installation first retains a separately
verified safety backup. After restore, start Flixr and confirm owner login, profiles,
history, artwork, and library availability. Media mounts and environment settings must
be supplied separately on the restored host. Maintainers can run the fully disposable
recovery exercise with `scripts/backup-recovery-drill.sh`.

## Metadata edits and refresh

In Server settings → Metadata, expand **Edit metadata** for a film or series. Edit its title, synopsis, year, cached artwork references, tags, or local content rating, select the fields to lock, and choose **Save metadata**. Locked values survive rescans and provider refresh. Tags and content ratings remain local and are never replaced by a provider refresh.

To refresh a locked field, clear **Lock this field** and save first. **Preview provider refresh** shows the proposed values and their source; **Refresh unlocked fields** applies that provider snapshot while checking the current locks again. Previews last one minute, and the server retains the four most recent title previews. After expiry, eviction, or a restart, refresh fetches a new snapshot. Matching or unmatching a title invalidates older snapshots of its identity.

Metadata and refreshed artwork references commit together. Provider, artwork-cache, cancellation, and database failures leave the previous committed values and artwork intact. Cached images remain local; failed refresh objects are removed, and bounded background maintenance collects objects abandoned by an interrupted process.

Library scans can also import a bounded offline NFO and local-artwork subset. See [Local metadata and artwork](local-metadata.md) for supported names, fields, precedence, diagnostics, and safety limits.

Official images resolve metadata access in this order: an explicit disabled setting,
an environment/file or saved owner override, then the FlixR application credential.
Removing an override returns to automatic application access. Disabling remote metadata
does not delete either credential. Source builds normally have no application credential
and report that state truthfully; a personal token remains an optional advanced override.

FlixR records only a one-way credential revision and the last successful refresh time.
A new application revision or a refresh age of 150 days schedules one normal bounded
scan of existing roots. A partial/provider-failed scan remains due. The scan preserves
catalog identity, owner field locks, local overrides, cached artwork, and viewing history.
TMDB's current API terms limit caching to six months; do not deliberately keep a server
offline past that limit while continuing to use provider-derived content. If TMDB access
is terminated, stop remote metadata and remove provider-derived cached data before reuse.
