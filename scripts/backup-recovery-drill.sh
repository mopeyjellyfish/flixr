#!/bin/sh
set -eu

repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
cd "$repo/backend"

# Both tests use disposable source, archive, and restore directories. They never
# inspect or modify the configured household data.
go test ./backup . -run 'TestManagerPersistsPolicyRunsAndRestoresAutomaticBackup|TestRestoreBackupCommandRestoresVerifiedArchiveOffline' -count=1
