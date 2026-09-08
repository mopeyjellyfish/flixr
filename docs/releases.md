# Releases and GHCR

CI runs on branches and pull requests. **Only a successful push to `main`** in
`mopeyjellyfish/flixr` may publish. The release job depends on backend, race/vet,
media integration, frontend, Docker smoke, three-browser and offline acceptance,
and release-policy tests. A failed gate publishes nothing. Pull requests never
receive package-write permissions.

## Versioning

The first published release is **v0.1.0**. Flixr stays below v1 until the maintainer
explicitly authorizes a policy change. `semantic-release` analyzes Conventional
Commits: `fix`/`perf` increment patch and `feat` increments minor. `!` and
`BREAKING CHANGE:` markers do not trigger a major release or a breaking-changes
section in generated notes. Docs/tests/chore-only merges do not create a release.
The image publisher rejects every version outside `0.x` as a second safeguard.

On the first main release, `scripts/release-baseline.sh` creates a **v0.0.0 Git
baseline tag** at the repository's initial commit. This works with semantic-release's
native version calculation: it publishes no v0.0.0 image or GitHub release. The first
feature delivery becomes v0.1.0. The baseline is pushed alongside the first actual
release tag; subsequent runs retain it. A local bare-repository dry-run test verifies
0.1.0, 0.1.1 and 0.2.0, including breaking markers.

The frontend package version is a private build package, not the server's release version.
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

## TMDB application access

Credential-optional official releases remain supported while application distribution
permission is unresolved. Setting the Actions variable
`FLIXR_REQUIRE_APPLICATION_METADATA=1` turns on the fail-closed distribution gate; the
publisher then requires `FLIXR_TMDB_APPLICATION_TOKEN` as a repository secret. It passes
the value to Docker BuildKit as the
`tmdb_application_token` build secret and links it into the server executable. Build
logs and image layers must not contain the value, but anyone with the published binary
can extract it. Treat it as a distributable application credential, not as a secret.

Before creating that repository secret, the maintainer must complete this one-time work:

1. Sign in to a maintainer-controlled TMDB account and open **Settings → API** on a
   desktop browser.
2. Register a non-commercial application named **FlixR**, with
   `https://github.com/mopeyjellyfish/flixr` as its public URL and a description that it
   is a free, open-source, self-hosted movie/TV server whose official binaries make
   server-side metadata and image requests for household libraries.
3. Accept the current TMDB API terms for FlixR and retain the registration record.
4. Establish with TMDB that use by publicly distributed self-hosted binaries, including
   the extractable application bearer credential, is permitted. The general FAQ and API
   terms do not by themselves expressly settle this distribution detail; do not infer
   approval from project non-commercial status.
5. Copy the application's **API Read Access Token** into the Actions secret named
   `FLIXR_TMDB_APPLICATION_TOKEN`. Never use a token registered to another project.
6. Set the Actions variable `FLIXR_REQUIRE_APPLICATION_METADATA` to `1`. A credential
   without this explicit distribution gate is rejected rather than embedded.
7. Run the release gates, inspect logs for credential disclosure, pull the immutable
   multi-architecture image, and verify normal-mode movie, series, and episode enrichment
   without any household token.

Rotate access by updating the same Actions secret and publishing a new 0.x image. The new
binary credential revision schedules one bounded refresh on upgraded installations. If
the provider suspends access or permission changes, stop publication, set remote metadata
disabled in affected deployments, retain local playback, and follow TMDB's termination
and cache-removal requirements.

No separate release-event workflow is required, avoiding the `GITHUB_TOKEN`
event-trigger limitation. Authentication uses GitHub's job token with only
`contents: write` and `packages: write`. Automatic issue/PR comments are disabled.
See [GitHub's container publishing guidance](https://docs.github.com/en/actions/tutorials/publish-packages/publish-docker-images)
and [semantic-release's workflow](https://github.com/semantic-release/semantic-release).

## First public image

The source repository is public, but GHCR package visibility is separate. If
anonymous downloads are intended, verify the package is publicly pullable and its
Actions access permits publishing. [GitHub package visibility](https://docs.github.com/en/packages/learn-github-packages/configuring-a-packages-access-control-and-visibility)

The maintainer watching the first merge should:

- Confirm every CI gate and the release job succeeds.
- Inspect both platform manifests, version/commit labels, SBOM and release notes.
- Confirm an unauthenticated `docker pull ghcr.io/mopeyjellyfish/flixr:v0.1.0` after
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
