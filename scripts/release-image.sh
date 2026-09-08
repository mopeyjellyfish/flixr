#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."
mode="${1:-}"
version="${2:-}"
[[ "$mode" == prepare || "$mode" == promote ]] || { echo 'Expected prepare or promote' >&2; exit 1; }
[[ "$version" =~ ^0\.[0-9]+\.[0-9]+$ ]] || { echo 'Only 0.x releases are authorized; v1 requires an explicit policy change' >&2; exit 1; }
[[ "${GITHUB_REF:-}" == refs/heads/main && "${GITHUB_EVENT_NAME:-}" == push ]] || { echo 'Publishing requires a main push' >&2; exit 1; }
[[ "${GITHUB_REPOSITORY:-}" == mopeyjellyfish/flixr ]] || { echo 'Unexpected release repository' >&2; exit 1; }
[[ "${GITHUB_SHA:-}" == "$(git rev-parse HEAD)" ]] || { echo 'Release checkout differs from tested commit' >&2; exit 1; }
image="ghcr.io/mopeyjellyfish/flixr"
if [[ "$mode" == prepare ]]; then
	metadata_secret=false
	metadata_smoke=0
	if [[ "${FLIXR_REQUIRE_APPLICATION_METADATA:-}" == 1 ]]; then
		[[ -n "${FLIXR_TMDB_APPLICATION_TOKEN:-}" ]] || { echo 'Automatic metadata distribution requires the FlixR TMDB application credential' >&2; exit 1; }
		[[ "$FLIXR_TMDB_APPLICATION_TOKEN" =~ ^[A-Za-z0-9._~-]+$ ]] || { echo 'FlixR TMDB application credential has an invalid format' >&2; exit 1; }
		if ! printf 'header = "Authorization: Bearer %s"\n' "$FLIXR_TMDB_APPLICATION_TOKEN" \
			| curl --fail --silent --show-error --config - https://api.themoviedb.org/3/authentication >/dev/null; then
			echo 'FlixR TMDB application credential did not validate; refusing automatic metadata distribution' >&2
			exit 1
		fi
		metadata_secret=true
		metadata_smoke=1
	elif [[ -n "${FLIXR_TMDB_APPLICATION_TOKEN:-}" ]]; then
		echo 'FlixR TMDB application credential is set without the distribution gate' >&2
		exit 1
	fi
	# Build and verify before semantic-release creates the Git tag / GitHub release.
	build=(docker buildx build --platform linux/amd64,linux/arm64 --push --no-cache-filter runtime
		--tag "$image:v$version" --tag "$image:sha-$GITHUB_SHA")
	if [[ "$metadata_secret" == true ]]; then
		build+=(--secret id=tmdb_application_token,env=FLIXR_TMDB_APPLICATION_TOKEN)
	fi
	build+=(--build-arg "VERSION=$version" --build-arg "REVISION=$GITHUB_SHA"
		--cache-from type=gha,scope=release --cache-to type=gha,scope=release,mode=max
		--provenance=true --sbom=true .)
	"${build[@]}"
	for arch in amd64 arm64; do
		FLIXR_SMOKE_PLATFORM="linux/$arch" FLIXR_EXPECT_APPLICATION_METADATA="$metadata_smoke" bash scripts/image-smoke.sh "$image:v$version" "$version"
	done
else
  # Moving aliases are promoted only after the versioned image and release succeed.
  docker buildx imagetools create \
    --tag "$image:latest" --tag "$image:${version%.*}" --tag "$image:${version%%.*}" \
    "$image:v$version"
fi
