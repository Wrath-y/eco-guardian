#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."

# All component inputs are pre-pinned local directories. This pipeline has no
# download step and fails if any required input/version is omitted.
exec go run ./internal/packageinfo/cmd/assemble-complete "$@"
