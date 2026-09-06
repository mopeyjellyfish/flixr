# Flixr

**Your media. Your network.**

Flixr is an MIT-licensed, LAN-first media server for locally owned films and episodic TV.

Delivery Unit 1 provides:

- one-time local owner setup;
- durable SQLite catalog and household profiles;
- bounded film and TV scanning with FFmpeg and ffprobe;
- optional TMDB metadata and locally cached artwork;
- responsive local browsing, search, and film or series details.

Playback and casting belong to later delivery units. Flixr does not provide media acquisition, a hosted account, or WAN automation.

## Requirements

Install these tools:

- Go 1.25;
- Node.js 22 and npm;
- FFmpeg and ffprobe.

## Build and run

Build the frontend and copy it into the Go embed directory:

```bash
npm --prefix frontend ci
npm --prefix frontend run build:embed
```

Build and start Flixr:

```bash
cd backend
go build -o flixr .
./flixr
```

Flixr prints a one-time setup token. Open the displayed local address and use that token to claim the owner account.

By default, Flixr stores data in `./flixr-data` and listens on `127.0.0.1:8787`.

## Bootstrap configuration

Set bootstrap values with environment variables before you start Flixr:

| Variable | Purpose |
| --- | --- |
| `FLIXR_DATA_DIR` | Override the durable data directory. |
| `FLIXR_LISTEN_ADDR` | Override the HTTP listen address. |
| `FLIXR_TLS_CERT` | Set the TLS certificate path. Use with `FLIXR_TLS_KEY`. |
| `FLIXR_TLS_KEY` | Set the TLS private-key path. Use with `FLIXR_TLS_CERT`. |

The owner configures library roots and the optional TMDB token in the web interface. Flixr never returns the stored TMDB token to a client.

## Development

Run the Go checks:

```bash
cd backend
go test ./...
go test -race ./...
go vet ./...
```

Run the frontend checks:

```bash
npm --prefix frontend run lint
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run build
```

Run the mocked browser matrix after Playwright Chromium is available:

```bash
npm --prefix frontend run test:e2e -- --project=chromium
```

The production browser suite runs against the built `flixr` executable. See `.github/workflows/ci.yml` for the complete fixture and environment setup.

## Repository layout

- `backend/` — Go server, SQLite migrations, catalog, household, and embedded web assets.
- `frontend/` — React, TypeScript, Vite, Tailwind CSS, unit tests, and Playwright tests.
- `docs/features/flixr-core/` — accepted pitch, delivery plan, and validation evidence.
- `DESIGN.md` — accepted Cobalt Signal interface direction.

## License

Flixr is available under the MIT License. See `LICENSE`.

FFmpeg and ffprobe are external runtime dependencies. They are not bundled with Flixr.
