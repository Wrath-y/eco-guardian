# Balance Risk Assessment Implementation Handoff

Date: 2026-08-24 (Asia/Shanghai)
Base commit: `58bade0`
Database schema: additive migration v16 (`balance-risk-assessment-v16`)

## Frozen contracts

- `metric-resource@v1`: unit `ratio`, direction `target_range`, inclusive target range `[0.8, 1.2]`.
- Threshold schema `v1`; inactive starter body hash `1483e3397e02eb9d8ce1bb91a070a80018682db569c6dc7f2a8d9bf3d82c4df2`; relative WARNING `0.10`, BLOCK `0.25`; absolute thresholds remain null unless explicitly supplied.
- Risk capability `risk`, Gate `balance`, contract `risk-v1`, Job kind `risk_review`, report schema `v1`.
- Installed implementation version: `33fddba83c82c9767afdf0fb6657bde2394e9a6cbc54a29fb553ec8abad7b95b`.
- V1 Registry set: threshold `risk-threshold-schema@v1`, cohort `risk-cohort@v1`, structural `risk-structural@v1`, report `risk-report-schema@v1`, and comparison manifests `risk-comparison-{higher_is_risk,lower_is_risk,target_range}@v1`.

| Registry manifest | Source hash | Golden hash |
| --- | --- | --- |
| `risk-threshold-schema@v1` | `2af56724a51134c8aa768873fcc2fbdc180006f53402552e059b384deda77ff2` | `d1e5ad0ee1d0fb834e65fd972b1a221452bf8cb2cb92f7f8287c6c49d0b546d9` |
| `risk-cohort@v1` | `578e84b31af18803168a976e045698e663a8e6a8a9a4493a30ac9d14ff3ce4a3` | `00318d1f56c621082967b04669a06a8e9ebf0ab39033e433303ea7314ea3b265` |
| `risk-structural@v1` | `141031a3ed635c790b7c738e708187dfd7b1bacbefaedbcd6781a4c5c33b6e87` | `4eb6233c3308a09b2687fafaf267e643b5ad1f40d751f373e593d36f33a5fcc2` |
| `risk-report-schema@v1` | `a6f597a0f1ffa4cbe36eec09b1c787c9a096b3f873279aa53f75c268878acfcc` | `4280b4c802981601d58839ea377065658ba84d69efca33f36f91454f0951498e` |
| `risk-comparison-higher_is_risk@v1` | `8ff44c3db958e7c53a49c494bed8cb03ea5b03b3690a3e98dd253d82ebfd1c5d` | `276755d0ad5f092323c6fded42b50902342529325e607243fe3b700f52fb8a74` |
| `risk-comparison-lower_is_risk@v1` | `40110f5443cbe027b5ecd5d5965526e9a91387fc4d9e5495bcb78861f8e0b071` | `65c1b458fbabf3150c17ec8b8da79a57c64a26ccb3fa609d544e00ff2ee04832` |
| `risk-comparison-target_range@v1` | `cfb11694410b74a77744d8d7a25a41fe115894e4547ee82dc0f24f2675ea5896` | `c057652427def17406f627bdae38b290889b0f642e37626fdb2dcaa1c87d4b24` |

## Determinism evidence

- `go test ./internal/risk/... -shuffle=on -count=10`: 840 tests passed; a second independent invocation also passed 840 tests.
- `go test ./internal/storage/sqlite -run 'Risk|Threshold' -shuffle=on -count=5`: 100 tests passed.
- The fixed fixtures shuffle maps, subjects/cohorts, policy requirements, Metric/run/implementation enumeration and structural inputs. Golden tests pin canonical inputs/items/reports, Registry manifests and hashes. Optional Graph evidence/provider state, CI formatting and user-facing text are excluded from calculation/severity identities and have isolation tests.

## Capacity evidence

All capacity fixtures preserve every item and exact decimal/rule semantics. The risk limits are 500 policy Metric units or 500 structural units, 8 MiB response payload, 128 MiB allocated bytes and 2 seconds per bounded calculation/seal fixture.

| Fixture | Result | Elapsed/lock | Allocated | Payload |
| --- | --- | ---: | ---: | ---: |
| 500 Metric comparisons | 500 comparable BLOCK items | 23.188792 ms | 12,600,808 B | 963,001 B |
| 500 changed formula units | 1,000 ordered findings | 26.065459 ms | 13,844,696 B | 770,001 B |
| SQLite immutable report seal | 500 stored items | 61.514 ms write lock | 66,747,136 B | 645,404 B |

- SQLite cancellation intent latency on the same fixture: 278.333 microseconds.
- Existing `TestCapacityFixture`/`TestValidationCapacityFixtures` at 10,000 entities and 100,000 facts passed together with the risk storage fixture (5 tests total).
- `TestCompareRevisionSegmentBoundsGeneratedTenThousandEntityFixture` passed for 10,000 entities across 200 revisions.

## Migration and platform evidence

- Local migration/reopen selection passed 8 tests covering v16 migration, migration rollback, atomic report sealing, report-hash corruption, concurrent reads/project reopen, VersionContributor and Gate registration.
- Windows amd64 pure-Go build fixtures succeeded with `GOOS=windows GOARCH=amd64 CGO_ENABLED=0` for `internal/storage/sqlite`, `internal/risk/gate`, `internal/httpapi` and `internal/app`.
- Windows evidence is cross-compilation in this macOS environment; runtime migration/reopen behavior was executed against local SQLite fixtures. Historical reports remain readable when an implementation is missing, with release/Gate state projected as unavailable rather than mutating history.

## Toolchain

- Go `go1.25.4 darwin/arm64`; `modernc.org/sqlite v1.38.2`.
- Node `v22.18.0`; npm `10.9.3`.
- Vue `3.5.40`; Vue Router `4.6.4`; TanStack Vue Query `5.101.4`; Vite `6.4.3`; Vitest `3.2.7`; Playwright `1.62.1`; TypeScript `5.7.3`.
- OpenSpec CLI `1.3.0`.

## Verification record

- `go test ./...`: 650 tests passed across 45 packages.
- `go test -race -skip Capacity ./internal/risk/... ./internal/storage/sqlite ./internal/httpapi ./internal/app/...`: 333 tests passed across 12 packages with no race findings. The initial race invocation ran 338 successful tests and failed only the existing `<5s` capacity timing assertion (9.35s under race instrumentation); the same capacity suite passes without race and its limits were not weakened.
- `go vet ./...` and `gofmt -l` passed with no findings.
- Vue typecheck and ESLint passed; Vitest passed 56 tests in 14 files; production build passed. Vite reported its existing informational chunk-size warning.
- Full Playwright passed 20 tests, including all three balance-risk first-release, subsequent-release and failure workflows.
- `scripts/check-contract.sh` regenerated the TypeScript OpenAPI client and passed Go/API fixtures with no drift.
- `openspec validate balance-risk-assessment --strict` and `git diff --check` passed.
