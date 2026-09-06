#!/usr/bin/env bash
# Seed semantic-release's history; v0.0.0 is a Git baseline, never a release/image.
set -euo pipefail
if ! git tag --merged HEAD --list | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  git tag v0.0.0 "$(git rev-list --max-parents=0 HEAD | tail -1)"
fi
