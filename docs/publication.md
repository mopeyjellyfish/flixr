# Verify a public release

This is the remaining publication check for [#91](https://github.com/mopeyjellyfish/flixr/issues/91).
It does not change repository or package visibility. Prepare the release and obtain
explicit maintainer approval before making either public. Releases stay on **0.x**.

## Before publication

- Confirm the release's CI passed and its source tag and image commit label agree.
- Review the source, image contents and bundled licences for material that should
  not be published. Keep local media, credentials and downloaded demo caches out.
- Record the proposed source tag, image tag, source commit and image index digest.
- Confirm the repository and GHCR package visibility separately with the maintainer.
  An authenticated download does not prove either is publicly accessible.

The short installation guide is in [the README](../README.md). Keep its registry
access limitation until the anonymous checks below pass. Existing release mechanics
are documented in [releases](releases.md); do not create another publisher.

## Current qualification evidence

On 7 September 2026, an unauthenticated download of the public `v0.3.0` source
archive completed with HTTP 200. A properly negotiated anonymous GHCR bearer-token
request also read its image index; the earlier recorded manifest 401 was not a
valid qualification result because it omitted the OCI manifest `Accept` header.

The first credential-free Docker-client pull was then proved on a native Linux
AMD64 host for `v0.4.0`. It resolved to image-index digest
`sha256:554dfba8c70bebe37a1061a726f40233bb7ce7f7be5bd65ee4983a31e1e775ca`.
An isolated native AMD64 Compose installation also passed with fresh named volumes,
an unclaimed setup status before and after restart, loopback-only access, and a
read-only media bind. Its temporary container, network, volumes, client config,
and files were removed after the check. This proves neither the ARM64 native pull
nor its isolated Compose installation.
A fresh Docker Desktop ARM64 client returned `denied`, although the registry's
anonymous ARM64 manifest and blobs were readable over HTTPS; that client result
does not establish a GHCR visibility problem. Repeat the pull on a clean native
Linux ARM64 host. Keep [#91](https://github.com/mopeyjellyfish/flixr/issues/91)
open until both remaining checks pass. No repository or package visibility was
changed during qualification.

## Anonymous source and image checks

Run on a disposable **native Linux AMD64** host, then repeat on a **native Linux
ARM64** host. Install Docker, Compose 2.24+, curl, Python 3 and OpenSSL first. Use a
local Docker daemon with permission to create temporary containers and volumes.
Do not run against household configuration or media volumes.

Set a real published version, then download source without GitHub credentials:

```sh
version=v0.2.0 # Replace with the candidate 0.x release.
verification_dir="$(mktemp -d)"
curl --disable --fail --location --proto '=https' --proto-redir '=https' \
  "https://codeload.github.com/mopeyjellyfish/flixr/tar.gz/refs/tags/$version" \
  --output "$verification_dir/source.tar.gz"
```

A failed download blocks anonymous source verification. Do not add a token to make
this test pass. Inspect the archive contents and licences before running its scripts:

```sh
tar -tzf "$verification_dir/source.tar.gz"
tar -xzf "$verification_dir/source.tar.gz" -C "$verification_dir"
cd "$verification_dir/flixr-${version#v}"
```

Pull through a fresh Docker client configuration with no registry credentials.
Capture the local daemon address before changing the client configuration:

```sh
image="ghcr.io/mopeyjellyfish/flixr:$version"
platform="$(docker version --format '{{.Server.Os}}/{{.Server.Arch}}')"
docker_endpoint="$(docker context inspect --format '{{(index .Endpoints "docker").Host}}')"
mkdir "$verification_dir/anonymous-docker"
docker --config "$verification_dir/anonymous-docker" --host "$docker_endpoint" \
  pull --platform "$platform" "$image"
docker image inspect "$image" --format '{{json .RepoDigests}}'
docker image inspect "$image" --format '{{json .Config.Labels}}'
```

Require `linux/amd64` or `linux/arm64` as appropriate to the physical host. A 401/403,
missing platform, or missing tag is a failed check. Record the digest and OCI version,
revision and licence labels. Repeat for the other native architecture; emulation
alone does not establish native support.

## Install and preserve data

The anonymous pull above is the access check. Use the normal Docker client again
for the existing local-image smoke scripts; some installations discover Compose
through their normal client configuration.

```sh
FLIXR_SMOKE_PLATFORM="$platform" bash scripts/image-smoke.sh "$image" "$version"
bash scripts/compose-smoke.sh "$image"
```

These scripts create uniquely named temporary resources and clean up those resources.
They check the packaged runtime, non-root operation, fresh storage, setup readiness,
Compose settings/secrets, persistent owner access across recreation, and trusted TLS.
They do not qualify every media format or complete the household beta.

Also follow the README exactly in a fresh directory: one-time owner setup, readable
read-only media, a real movie and episode scan, playback, stop/start and retained
profile/history. Record the browser and media fixture IDs. Use the configuration
from that release and its own temporary media/config volumes.

## Evidence and completion

Attach one report per native architecture to #91 with:

- Date, host/architecture, Docker/Compose and browser versions.
- Source tag/commit, image tag/index digest and platform digest.
- Source-download and credential-free pull results.
- Smoke commands/results and manual setup/playback/restart outcomes.
- Licence/content review, limitations and the maintainer's visibility decision.

Only after both reports pass should the README's access limitation be removed and
#91 closed. Leave the issue open if source or image access is unavailable. Passing
this check does not authorize v1 promotion; [#103](https://github.com/mopeyjellyfish/flixr/issues/103)
remains the final release gate.
