#!/usr/bin/env bash
# Execute the issue #56 workload wholly inside a resource-limited container.
set -euo pipefail

seed_dir="$(mktemp -d /tmp/flixr-artwork-seed.XXXXXX)"
cleanup() { rm -rf "$seed_dir"; }
trap cleanup EXIT

node /work/scripts/generate-artwork-measurement-demo.mjs "$seed_dir"
FLIXR_MEASURE_DEMO_SOURCE="$seed_dir" \
  FLIXR_MEASURE_FIXTURE_SOURCE='/work/fixture/Long Duration Seek 2026.mkv' \
  FLIXR_MEASURE_PREBUILT_BINARY=/usr/local/bin/flixr \
  FLIXR_MEASURE_CGROUP_ROOT=/sys/fs/cgroup \
  FLIXR_MEASURE_RESULT_DIR=/results \
  FLIXR_MEASURE_PORT=18787 \
  bash /work/scripts/measure-artwork-acceptance.sh
