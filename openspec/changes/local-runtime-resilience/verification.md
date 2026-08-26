# Verification Traceability

This table maps every specification scenario to the automated test or acceptance environment that owns it. `.github/workflows/windows-runtime.yml` owns continuous Windows Server 2025 x64 execution through `scripts/test-windows-runtime.ps1`. `scripts/windows-clean-vm-smoke.ps1` separately owns artifact acceptance for both package modes on a clean Windows 11 x64 client VM. Neither Windows Server nor Windows 11 ARM emulation substitutes for that client-VM report.

| Scenario | Automated evidence |
|---|---|
| Start from a clean machine | `internal/bootstrap.TestCompositionStartsAppliedServicesAndClosesInOwnershipOrder`; `scripts/windows-clean-vm-smoke.ps1` (clean Windows 11 x64 client execution pending) |
| Browser launch fails | `internal/bootstrap.TestBrowserFailureKeepsExactConsoleURLAndEmitsOnlySafeDiagnostic` |
| Preferred port is unavailable | `internal/httpapi.TestRuntimePreferredPortCollisionFallsBackWithoutRace` |
| Recent project cannot be reopened safely | `internal/bootstrap.TestRecentProjectReopenUsesManagerAndPreservesUnsafeProjects` |
| Complete package is unpacked and launched | `internal/packageinfo.TestAssembleCompleteStagesLocalInputsAndVerifiedManifest`; `scripts/windows-clean-vm-smoke.ps1` |
| Required model asset is missing | `internal/bootstrap.TestMissingCompleteComponentsDegradePreciselyWithoutExecutingThem` |
| Lightweight package has no external Graph service | `internal/packageinfo.TestAssembleLightweightContainsNoBundledRuntimeOrModels`; `graphprocess.TestSelectorLightweightKeepsIncompatibleOccupantUntouched` |
| Unsupported operating system is used | `internal/platform/appdir.TestResolveHostReportsUnsupportedPlatform`; platform adapter tests |
| Compatible external service is already running | `internal/app/runtime/graphprocess` selection tests; `internal/bootstrap.TestGraphRuntimeDependencySelectsExternalAndReprobesWithoutOwningIt` |
| Bundled child starts successfully | Windows `internal/platform/process.TestWindowsOwnedProcessRedirectsOutputAndWaitsByLiveHandle`; supervisor Windows tests; Windows Server x64 workflow |
| Owned child crashes repeatedly | Windows `graphprocess.TestWindowsSupervisorCrashLoopExhaustion`; Windows Server x64 workflow |
| Eco Guardian exits unexpectedly | Windows `process.TestWindowsParentCrashClosesOwnedJobTree`; Windows Server x64 workflow |
| Endpoint is occupied by an incompatible process | `graphprocess.TestSelectorLightweightKeepsIncompatibleOccupantUntouched` |
| Contract-compatible service has a different implementation major | `tests/contract.TestLocalRAGRuntimeConsumerMalformedTimeoutAndRecovery` |
| Known unfixed build is detected | `graphprocess` compatibility/known-fix table tests |
| Optional retrieval dependency is degraded | `tests/contract.TestLocalRAGRuntimeConsumerReplaysHealthVariantsThroughActualClient` |
| Required Graph contract is missing | `graphprocess` compatibility evaluator tests |
| Graph health times out | `tests/contract.TestLocalRAGRuntimeConsumerMalformedTimeoutAndRecovery` |
| Graph core is unavailable | `capability.TestDefaultCapabilityFailureMatrix` |
| Vector and Rerank are degraded | `capability.TestDefaultCapabilityFailureMatrix`; local-rag retrieval consumer tests |
| AI Provider alone is unavailable | `httpapi.TestUnavailableAICapabilityDoesNotChangeReleaseOrGraphCapabilities` |
| One dependency recovers | capability independent-recovery tests |
| Release requirements remain incomplete | capability release Gate completeness tests |
| Runtime status is read repeatedly | `httpapi.TestRuntimeStatusHandlerReturnsGeneratedSafeDTOWithoutSideEffects` |
| User inspects a degradation banner | `web/tests/runtime-ui.spec.ts` |
| Reconnect succeeds | `internal/httpapi.TestRuntimeActionsEnforcePreconditionsAndIdempotency`; `internal/bootstrap.TestGraphRuntimeDependencySelectsExternalAndReprobesWithoutOwningIt`; runtime Pinia action test in `web/tests/runtime-ui.spec.ts` |
| Page reload occurs during recovery | Graph/simulation/risk/AI persistent SSE ordinal and polling tests |
| Deterministic Job is interrupted | runtime deterministic recovery adapter tests |
| Deterministic Job fingerprint changed | simulation/risk recovery mismatch tests |
| AI Provider call was in flight | AI recovery and fault-matrix tests |
| External Graph Task was accepted | `graphsync.TestRecoveryPollsOriginalTaskBeforeAnyReconciliation` and submission crash tests |
| User cancels after external acceptance | graph shutdown/recovery tests |
| User changes a valid runtime setting | `httpapi.TestSettingsHandlerAtomicallyUpdatesRuntimeSettingsAndReportsEffects`; runtime settings UI test |
| Invalid settings are submitted | config validation plus settings Problem Details tests |
| Non-loopback unauthenticated Graph endpoint is configured | config loopback policy tests |
| Project is backed up | runtime data-separation and credential omission tests |
| Component failure is correlated | diagnostics middleware/correlation and child-sink tests |
| Logs exceed their configured bound | `diagnostics.TestLoggerRotatesReopensFlushesAndCorrelatesHTTP` |
| Logging fails | `diagnostics.TestLoggerFailureIsolationAndBackpressure` |
| Clean complete-package smoke test | `scripts/windows-clean-vm-smoke.ps1` and `packaging/WINDOWS-CLEAN-VM.md` (clean Windows 11 x64 client report pending) |
| Partial services recover independently | capability matrix and runtime UI combined-failure tests |
| Crash occurs at each recovery boundary | recovery, Graph submission, AI and SQLite fault-injection suites |
| External service survives application exit | Windows `process.TestWindowsExternalProcessSurvivesOwnedJobClose`; `internal/bootstrap.TestGraphRuntimeDependencySelectsExternalAndReprobesWithoutOwningIt`; Windows Server x64 workflow |

## Local gate record (2026-08-25)

- `go test ./...`: 1328 tests passed across 77 packages.
- `go test -race` on bootstrap, runtime, HTTP, package, Graph sync, and SQLite: all functional/race checks passed. The non-race-only `TestValidationCapacityFixtures` timing assertion was excluded from the SQLite race rerun after race instrumentation raised its 5-second benchmark to 9.13 seconds; the same benchmark passed in the ordinary full suite.
- Targeted race repetition for production Graph composition and runtime actions: 80 tests passed (`-count=20`).
- Repeated lifecycle/resource loops: 300 tests passed across bootstrap shutdown, supervisor restart/stop, logging/SSE, SQLite reopen, and project lock/close groups (`-count=20`).
- Frontend: 73 Vitest tests, typecheck, lint, and production build passed.
- `go vet ./...`, generated Go/TypeScript OpenAPI drift check, strict OpenSpec validation, package/fixture digest and reproducibility tests passed.
- Windows amd64 production packages and the platform-process, Graph-supervisor, and bootstrap test binaries cross-compiled successfully.
- The Windows Server 2025 x64 workflow and reusable PowerShell gate are checked in but have not run on a remote hosted runner in this workspace, so their native race/handle/resource result remains pending.
- The non-elevated clean Windows 11 x64 client-VM artifact report remains the final release-environment gate and is pending. Windows 11 ARM emulation does not satisfy it.
