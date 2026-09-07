# Support and diagnostics

FlixR support happens in public GitHub issues on a best-effort basis; there is no
support email, chat service, or response-time commitment. Use the support template
for installation or operation questions, and the bug template for a reproducible
defect. Search existing issues first.

For setup, updates, backups, network exposure, and environment settings, start
with [docs/docker.md](docs/docker.md). For local development and checks, use
[docs/development.md](docs/development.md).

## Attach redacted diagnostics

Describe the FlixR version or image tag, operating system, browser and version
when relevant, installation method, expected result, actual result, and exact
steps to reproduce. A small redacted log excerpt around the failure is more useful
than a whole database, media file, configuration volume, or browser profile.

Before posting, remove or replace all setup tokens, owner passwords, profile PINs,
session cookies, `Authorization` headers, TMDB tokens, TLS keys and certificates,
hostnames/IP addresses, user names, media paths, titles that identify a household,
and Docker environment files. Do not attach `flixr.env`, `.env`, `secrets/`,
`flixr-data`, browser storage, `docker compose config` output, or raw diagnostic
archives. These can contain credentials or private household data.

The planned [privacy-safe support diagnostic export](https://github.com/mopeyjellyfish/flixr/issues/57)
is not available yet. Until it ships, redact diagnostics manually and keep only
the smallest excerpt needed to reproduce the problem.

Security reports follow [SECURITY.md](SECURITY.md), not the public templates.
