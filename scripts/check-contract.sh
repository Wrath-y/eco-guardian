#!/usr/bin/env sh
set -eu
cd "$(dirname "$0")/.."
contract_tmp=$(mktemp)
go_contract_tmp=$(mktemp)
cp web/src/api/generated.ts "$contract_tmp"
cp internal/httpapi/riskdto/risk.gen.go "$go_contract_tmp"
npm --prefix web run generate:api
go generate ./internal/httpapi/riskdto
cmp -s "$contract_tmp" web/src/api/generated.ts
cmp -s "$go_contract_tmp" internal/httpapi/riskdto/risk.gen.go
rm -f "$contract_tmp"
rm -f "$go_contract_tmp"
go test ./internal/httpapi ./internal/httpapi/riskdto ./internal/domain ./internal/formula ./internal/validation
