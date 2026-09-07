# Support and diagnostics

FlixR support happens in public GitHub issues on a best-effort basis; there is no
support email, chat service, or response-time commitment. Use the support template
for installation or operation questions, and the bug template for a reproducible
defect. Search existing issues first.

For setup, updates, backups, network exposure, and environment settings, start
with [docs/docker.md](docs/docker.md). For local development and checks, use
[docs/development.md](docs/development.md).

## Attach diagnostics

Describe the FlixR version or image tag, operating system, browser and version
when relevant, installation method, expected result, actual result, and exact
steps to reproduce. A small redacted log excerpt around the failure is more useful
than a whole database, media file, configuration volume, or browser profile.

An owner can download **Support diagnostics** from Server settings. The local ZIP
contains the Flixr build/runtime version, tool and scan health, active playback
count, and up to 100 recent failure IDs. Browser API failures display the matching
support ID. Attach the archive only when you choose to share it with a public
issue. It is bounded, contains no raw errors, media names, filesystem paths,
configuration values, passwords, tokens, or database content, and is cleared on
restart. It is not hosted telemetry.

Before posting, remove or replace all setup tokens, owner passwords, profile PINs,
session cookies, `Authorization` headers, TMDB tokens, TLS keys and certificates,
hostnames/IP addresses, user names, media paths, titles that identify a household,
and Docker environment files. Do not attach `flixr.env`, `.env`, `secrets/`,
`flixr-data`, browser storage, or `docker compose config` output. These can
contain credentials or private household data.

Security reports follow [SECURITY.md](SECURITY.md), not the public templates.
