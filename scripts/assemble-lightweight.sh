#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."

# The lightweight artifact contains only the executable, a settings template,
# licenses/notices, and its manifest. No Graph/Python/model input is accepted.
exec go run ./internal/packageinfo/cmd/assemble-lightweight "$@"
