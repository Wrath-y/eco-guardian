#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
contract_tmp=$(mktemp)
cp web/src/api/generated.ts "$contract_tmp"
npm --prefix web run generate:api
cmp -s "$contract_tmp" web/src/api/generated.ts
rm -f "$contract_tmp"
go test ./internal/httpapi ./internal/domain ./internal/formula ./internal/validation
