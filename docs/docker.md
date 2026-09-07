# Run Flixr with Docker Compose

The published image is `ghcr.io/mopeyjellyfish/flixr`. It contains the web interface,
Go executable, FFmpeg/ffprobe and CA certificates. No Node, Go compiler or source
checkout is needed at runtime. Images target Linux AMD64 and ARM64.

The GHCR image currently requires registry access. If pulling it returns an access
error, use the source-build setup below. Package visibility and anonymous-pull
verification are tracked in [#91](https://github.com/mopeyjellyfish/flixr/issues/91).
See the [public-release verification procedure](publication.md) for the remaining checks.

## Start

Use Docker Compose 2.24 or later (the optional `env_file` requires it).

Save `compose.release.yml`, optionally copy `flixr.env.example` to `flixr.env`, then:

```sh
FLIXR_MEDIA_DIR=/absolute/path/to/media docker compose -f compose.release.yml up -d --wait
docker compose -f compose.release.yml logs flixr
```

Open `http://localhost:8787` on the server. The published Compose default binds only to loopback. For other devices, use the HTTPS override below, or explicitly set `FLIXR_BIND_ADDR=0.0.0.0` for HTTP on a trusted LAN; HTTP exposes passwords, sessions and media to on-path observers. Logs contain the one-time setup token unless an
owner was provisioned through a secret. The web wizard is available when credentials
are omitted. Use `/media/films` and `/media/tv` for a host folder with those children.
Only one root per media kind is currently supported; several disks can be mounted
under subdirectories of that root.

Set `FLIXR_VERSION=v0.1.0` (replace with an available release) to pin a release.
`latest` follows successful releases. For reproducible deployments, use the image's
registry digest in the Compose `image` field.

## Build locally

From a source checkout, create a `.env` file with your media folder and start the
local Compose configuration:

```dotenv
FLIXR_MEDIA_DIR=/absolute/path/to/your/media
```

```sh
docker compose -f compose.local.yml up --build -d --wait
docker compose -f compose.local.yml logs flixr
```

Open http://localhost:8787 and follow the setup steps above. This builds the image
locally and needs internet access on the first build. Later starts can reuse the
image without `--build`. The local configuration enables trusted-LAN access by
default and uses a persistent `/data` volume; it is separate from the published
configuration's `/config` and `/cache` volumes. Keep using the same Compose file
and media path for subsequent commands.

For native builds, the demo and checks, see [development](development.md).

## Filesystem contract

| Container path | Contents | Mount policy |
|---|---|---|
| `/config` | SQLite database, accounts, settings, local artwork and lock files | Persistent, writable; back up this volume |
| `/cache/segments` | Bounded, disposable FFmpeg playback generations | Writable cache volume or suitably sized tmpfs; never point at media |
| `/media` | Original films and TV files | Read-only host bind mounts |
| `/run/secrets/*` | Optional password and metadata credential files | Read-only Compose secrets |
| `/tmp` | Temporary process files | Disposable tmpfs |

This separation follows [Jellyfin's container layout](https://jellyfin.org/docs/general/installation/container/).
[Plex also separates configuration, transcode scratch space and original media](https://github.com/plexinc/pms-docker).
Flixr uses bridge networking and a single TCP port; host networking is unnecessary.

The image runs as **UID 100, GID 101**, matching earlier Flixr images so existing
volumes remain readable. Named volumes inherit prepared directory ownership.
For host bind mounts, create writable config/cache directories for that UID/GID,
or set Compose `user: "1000:1000"` and grant that account access. Use `group_add`
for supplementary media-read groups. Flixr does not recursively chown your media.
Keep SQLite on storage with reliable local file locking; network media mounts are fine.

Separate mounts can replace the single `/media` mount:

```yaml
volumes:
  - ./config:/config
  - ./cache:/cache
  - /srv/movies:/media/films:ro
  - /mnt/tv:/media/tv:ro
```

Use long bind syntax with `bind: {create_host_path: false}` as in the shipped Compose
file to fail clearly when a host path is missing. A mounted but empty network share
cannot always be distinguished from an intentionally emptied library; ensure the
share is available before scanning. Unreadable or missing roots fail scans without
replacing the last catalog.

### External audio tracks

Put an external audio file beside its film or episode and start its name with the
complete video filename stem. Add a language tag and an optional label before the
audio extension. For example, `Signal S01E01.jpn.Director Commentary.m4a` belongs
to `Signal S01E01.mkv`. Flixr recognizes AAC, FLAC, M4A, MP3, Ogg, Opus and WAV
sidecars. The language tag may use a two- or three-letter code, with optional
hyphenated subtags.

The player labels embedded and external tracks, including default and commentary
metadata. Choosing a track with a known language remembers that language for the
active profile and prefers it on the next episode. Unlabeled languages remain
selectable without replacing the last known preference.

## Environment and secrets

Compose `environment` and `env_file` are the supported ways to inject settings.
The shipped Compose file reads optional `flixr.env`; select another path with
`FLIXR_ENV_FILE=/path/server.env`. Compose reads this file on the host and supplies
the variables. Merely mounting a `.env` file inside a container does not load it.
See [Docker's environment documentation](https://docs.docker.com/compose/how-tos/environment-variables/set-environment-variables/).

Explicit environment settings apply at startup. Omitted roots/token retain saved
settings; provided roots/token are imported into the saved configuration. Playback
environment values overlay saved settings without persisting that overlay. UI changes
can apply during the process lifetime; an explicit environment value wins again on
restart. To hand management back to the UI, remove that variable and recreate the
container. Per-profile preferences/history remain application data, not environment.
Owners can rename or delete household profiles and revoke active browser sessions from
Server settings. Deleting a profile removes its profile-owned history and lists, and
signs that profile out; changing or removing its PIN also signs its active sessions out.

| Variable | Default / behavior |
|---|---|
| `FLIXR_DATA_DIR` | `/config` in Docker; `./flixr-data` natively |
| `FLIXR_SEGMENT_DIR` | `/cache/segments` in Docker; saved setting or data-dir `segments` natively; absolute path |
| `FLIXR_LISTEN_ADDR` | `0.0.0.0:8787` in Docker; `127.0.0.1:8787` natively |
| `FLIXR_FILMS_ROOT`, `FLIXR_TV_ROOT` | Saved roots; container paths. Explicit empty value clears that root |
| `FLIXR_TMDB_TOKEN` / `FLIXR_TMDB_TOKEN_FILE` | Optional TMDB read-access token; empty direct value removes saved token |
| `FLIXR_OWNER_PASSWORD` / `FLIXR_OWNER_PASSWORD_FILE` | Optional first-start owner claim; never resets an existing owner |
| `FLIXR_INITIAL_PROFILE` | Optional unprotected initial profile, created only for a claimed household with no profiles |
| `FLIXR_SCAN_ON_START` | `false`; start a background scan on boot |
| `FLIXR_SCAN_WORKERS` | `4`, range 1–32; startup scan worker count |
| `FLIXR_GENERATION_BYTES` | Saved value or `268435456` (256 MiB) |
| `FLIXR_GLOBAL_BYTES` | Saved value or `536870912` (512 MiB) |
| `FLIXR_MAX_GENERATIONS` | Saved value or `2`; must fit the reserved global byte budget |
| `FLIXR_LEASE_TTL` | `45s` |
| `FLIXR_HEARTBEAT_INTERVAL` | `15s`; must be less than half the lease TTL |
| `FLIXR_SEGMENT_WINDOW` | `60s` |
| `FLIXR_PROCESS_GRACE` | `2s` |
| `FLIXR_TLS_CERT`, `FLIXR_TLS_KEY` | Both required when serving TLS directly; paths to readable mounted files |
| `FLIXR_DEMO` | `false`; development only; never enable on a household installation |

Durations use `15s`, `2m`, etc. Byte counts are integers. Invalid resource combinations
fail startup. Docker host settings (`FLIXR_MEDIA_DIR`, `FLIXR_PORT`, `FLIXR_BIND_ADDR`,
`FLIXR_VERSION`, `FLIXR_ENV_FILE`) are Compose substitutions, not app settings.
The supplied Compose file fixes its container listener and storage paths; override
those service values and matching mounts/health check together if changing them.

For credentials, prefer [Compose secrets](https://docs.docker.com/compose/how-tos/use-secrets/):

```yaml
services:
  flixr:
    environment:
      FLIXR_OWNER_PASSWORD_FILE: /run/secrets/owner_password
      FLIXR_TMDB_TOKEN_FILE: /run/secrets/tmdb_token
      FLIXR_INITIAL_PROFILE: Home
    secrets: [owner_password, tmdb_token]
secrets:
  owner_password:
    file: ./secrets/owner_password
  tmdb_token:
    file: ./secrets/tmdb_token
```

Save that as an override and use `-f compose.release.yml -f compose.secrets.yml`.
Make files readable by the container user. The `_FILE` reader bounds files to 16 KiB
and strips trailing newlines; setting both forms fails rather than guessing.
No automatic password reset occurs when a secret changes. Mounted TLS keys use their
path variables directly. No arbitrary env-file parser or shell entrypoint is needed.

## Update and recovery

Stop the service before backing up `/config`; retain its entire contents together.
Do not copy a live SQLite main database alone while its WAL is active. Then:

```sh
docker compose -f compose.release.yml pull
docker compose -f compose.release.yml up -d --wait
docker compose -f compose.release.yml logs --tail=50 flixr
```

`down` preserves named volumes; `down -v` deletes them. Cache may be discarded while
stopped. To roll back, stop the service, restore the matching config backup if a
schema migration occurred, select the previous image version/digest, and restart.
Never run old and new containers against the same config or segment directory.
Existing source/demo Compose files continue mounting their previous `/data` volumes
and explicitly set `FLIXR_DATA_DIR`; no automatic data migration is performed.

## Database and log maintenance

FlixR retains the current catalog scan report and the 31 most recent earlier
reports. Saving a scan status and pruning older reports is one database transaction;
the matching per-file outcomes are removed with the report. Owner-recovery audit
events are durable security records and are not deleted by scan, artwork, or log
maintenance.

SQLite remains in WAL mode and uses its conservative automatic checkpoint threshold
of 1,000 WAL pages. FlixR does not schedule manual checkpoints or a full `VACUUM`.
Stop FlixR before any operator-run database maintenance and keep the database, WAL,
and shared-memory files together when backing up or restoring the data volume.

FlixR writes application logs to stdout and stderr; the container runtime owns those
logs. The shipped release Compose file uses Docker's `json-file` driver with three
10 MiB files. If a deployment replaces that logging block or uses another runtime,
configure an equivalent finite size and file count there. A Compose override can
apply the same bound explicitly:

```yaml
services:
  flixr:
    logging:
      driver: json-file
      options:
        max-size: "10m"
        max-file: "3"
```

### Recover a forgotten owner password

Recovery is a host-local command. It has no HTTP API, needs no network access, and
requires write access to the existing protected `/config` volume. Stop Flixr first
so the command can take its data lock. Put the replacement password in a regular
file readable only by the container user; do not put it in a command line or log.

```sh
docker compose stop flixr
docker compose run --rm -T --no-deps \
  -v "$PWD/owner-password:/run/secrets/owner-password:ro" \
  flixr recover-owner --password-file /run/secrets/owner-password
docker compose up -d --wait
```

The command replaces the password through the normal Argon2 hashing path, records
an `owner_recovered` audit event in SQLite, and atomically revokes every owner
browser session. Profiles and their sessions remain available. Remove the temporary
password file with your normal secret-handling process after a successful recovery.
If the command reports a failure before success, the database transaction leaves the
previous credential intact. If the host or command was interrupted and the outcome is
unknown, try the replacement password first, then the previous password; rerun local
recovery if neither works. Do not assume an interrupted command rolled back after it
committed.

For a native install, stop its service and run
`./flixr recover-owner --password-file /path/to/owner-password`; add
`--data-dir /path/to/flixr-data` when it differs from `FLIXR_DATA_DIR` or the
default `./flixr-data`.

## HTTPS on the LAN

Use a certificate trusted by household devices for the server hostname. Mount its
certificate and private key with `compose.https.yml`, then:

```sh
FLIXR_BIND_ADDR=0.0.0.0 docker compose -f compose.release.yml -f compose.https.yml up -d --wait
```

Open `https://YOUR-SERVER-HOSTNAME:8787`. Direct TLS makes session cookies Secure.
The local health probe skips certificate-name verification because it connects to
loopback; browser clients must still validate the hostname and certificate. Keep
keys readable only by the required host/container accounts. A reverse proxy is also
possible, but do not trust arbitrary forwarded headers or expose its backend port;
this release's provided secure deployment uses direct TLS.
