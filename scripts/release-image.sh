#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mode="${1:-}"
version="${2:-}"
[[ "$mode" == prepare || "$mode" == promote ]] || { echo 'Expected prepare or promote' >&2; exit 1; }
[[ "$version" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || { echo 'Expected a stable semantic version' >&2; exit 1; }
[[ "${GITHUB_REF:-}" == refs/heads/main && "${GITHUB_EVENT_NAME:-}" == push ]] || { echo 'Publishing requires a main push' >&2; exit 1; }
[[ "${GITHUB_REPOSITORY:-}" == mopeyjellyfish/flixr ]] || { echo 'Unexpected release repository' >&2; exit 1; }
[[ "${GITHUB_SHA:-}" == "$(git rev-parse HEAD)" ]] || { echo 'Release checkout differs from tested commit' >&2; exit 1; }
image="ghcr.io/mopeyjellyfish/flixr"
if [[ "$mode" == prepare ]]; then
  # Build and verify before semantic-release creates the Git tag / GitHub release.
  docker buildx build --platform linux/amd64,linux/arm64 --push --no-cache-filter runtime \
    --tag "$image:v$version" --tag "$image:sha-$GITHUB_SHA" \
    --build-arg "VERSION=$version" --build-arg "REVISION=$GITHUB_SHA" \
    --cache-from type=gha,scope=release --cache-to type=gha,scope=release,mode=max \
    --provenance=true --sbom=true .
  for arch in amd64 arm64; do
    FLIXR_SMOKE_PLATFORM="linux/$arch" bash scripts/image-smoke.sh "$image:v$version" "$version"
  done
else
  # Moving aliases are promoted only after the versioned image and release succeed.
  docker buildx imagetools create \
    --tag "$image:latest" --tag "$image:${version%.*}" --tag "$image:${version%%.*}" \
    "$image:v$version"
fi
