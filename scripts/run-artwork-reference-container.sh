#!/usr/bin/env bash
# Build and run a disposable issue #56 measurement container on a Linux Docker host.
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
result_dir="${FLIXR_MEASURE_RESULT_DIR:-$repo_root/artwork-reference-results-$(date -u +%Y%m%d-%H%M%S)}"
name="flixr-artwork-reference-$$"
image="$name-image"
source_sha="$(git -C "$repo_root" rev-parse HEAD)"

cleanup() {
  docker rm -f "$name" >/dev/null 2>&1 || true
  docker image rm "$image" >/dev/null 2>&1 || true
}
trap cleanup EXIT

require() { command -v "$1" >/dev/null 2>&1 || { echo "missing required command: $1" >&2; exit 1; }; }
for command in docker git awk lscpu uname; do require "$command"; done
[[ "$(uname -s)" == Linux ]] || { echo "reference-container measurement requires a Linux Docker host" >&2; exit 1; }
mkdir -p "$result_dir"
chmod 0777 "$result_dir"

{
  printf 'captured_at_utc='; date -u +%FT%TZ
  printf 'source_sha=%s\n' "$source_sha"
  printf 'kernel='; uname -srmo
  printf 'cpu_online='; nproc
  printf 'cpu_model='; lscpu | sed -n 's/^Model name:[[:space:]]*//p' | head -n 1
  printf 'mem_total_kib='; awk '/MemTotal:/ {print $2}' /proc/meminfo
  printf 'mem_available_kib='; awk '/MemAvailable:/ {print $2}' /proc/meminfo
  printf 'docker_version='; docker version --format '{{.Server.Version}}'
  printf 'docker_resources='; docker info --format 'cpus={{.NCPU}} mem={{.MemTotal}} cgroup={{.CgroupVersion}}'
  printf 'container_cpus=4\ncontainer_memory_bytes=8589934592\ncontainer_memory_swap_bytes=8589934592\ncontainer_pids_limit=512\ncontainer_network=none\ncontainer_filesystem=read-only-with-tmpfs\n'
} >"$result_dir/environment.txt"

docker build --pull=false --build-arg "SOURCE_SHA=$source_sha" -f "$repo_root/Dockerfile.artwork-measurement" -t "$image" "$repo_root"
docker run --name "$name" \
  --network none \
  --read-only \
  --tmpfs /tmp:rw,exec,size=2g \
  --shm-size=1g \
  --cpus=4 \
  --memory=8g \
  --memory-swap=8g \
  --pids-limit=512 \
  --cap-drop=ALL \
  --security-opt no-new-privileges \
  --mount "type=bind,source=$result_dir,target=/results" \
  "$image"
docker inspect "$name" >"$result_dir/container-inspect.json"
docker image inspect "$image" >"$result_dir/image-inspect.json"
printf 'Results: %s\n' "$result_dir"
