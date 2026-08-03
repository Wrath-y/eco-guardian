#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
npm --prefix web run build
go build -o dist/eco-guardian ./cmd/eco-guardian
