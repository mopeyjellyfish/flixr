# Releases and GHCR

CI runs on branches and pull requests. **Only a successful push to `main`** in
`mopeyjellyfish/flixr` may publish. The release job depends on backend, race/vet,
media integration, frontend, Docker smoke, three-browser and offline acceptance,
and release-policy tests. A failed gate publishes nothing. Pull requests never
receive package-write permissions.

## Versioning

`semantic-release` analyzes commits since the last `v*` tag and generates release
notes. `fix`/`perf` increment patch, `feat` increments minor, and `!` or a
`BREAKING CHANGE:` footer increments major. Docs/tests/chore-only merges do not
create a release. With no previous release tag, the standard first release is
**v1.0.0**; this version does not imply full Plex/Jellyfin parity. The frontend
package version is a private build package, not the server's release version.
The authoritative runtime version is `docker run --rm IMAGE --version`.

Use Conventional Commit PR titles, checked by CI, when squash-merging. Preserve
Conventional Commits when using merge/rebase. Do not manually bump package files
or create version tags for ordinary releases.

## Publication order

1. Serialize releases; checkout full history of the tested `main` commit.
2. Compute version and notes with locked release-tool dependencies.
3. Build AMD64 and ARM64 images, using native build stages and Go cross-compilation.
   Refresh runtime distribution packages on every release, bypassing that stage’s cache.
4. Push `vX.Y.Z` and `sha-FULL_COMMIT_SHA` images with OCI labels, provenance and SBOM.
5. Pull and smoke-test each platform: non-root runtime, FFmpeg encode/probe, UI,
   setup readiness, fresh volumes, read-only root filesystem and graceful stop.
6. Create the Git tag and GitHub release with generated notes.
7. Promote `latest`, `X.Y` and `X` aliases to that verified image index.

No separate release-event workflow is required, avoiding the `GITHUB_TOKEN`
event-trigger limitation. Authentication uses GitHub's job token with only
`contents: write` and `packages: write`. Automatic issue/PR comments are disabled.
See [GitHub's container publishing guidance](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)
and [semantic-release's workflow](https://github.com/semantic-release/semantic-release).

## First merge

The repository is currently private. After the first publication, set the GHCR
package visibility to **public** if anonymous downloads are intended; package
visibility is separate from repository visibility. This one-time GitHub setting
cannot be performed before the package exists. Check the package's Actions access
if publishing permissions have been restricted. [GitHub package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)

The maintainer watching the first merge should:

- Confirm every CI gate and the release job succeeds.
- Inspect both platform manifests, version/commit labels, SBOM and release notes.
- Confirm an unauthenticated `docker pull ghcr.io/mopeyjellyfish/flixr:v1.0.0` after
  setting package visibility, then follow `docs/docker.md` with a fresh config volume.
- Test owner claim, scan, direct playback and compatibility playback; restart and
  confirm local accounts, roots and history persist. Review logs during this first session.

## Failed publication and rollback

Releases span GitHub and a registry, so they are not atomic. A failure before tag
creation can leave a versioned candidate image; moving aliases remain unchanged.
Correct the cause and rerun the main workflow. A failure after tag creation may
require completing the missing GitHub release using that tag and generated notes;
do not move or delete a released tag to force a version bump. If only alias promotion
failed, re-promote the verified version with `docker buildx imagetools create`.
Check commit labels before any manual recovery. Routine releases are automatic;
partial cross-service failures require maintainer inspection.

Rollback by pinning the prior digest/version, restoring a compatible config backup
when required. A failed readiness check, lost configuration or playback regression
is a stop/rollback signal. Do not auto-update household servers: users control pull
and restart timing. Pin supported Alpine/build images and review dependency updates;
FFmpeg capabilities and working playback matter more than removing a few base megabytes.
