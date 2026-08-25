# AI Balance Design Workflow — Implementation Handoff

Date: 2026-08-25  
OpenSpec change: `ai-balance-design-workflow`

## Frozen v1 identities

| Registry entry | Version | SHA-256 identity |
|---|---:|---|
| Prompt `ai-balance-design` | v1 | `1dbd1f20d5ff02ff0c3397154219aa468a0d39fa8a90fba94e83bbafa854d010` |
| DraftPatch Schema `draft-patch` | v1 | `c70965d5a5380509607678cd7efcb92041b2215bc926f7c609d62ada47b15bc7` |
| Budget `ai-budget-policy` | v1 | `d3f3b033bdfa8a3baabd771ed291de08dad4ae5a3ce37b469f97a21e9d332db6` |
| Orchestrator `ai-design-orchestrator` | v1 | `de577e79e3482ac738e3a9fd789c6d7d95b81495814d52708027dca0f88cee79` |
| Tool `read_revision_context` | v1 | `a0e4446427a793611ad1e2f48c8b538149d7a04362272a75c4520226d6b2274e` |
| Tool `retrieve_evidence` | v1 | `9912623bbef0370fc7b206eeae3f8edf1044cc4b5438238d9bc4a09bf9daad89` |
| Tool `validate_proposal` | v1 | `94abbf27aa33a28625c7f308331d77e97cfd26d1621fd8713704b4019d8e8e8d` |
| Tool `preview_simulation` | v1 | `83f10c7ef08f913c28c47ade964150afc1dffb6b1843d91c6b7ed39176e669a7` |
| Tool `preview_risk` | v1 | `19c01412c5102788ced2aa526ec2bca0bb89638f875f4679e4096cbbc02708fe` |
| Tool `search_parameters` | v1 | `2f288dd2ceddb67d8d4298b71398e1940812a449f2a2f943e1878f8070b8ba38` |

Validation uses `schema-v1`, `dsl-v1`, `ast-v1`, formula registry manifest `f19067585963197109c484e1e02f7a46cbf3ec508ac81391756b27b089bb93ab`, and numeric policy `numeric-v1` (decimal128, 34 digits, HALF_EVEN). Simulation registers the 14 closed `simulation-*`/`metric-*` descriptors from `simulation/contract.V1Descriptors`, all at v1. Risk registers `risk-threshold-schema`, `risk-cohort`, `risk-structural`, `risk-report-schema`, and the three versioned comparison directions from `risk/contract.V1Manifests`, all at v1.

The deterministic advisory golden chain is:

- validation: `db74ff30b6254a94b96828a57d4346d1fa95aee985e93cf31a93728819d8be9b`
- simulation: `e8d99d80990e17756a5e6427cdf3e4929024791551d2385b742f5f30d27e6148`
- risk: `ed03cad15565ec9caac0730897b76a3cb50ae770a8e59780592d62aaa9929e78`
- acceptability decision: `ae56bfb49532ad909efb819e9b075872ad4ae3f4e15797857d5e6206fb330a3e`

## Fixture matrix

- Provider stream fixture: `internal/ai/provider/openai/testdata/stream-fixtures.json`; scenarios `valid_structured_patch`, `multiple_tool_calls`, `malformed_json`, `oversized_json`, `timeout`, `disconnect_without_done`, `cancellation`, `late_terminal_after_cancel`, and `usage_accounting`.
- Provider capability fixture: `internal/ai/provider/openai/testdata/capability-fixtures.json`; scenarios `available`, `model_unavailable`, `structured_output_unsupported`, `tool_calls_unsupported`, `streaming_degraded`, `provider_unavailable`, and `probe_timeout`.
- Retrieval fixture `local-rag-hybrid-graph-retrieval-v1` is version `1.0`, sourced from local-rag commit `cfbf6106a38bc46730217b987ca54c6b1b302f20`; OpenAPI SHA-256 is `fd39c71846e49f0f6a7b4b1dc69a089634006af002d36af58c61444611df9369`. The fixture manifest pins every request/response/error/transcript file hash.
- Provider/model identities are resolved and sealed per attempt because endpoint/model are user configuration. They are recorded alongside the fixed Prompt/Schema/tool/orchestrator/budget/input/evaluator identities; Provider text remains redacted recorded non-deterministic output and is never a deterministic evaluator fact.

## Capacity and isolation bounds

- v1 hard limits: 3 format repairs, 4 Provider turns, 12 tool calls, 128 search candidates, 120 seconds, 128 KiB context, 32 KiB structured output, and 32 KiB tool result.
- Each AI run retains at most 8 MiB of canonical run data. SQLite database/index/WAL growth for one maximal run is bounded to 24 MiB.
- Representative 2,000-entity/20,000-reference FULL validation and 10,000-entity/100,000-reference materialization fixtures pass. Cancellation intent latency is bounded to 250 ms.
- AI packages cannot import storage/project/release/revision/Graph activation/HTTP implementations. AI HTTP handlers cannot import release or Graph activation implementations. The AI UI contains no release or Snapshot activation endpoint. Accept creates exactly one revision and returns `published=false`; release remains a separate human workflow.

## Passing verification commands

All commands were run from the repository root on macOS/arm64 on 2026-08-25:

```text
go test ./...                                                        # 1092 passed / 65 packages
./scripts/check-contract.sh                                         # passed
npm --prefix web run check:api                                      # no generated drift
npm --prefix web run typecheck                                      # passed
npm --prefix web run lint                                           # passed, 0 warnings
npm --prefix web test                                               # 65 passed / 19 files
npm --prefix web run build                                          # passed; existing chunk-size warning only
npm --prefix web run e2e                                            # 30 passed
go test -race ./internal/ai/... ./internal/storage/sqlite ./internal/httpapi ./tests/contract -skip 'Capacity'
                                                                    # 649 passed / 20 packages, no race
go test ./internal/ai/patch -run '^$' -fuzz FuzzDecodeV1NeverExpandsFrozenScope -fuzztime 3s
go test ./internal/ai/patch -run '^$' -fuzz FuzzPatchPathCannotEscapeForbiddenRoots -fuzztime 3s
go test ./internal/ai/tools -run '^$' -fuzz FuzzPolicyGuardNeverAuthorizesUnregisteredCapability -fuzztime 3s
go test ./internal/ai/tools -run '^$' -fuzz FuzzPromptInjectionRemainsInertToolData -fuzztime 3s
openspec validate ai-balance-design-workflow --strict --no-interactive
go test ./internal/platform/credential ./internal/ai/provider ./internal/httpapi -run 'Credential'
                                                                    # 11 passed / 3 packages
go test -race ./internal/platform/credential                        # 4 passed, no race
GOOS=windows GOARCH=amd64 go test -c ./internal/platform/credential -o /tmp/eco-guardian-credential-windows-amd64.test.exe
GOOS=windows GOARCH=amd64 go test -c ./internal/ai/provider -o /tmp/eco-guardian-ai-provider-windows-amd64.test.exe
                                                                    # Windows implementations/tests compile
git diff --check                                                    # passed
```

The ordinary (non-race) suite includes all capacity timing fixtures. Running those timing assertions under race instrumentation produced no race report, but the 2,000-entity local-save timing rose to 9.26 seconds versus its normal-build 5-second threshold; the passing race command therefore excludes tests named `Capacity`.

Windows Credential Manager policy is exercised through a deterministic native-API seam: Put/Get/Delete, target validation, NOT_FOUND/native error mapping, cancellation, defensive copying and temporary-buffer clearing all run on this host, including under `-race`. The production `advapi32` binding and the same tests cross-compile into the Windows test binary. No Wine or Windows runner was present, so an actual Windows vault round-trip remains a packaging smoke test rather than a unit-test dependency.
