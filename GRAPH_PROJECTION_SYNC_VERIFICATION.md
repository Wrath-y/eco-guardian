# Graph projection sync release verification

Verified on 2026-08-21 (Asia/Shanghai).

## Tooling and fixtures

- Go `go1.25.4 darwin/arm64`; Node.js `v22.18.0`; npm `10.9.3`.
- Eco Guardian's SHA-256-checked local-rag snapshot and operability fixtures.
- Compatible sibling `local-rag` provider revision `cfbf610`; its contract suite passed without changes to that repository.

## Verification

- Go unit/integration and race suites, including consumer contracts.
- Vue tests, TypeScript checking, and generated OpenAPI drift checking.
- Strict OpenSpec validation and whitespace/diff checking.

## Capacity evidence

| Fixed profile | Projection/canonicalization/full/delta | Heap |
| --- | ---: | ---: |
| 2,000 Nodes / 20,000 Edges | 3.34 s | 75.5 MiB |
| 10,000 Nodes / 100,000 Edges | 11.37 s | 307.1 MiB |

Both profiles passed the 60-second acceptance limit.
