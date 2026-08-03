#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
npm --prefix web run check:api
go test ./internal/httpapi ./internal/domain
