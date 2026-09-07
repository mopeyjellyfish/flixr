#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")/.."

fake_bin="$(mktemp -d)"
counter="$(mktemp)"
e2e_marker="$(mktemp)"
cleanup() {
  rm -rf "$fake_bin"
  rm -f "$counter" "$e2e_marker"
}
trap cleanup EXIT
printf '0\n' >"$counter"

cat >"$fake_bin/npm" <<'EOF'
#!/usr/bin/env bash
if [[ "$*" == *"test:e2e"* ]]; then
  : >"$METADATA_ACCEPTANCE_E2E_MARKER"
fi
EOF
cat >"$fake_bin/go" <<'EOF'
#!/usr/bin/env bash
sleep 30
EOF
cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env bash
attempt="$(cat "$METADATA_ACCEPTANCE_COUNTER")"
attempt=$((attempt + 1))
printf '%s\n' "$attempt" >"$METADATA_ACCEPTANCE_COUNTER"
((attempt > 60))
EOF
chmod +x "$fake_bin/npm" "$fake_bin/go" "$fake_bin/curl"

PATH="$fake_bin:$PATH" \
  METADATA_ACCEPTANCE_COUNTER="$counter" \
  METADATA_ACCEPTANCE_E2E_MARKER="$e2e_marker" \
  FLIXR_METADATA_ACCEPTANCE_STARTUP_ATTEMPTS=61 \
  FLIXR_METADATA_ACCEPTANCE_POLL_INTERVAL=0 \
  bash scripts/metadata-acceptance.sh

test -f "$e2e_marker"
test "$(cat "$counter")" = 61
