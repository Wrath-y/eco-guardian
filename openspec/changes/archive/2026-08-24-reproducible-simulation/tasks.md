## 1. Prerequisite Boundaries and Frozen Contracts

- [x] 1.1 Verify #5 exposes project-scoped immutable revision/release materialization, `config_hash`/VersionManifest reads, Schema Registry and transaction/repository ports; stop apply if absent instead of creating a parallel revision model.
- [x] 1.2 Verify #6 exposes exact revision/config/version FULL `ValidationGate`, typed AST/evaluator, DSL/Registry manifests, decimal128 NumericPolicy and unit adapters; stop if simulation would need to copy or weaken them.
- [x] 1.3 Verify #7 exposes VersionContributor and Gate Registry, unified `JobStore/EventStore`, persisted cancellation/SSE/Last-Event-ID, idempotency, migration ledger, revision timeline result links and generated OpenAPI boundaries.
- [x] 1.4 Confirm #8/#9 remain optional consumers of the same #7 infrastructure and add import/contract tests proving `internal/simulation` does not depend on Graph, local-rag, impact, AI or model-provider packages.
- [x] 1.5 Add the `internal/simulation/{contract,scenario,engine,random,metric,orchestration,gate}` package boundaries plus transport-neutral ports for revision/validation, scene/run/checkpoint stores, Jobs, clock/IDs and bounded execution.
- [x] 1.6 Freeze versioned manifests and unique stable IDs for simulation input/result schemas, scene contract, engine/event/time semantics, PRNG, evaluator adapters, five Metric Modules and aggregation; fail startup/tests on duplicates, missing dependency declarations or same-version hash drift.
- [x] 1.7 Add compile-time fakes and contract suites for every upstream/downstream port so the pure engine and orchestrator can be tested without SQLite, Gin, Vue, Graph or mutable working state.

## 2. Scenario Registry, Templates, and Persistence

- [x] 2.1 Define the bounded scenario schema for participants/targets, ordered actions, initial attributes/resources/effects, duration, default seed, typed adjustable parameters and event/step/sample/runtime budgets; explicitly reject scripts and unknown executable fields.
- [x] 2.2 Create canonical version-controlled fixtures for 30-second single target, 180-second single target, 60-second three target and 60-second extreme stacking templates with stable identities, versions and body hashes.
- [x] 2.3 Implement the compile-time `ScenarioRegistry` with schema/version resolution, body-hash verification, immutable built-in access and startup checks that every template references only registered v1 events/evaluators.
- [x] 2.4 Add additive migration and repository for insert-only `scenario_definitions`, including scene/version uniqueness, template origin, canonical body/hash, project ownership and indexes; idempotently seed exactly the four built-ins.
- [x] 2.5 Implement clone-as-new-version semantics and service-side parameter overlay validation for declared JSON paths, types, decimal/unit bounds, revision participant references and budgets; never mutate a built-in or referenced definition.
- [x] 2.6 Add unit/property tests for clone lineage, immutable versions, default expansion, parameter order, invalid paths/types/units/bounds, missing/tombstoned participants, unknown events/extensions and byte-stable scene hashes.
- [x] 2.7 Add migration tests for new projects, existing empty/non-empty projects, repeat/open-after-crash seeding, rollback and mandatory #7 backup preflight failure without making Graph or #9 a prerequisite.

## 3. Canonical Input, Admission, and Version Fingerprint

- [x] 3.1 Implement revision source resolution that accepts an explicit immutable revision or resolves a selected release once, verifies same-project ownership and never reads later working/active-release changes.
- [x] 3.2 Implement synchronous admission against #6 exact FULL `ValidationGate`, rejecting LOCAL/working/missing/failed/stale/version-mismatched/ERROR/BLOCK results before a Job is created.
- [x] 3.3 Implement `SimulationInputV1` normalization with all defaults expanded; canonical scene parameters, participants, actions and budgets; ordered Metric identities; sample count defaulting to 1000 for random scenes; and a normalized seed.
- [x] 3.4 Implement the domain-separated canonical encoder and SHA-256 `input_hash`, sorting only unordered data by raw UTF-8 bytes while preserving semantic sequence order and using #6 canonical decimal/Duration representations.
- [x] 3.5 Implement complete implementation fingerprint resolution from revision VersionManifest, scene body, engine/event/time/PRNG, Schema/DSL/evaluator/NumericPolicy, Metric/aggregation/result schemas; reject unregistered, missing or conflicting identities.
- [x] 3.6 Register the simulation VersionContributor so new revisions pin exact simulation identities or explicit unavailable state; preserve older missing manifests as unavailable rather than filling them with current versions.
- [x] 3.7 Add golden/property tests showing equivalent omitted/default inputs have one hash, and any revision/config/scene/parameter/action/budget/sample/seed/Metric/implementation change changes the appropriate identity.
- [x] 3.8 Add concurrency tests showing a release pointer, working state, scene definition or Registry availability change after capture cannot alter an accepted Job's immutable input.

## 4. Deterministic PRNG, Event Queue, and Numeric Execution

- [x] 4.1 Select and freeze the portable fixed-width `simulation-prng-v1` algorithm, constants, seed expansion, sample-ordinal derivation, integer rejection and uniform mapping in source manifests and public fixed-vector golden fixtures.
- [x] 4.2 Implement engine-owned per-sample random streams derived only from PRNG version, normalized seed and sample ordinal; prohibit standard-library default/global RNG, clock seeding, worker identity and hidden pre-consumption.
- [x] 4.3 Define the canonical event DTO and implement a heap comparator ordered exactly by integer event time, rule priority, source stable-ID UTF-8 bytes and sample-local insertion ordinal.
- [x] 4.4 Implement deterministic initial-event creation and monotonic insertion ordinals from scene participant/action order and evaluator-declared derived-event order; detect duplicate complete keys and ordinal overflow as contract failures.
- [x] 4.5 Implement next-event time progression, same-time sequential execution, scene-end boundaries, past-event rejection and stable event/step/zero-time-loop budget checks without fixed-frame approximation or wall-clock input.
- [x] 4.6 Build adapters from #5 FormulaBinding/TriggerRule/Modifier/StackRule structures to #6 typed evaluator contexts, preserving `self/source/target/scenario`, stable references and registered event/evaluator identities.
- [x] 4.7 Ensure all state transitions use #6 decimal128 and Unit Registry types end-to-end, including ratio Percentage and integer-millisecond Duration, with no float crossing engine or Metric interfaces.
- [x] 4.8 Map NaN/non-finite, division by zero, numeric range, unit/scope, missing evaluator and contract-drift failures to stable simulation diagnostics without returning zero, truncation or current-version fallback.
- [x] 4.9 Add event/PRNG/numeric golden tests for all four templates, simultaneous ties, source/row/map shuffles, short-circuit random draws, duration edges, decimal HALF_EVEN/exponent bounds and independent-process replay.
- [x] 4.10 Add fuzz/property tests proving malformed events/payloads/parameters cannot execute dynamic code, panic, move time backwards or run without a budget, and that every accepted event sequence terminates or reports a bounded failure.

## 5. Sample Parallelism, Metrics, and Stable Result Hashes

- [x] 5.1 Implement bounded worker-pool planning for stable sample ordinals `0..sample_count-1`, with independent state/PRNG and immutable `SampleResult` values that never update a shared aggregate on completion.
- [x] 5.2 Define the compile-time Metric Module contract for required observations, sample accumulators, units, result schema, aggregation/confidence assumptions, direction (`higher_is_risk`/`lower_is_risk`/`target_range`) and optional absolute thresholds.
- [x] 5.3 Implement and version independent DPS, healing, survivability, resource and control Metric Modules over structured event/state observations, using only immutable sample inputs and #6 numeric types.
- [x] 5.4 Implement typed `UNAVAILABLE` Metric results with stable code, missing structured inputs/capabilities and readable reason; verify an unavailable module neither emits zero/estimate nor changes other modules.
- [x] 5.5 Freeze each v1 Metric aggregation and confidence-interval algorithm, confidence assumptions, rounding and output canonicalization in manifests/golden fixtures rather than relying on unordered maps or library defaults.
- [x] 5.6 Implement the single deterministic reducer that consumes successful samples by ascending ordinal and Metric IDs by raw UTF-8 bytes, independent of worker count, chunking, completion order and retry order.
- [x] 5.7 Implement canonical result encoding and `result_hash` over complete input fingerprint, Metric values/CI/unavailability, assumptions, warnings and result schema while excluding Job/run IDs, timestamps, progress, worker and machine details.
- [x] 5.8 Add tests across one-to-many workers, randomized completion, chunk sizes, repeated samples and shuffled observations, asserting identical sample hashes, Metric/CI bytes, unavailable ordering and final result hash.
- [x] 5.9 Add negative tests proving a failed/canceled/incomplete sample, Metric contract mismatch, aggregation error or requested-sample count mismatch cannot produce a successful aggregate or result hash.

## 6. Durable Jobs, Runs, Cancellation, and Recovery

- [x] 6.1 Add additive migrations and repositories for insert-only `simulation_runs`, `simulation_metric_results` and `simulation_verifications`, plus mutable `simulation_job_checkpoints`, with project/revision/scene foreign keys, uniqueness and stable history indexes.
- [x] 6.2 Implement Job creation using #7 `(project_uuid, Idempotency-Key)` semantics: same key/canonical input returns the original Job, changed input returns 409 conflict, and automatic policy keys deduplicate the complete input hash.
- [x] 6.3 Implement persisted phases `QUEUED/MATERIALIZED/SAMPLES_RUNNING/AGGREGATING/SEALING/SUCCEEDED` and monotonic Job events containing bounded sample progress, warnings, error/retry metadata and final result links.
- [x] 6.4 Implement ordinal-indexed checkpoint batches containing complete sample accumulators and hashes bound to Job/input/fingerprint/generation; never expose checkpoints through the immutable run resource.
- [x] 6.5 Implement persisted cancellation intent and cooperative checks before scheduling, at bounded engine intervals, before sample checkpoint, before aggregation and inside seal preconditions; stop new work and converge idempotently to `canceled`.
- [x] 6.6 Implement deterministic resource-budget and wall-clock timeout handling with stable `BUDGET_EXCEEDED`/timeout failures, retained diagnostics and no partial success rows.
- [x] 6.7 Implement atomic result sealing that rechecks ownership/input/fingerprint/sample completeness/cancel generation, inserts one run and ordered Metric rows, optionally inserts verification, sets Job result type/ID/URL and prevents duplicate commits.
- [x] 6.8 Implement startup/project-reopen recovery for queued/running/interrupted simulation Jobs: re-resolve the exact fingerprint, verify every checkpoint hash, reuse only complete matching ordinals and run/recompute missing samples under the original Job.
- [x] 6.9 Implement recovery refusal for missing/drifted implementations, source/scene mismatch, corrupt checkpoint or unverifiable state; preserve a stable interrupted/failed diagnostic and never mix versions or guess success.
- [x] 6.10 Implement replay handling when a run committed before the Job terminal update: compare stored input/result hashes and idempotently complete the original Job, or stop on any mismatch.
- [x] 6.11 Implement same-input reproducibility verification via optional `verify_run_id`: require exact source input/fingerprint, create a distinct Job/run, compare server-side hashes and write an insert-only verified/mismatch relation without mutating either run.
- [x] 6.12 Add SQLite fault-injection tests before/after every checkpoint, cancellation and seal write; cover process/project close, repeat recovery, cancel races, dedup races, one-Job/one-run constraints, corruption and immutable historical reads.

## 7. Historical Compatibility, API, and Release Gate

- [x] 7.1 Implement the `SimulationImplementationRegistry` compatibility projection and startup audit, including a v1-major manifest of every evaluator ever permitted to write a project and CI failure on removal or same-version semantic drift.
- [x] 7.2 Implement read-only fallback for stored runs whose engine/evaluator/PRNG/Metric/aggregation implementation is missing, returning original values/CI/assumptions/fingerprint/hash plus explicit non-reproducible reasons.
- [x] 7.3 Extend `api/openapi.yaml` with `POST /api/v1/simulation-jobs`, explicit revision/release source, scene/parameters/Metric/sample/seed/budget/verify target, required `Idempotency-Key`, 202/Location and unified Job/result links.
- [x] 7.4 Define `GET /api/v1/simulation-runs/{id}` as an immutable resource with complete input/result fingerprint, Metric/CI/unavailability, assumptions, hashes, verification refs and read-time stale/reproducibility projection.
- [x] 7.5 Reuse and verify #7 Job GET/SSE/cancel schemas for sample phases, monotonic event ordinals, `Last-Event-ID` resume and polling fallback; keep historical indexing in revision timeline/Job result links rather than adding a parallel write protocol.
- [x] 7.6 Add RFC 9457 Problem Details codes/details for invalid revision/release/scene/parameter/sample/seed/budget, validation required/blocked, implementation/Metric unavailable, idempotency conflict, budget/timeout, recovery mismatch and sanitized internal errors.
- [x] 7.7 Generate Go handlers/bindings and TypeScript clients, wire thin application adapters, enforce project ownership and preconditions, and add drift checks prohibiting handwritten duplicate DTOs.
- [x] 7.8 Add HTTP tests for synchronous non-Job admission failures, 202/Location, same/different idempotency replay, immutable GET, cancel, SSE resume, interrupted recovery, verify target, missing-evaluator read-only behavior and response sanitization.
- [x] 7.9 Register the versioned simulation Gate descriptor/evaluator and match every policy-required scene/Metric/sample/seed/implementation/input/result/reproducibility identity to the exact candidate revision.
- [x] 7.10 Implement required/optional semantics: required missing/unavailable/failed/stale/unreproducible results BLOCK, optional unavailability produces WARNING, and neither user/AI text nor numeric-risk override can turn a simulation deficiency into PASS.
- [x] 7.11 Add Gate contract tests for starter policy scenes and 1000 samples, exact PASS evidence refs, stale revision/scene/version, unavailable required versus optional Metric, missing evaluator and immutable historical result behavior.

## 8. Simulation User Interface

- [x] 8.1 Add `/simulations` navigation/route and generated-client data layer for revision/release, scene, Job and run resources, keeping server state in TanStack Vue Query rather than Pinia or a client simulation model.
- [x] 8.2 Build source/scene selectors and a fixed schema-driven parameter form that permits only declared fields, shows participant/action/initial-state assumptions and preserves user input on authoritative server errors.
- [x] 8.3 Build Metric selection plus sample/seed/budget controls with random-scene default 1000, explicit version/fingerprint summary and client hints that never replace server validation.
- [x] 8.4 Implement submit with one persisted idempotency key per user attempt, 202 Job tracking, SSE ordinal resume and polling fallback across disconnect, refresh, app restart and project reopen.
- [x] 8.5 Build Job progress and cancellation states for queued/running/sample progress/aggregating/sealing/canceling/canceled/interrupted/budget/timeout/retryable/non-retryable outcomes without inferring success from progress.
- [x] 8.6 Build run result panels for each Metric value/unit/CI/sample count/direction/assumptions and typed unavailable reason; never render missing values as zero or merge independent Metric statuses.
- [x] 8.7 Build historical run cards from #7 revision timeline/Job result links and direct immutable run GET, including revision/scene/seed/versions/input/result hashes, stale context and read-only missing-evaluator degradation.
- [x] 8.8 Implement explicit retry and reproducibility verification actions that reconstruct the original canonical options, use a new idempotency key and show verified/hash-mismatch/missing-implementation results without mutating history.
- [x] 8.9 Cover loading, partial refresh, empty/no eligible revision/scene, FULL validation blocked, invalid parameters, Metric unavailable, cancellation, timeout, budget, recovery, stale and missing-evaluator states with typed view models.
- [x] 8.10 Implement keyboard-complete selection/submit/cancel/history/Metric/fingerprint flows, error-summary and terminal-state focus management, aria-live progress, non-color text labels and WCAG 2.1 AA checks at 1024px desktop width.

## 9. Determinism, Recovery, Capacity, and End-to-End Verification

- [x] 9.1 Create one versioned fixed revision/scenario generator covering six entity kinds, all four templates, five Metrics, simultaneous events, bounded triggers/stacks, random draws, decimal/unit boundaries and unavailable structured inputs.
- [x] 9.2 Run repeated golden/property suites across shuffled JSON/map/SQLite order, one-to-many workers, randomized sample completion, independent processes and different chunking, asserting identical input/event/sample/Metric/result bytes and hashes.
- [x] 9.3 Add cancellation and crash matrices at every event/sample/aggregate/seal phase, proving no partial run is visible and recovery produces the same result hash as uninterrupted execution when fingerprints match.
- [x] 9.4 Add compatibility tests that open historical fixtures for every v1 evaluator/engine/PRNG/Metric version, rerun supported fingerprints, and retain read-only details while blocking verify/Gate when one implementation is intentionally absent.
- [x] 9.5 Add frontend component/accessibility tests for parameter errors, SSE/poll recovery, progress/cancel, unavailable-not-zero, CI/assumption text, stale history, fingerprint display, verify outcomes, keyboard focus and non-color status.
- [ ] 9.6 Add E2E flow: select a FULL-valid revision, clone/use a fixed template, run default 1000 samples, inspect all available/unavailable Metric details, restart, retrieve history and produce a distinct same-hash verification run.
- [ ] 9.7 Add E2E failure flow for validation block, invalid scene parameter, cancellation, event budget exceed, process interruption/recovery, missing evaluator read-only fallback and simulation Gate required/optional decisions.
- [ ] 9.8 Run the reference 4-core/16-GB/NVMe Windows 10/11 x64 capacity fixture with default 1000 samples, bounded workers/memory/events/payload/checkpoints, observable cancellation and the under-30-second target; retain diagnostics without weakening semantics on failure.
- [ ] 9.9 Run repository unit/integration/E2E, lint, fuzz smoke, generated-contract, migration, deterministic golden and compatibility-retention suites plus `openspec validate reproducible-simulation --strict`; require zero failures before marking implementation complete.
