## 1. Applied Baseline Verification and Risk Boundaries

- [x] 1.1 Verify the applied #6 baseline exposes exact revision/config/version FULL `ValidationGate`, immutable validation runs/issues, canonical typed AST/formula/stack indexes, decimal128/NumericPolicy, and stable `STATIC_FORMULA_CYCLE`, `EVENT_LOOP_UNBOUNDED`, `STACK_UNBOUNDED` evidence; stop with a dependency report rather than duplicating any missing boundary.
- [x] 1.2 Verify the applied #7 baseline exposes immutable revision/current-release/release-policy readers, canonical diff, VersionManifest/VersionContributor, Gate Registry statuses/evidence/override classification, `JobStore`/`EventStore`/SSE/cancel, idempotency, release confirmation audit and migration ledger; stop rather than create parallel version, Job, Gate or confirmation infrastructure.
- [x] 1.3 Verify the applied #10 baseline exposes immutable successful simulation runs, participant/scene/sample/seed/input/result identities, implementation fingerprint/reproducibility, ordered Metric results, aggregate/unit/direction/target range/absolute threshold/CI/assumptions/UNAVAILABLE reason, and Gate-compatible evidence refs; document and stop on any absent field required by the spec.
- [x] 1.4 Verify #9, when installed, exposes immutable impact report/path/suspected evidence identities with revision pair, Graph Snapshot/hash, deterministic/suspected classification and freshness; keep the adapter optional and prove #11 builds and calculates identically without it.
- [x] 1.5 Create `internal/risk/{contract,threshold,cohort,comparison,structure,orchestration,gate}` package boundaries plus storage, clock/ID, bounded executor and optional impact-evidence ports; add architecture tests preventing pure risk packages from importing Gin, SQLite, Vue, Graph/local-rag, AI/provider or release-worker implementations.
- [x] 1.6 Add composition-root registration for risk Registry manifests, worker lifecycle, VersionContributor and Gate descriptor while keeping #6/#7/#10 buildable and release capability explicitly disabled when risk is absent or incompatible.

## 2. Canonical Contracts and Versioned Registries

- [x] 2.1 Define validated tagged domain types for baseline/`NO_BASELINE`, policy role, comparison status, severity, Metric direction, threshold selection/scope, cohort subject, run/Metric evidence, structure evidence, override classification, report item and Gate result; reject incomplete identities and illegal state/severity combinations.
- [x] 2.2 Define `RiskInputV1` with candidate/baseline revision/config/VersionManifest, policy, threshold, ordered subjects/scenes/runs/Metrics, validation/implementation identities and rule versions, expanding all defaults before hashing.
- [x] 2.3 Implement domain-separated canonical JSON codecs and SHA-256 identities for threshold body, risk input, calculation items, optional explanation-evidence manifest and report; exclude Job/time/UI/worker/human text/Graph availability from deterministic calculation hash.
- [x] 2.4 Implement compile-time Threshold, Comparison, Cohort, StructuralRule and ReportSchema registries with stable IDs/versions/source manifests; fail startup on duplicate identities, version/hash conflicts, invalid severity/override policy, incompatible unit/direction or missing golden fixture.
- [x] 2.5 Register the v1 comparison manifest for `higher_is_risk`, `lower_is_risk`, inclusive `target_range`, relative/absolute boundary semantics and status ordering, and freeze canonical input/item/report fixtures.
- [x] 2.6 Add property tests proving map, database, policy input, Metric and cohort enumeration order cannot alter canonical bytes, identities, ordered items or calculation hash, while a semantic identity/version/value change always alters the appropriate hash.

## 3. OpenAPI and Error Contract

- [x] 3.1 Extend `api/openapi.yaml` with tagged `evaluate` and `record_numeric_decision` commands for `POST /api/v1/risk-reviews`: evaluation includes threshold selection, explicit baseline/`NO_BASELINE`, candidate/policy and ordered simulation refs; decision includes source calculation/hash, eligible item identities and non-empty reason; require `Idempotency-Key`, 202/Location and unified Job result links.
- [x] 3.2 Define `GET /api/v1/risk-reviews/{id}` schemas for immutable input/report identities, threshold projection, cohort, Metric values/units/CI or unavailable reason, signed/relative deltas, comparison status, BLOCK/WARNING/INFO, rule/evidence/assumptions, structural items, override classification, hashes and read-time freshness/Gate/release-audit links.
- [x] 3.3 Reuse and, only where generically missing, extend #7 Job GET/SSE/cancel DTOs for risk phases, progress, warnings, event ordinals, cancellation and `result_type/result_id/result_url`; do not add a second Job protocol.
- [x] 3.4 Freeze RFC 9457 Problem Details codes/statuses for revision/baseline/policy/threshold/validation/simulation/cohort invalid, required Metric unavailable, stale identity, Registry/rule contract, idempotency conflict, canceled/interrupted/recovery mismatch and storage failure.
- [x] 3.5 Generate Go bindings and TypeScript client, add request/response/error fixtures, and make drift checks fail if hand-written DTOs or generated contracts diverge.
- [x] 3.6 Add contract tests proving unknown fields follow compatibility policy while malformed tagged unions, noncanonical numeric values, missing identities, client-claimed severity/PASS and raw override attempts are rejected without leaking SQL, stack, secret or local paths.

## 4. Immutable Threshold Versions and Starter Activation

- [x] 4.1 Implement the canonical inactive starter fixture with relative WARNING `0.10`, BLOCK `0.25`, explicit source/assumptions and no guessed absolute values; add exact body/hash golden tests.
- [x] 4.2 Implement threshold version creation with UUIDv7/display version, canonical body/hash, enabled state, scene/Metric/nullable balance-group entries, direction/absolute boundaries, structural rule versions and immutable audit fields.
- [x] 4.3 Implement the idempotent POST threshold-selection flow so an existing enabled version is resolved exactly, or an explicitly confirmed starter/modified-starter selection creates one immutable enabled version before `RiskInputV1` is fixed; retries MUST reuse the same version.
- [x] 4.4 Implement deterministic scope resolution: exact scene/Metric/group first, then the null-group project default; singleton subjects use only the null-group entry; reject same-priority conflicts, incompatible Metric versions/directions/units and absent matches.
- [x] 4.5 Return `THRESHOLD_NOT_CONFIGURED/INVALID` with resolution evidence when no enabled applicable version exists, and prove no request, Gate or UI path silently activates or applies the inactive starter.
- [x] 4.6 Add repository/application tests for explicit first enable, modified template, same-key replay, changed-key conflict, new version on every semantic change, immutable historical references and exact 10%/25% decimal preservation.

## 5. Cohort Resolution and Simulation Admission

- [x] 5.1 Implement candidate/baseline subject materialization from immutable entity envelopes and scene participant identities, preserving kind, optional `balance_group`, stable IDs and canonical ordering without reading working state.
- [x] 5.2 Resolve non-empty `balance_group` to same-kind revision-scoped participant sets and ungrouped entities to same-stable-ID singletons; return explicit missing/member/kind/group statuses instead of name, tag, Graph or similarity matching.
- [x] 5.3 Validate that each bound candidate/baseline simulation run participant set exactly closes over its resolved cohort or singleton and uses the same scenario/Metric comparison contract; reject partial, extra or unprovable participant mappings.
- [x] 5.4 Implement admission matching for revision/config/VersionManifest, policy scene/Metric role, scene version, sample count, seed policy, engine/evaluator/NumericPolicy/Metric/aggregation versions, input/result hashes, success and reproducibility.
- [x] 5.5 Implement explicit `NO_BASELINE` admission that requires candidate required runs but never resolves or fabricates baseline runs/deltas, and subsequent-release admission that fixes the current active release exactly.
- [x] 5.6 Add cohort golden/property tests for member shuffle, additions/deletions, group/kind changes, missing baseline stable ID, checkpoint same-content revisions, scene participant mismatch and identical results with/without optional #9 capability.

## 6. Deterministic Metric Comparison and Status Semantics

- [x] 6.1 Implement signed raw delta and decimal `risk_delta` transforms for higher/lower directions and inclusive target-range distance, using only #6 canonical decimal/unit operations and never float or display rounding.
- [x] 6.2 Implement nonzero-baseline relative risk as `risk_delta / abs(baseline)` and the zero-baseline branch that never divides or emits a relative percentage, Infinity, estimate or safe fallback.
- [x] 6.3 Implement BLOCK-first `>=` boundary evaluation so relative 10% is WARNING, 25% is BLOCK and lower values are INFO; apply the same exact equality semantics to compatible absolute WARNING/BLOCK boundaries.
- [x] 6.4 Pass candidate/baseline Metric CI and assumptions through as immutable evidence while comparing only canonical aggregate values; prove CI width/format and provider state cannot probabilistically change severity.
- [x] 6.5 Emit tagged `COMPARABLE`, `NOT_COMPARABLE`, `UNAVAILABLE` and `STALE` items separately from severity, including stable reason, Metric/unit/value/threshold/run evidence, and never encode “not calculated” as INFO or zero.
- [x] 6.6 Apply policy roles so required NOT_COMPARABLE/UNAVAILABLE/stale/missing inputs block, optional NOT_COMPARABLE/UNAVAILABLE produces visible WARNING, and independent comparable Metrics retain their exact results.
- [x] 6.7 Add golden tests for positive/negative values, both risk directions, movement opposite risk, target-range inside/outside/crossing, 9.999/10/24.999/25 percent, zero baseline with/without absolute thresholds, absolute exact boundaries, decimal exponent/rounding edges and unit mismatch.

## 7. Structural Risk and Evidence Isolation

- [x] 7.1 Implement the #6 validation evidence adapter that accepts only exact candidate FULL run/result/version matches and maps static formula cycle, unbounded event and unbounded stack issues to their original immutable fingerprints and non-overridable BLOCK classification.
- [x] 7.2 Implement `StructuralRiskRegistryV1` new-multiplier detection over #7 canonical diff plus #6 typed AST/stack indexes, saving rule/version, entity/path/ordinal and canonical before/after evidence.
- [x] 7.3 Implement bounded repeated-multiplication detection for the same canonical output/target and multiplier source, comparing candidate chain counts to baseline and applying only the rule-declared limit/severity.
- [x] 7.4 Add stable ordering/hash and version compatibility checks for structural items; unknown AST/index/rule versions SHALL be unavailable rather than parsed with current logic.
- [x] 7.5 Implement the optional #9 adapter to attach only exact revision-pair/Snapshot/hash-matched deterministic or suspected evidence refs, labeling classification/freshness and keeping them outside calculation/severity hash.
- [x] 7.6 Add tests proving Graph paths, suspected confidence, provider degradation, AI/user text and index availability cannot create a structural match, alter severity, change override eligibility or satisfy Gate.
- [x] 7.7 Add structural fixtures for new Multiply layers, unchanged/moved nodes, repeated source chains, rule limit boundaries, all three #6 non-overridable codes and natural-language descriptions that MUST NOT be treated as structure facts.

## 8. SQLite Migration and Immutable Repositories

- [x] 8.1 Add a checksummed additive project migration for insert-only `threshold_versions`, `risk_reviews` and `risk_items`, reusing #7 `jobs`/`job_events`, foreign keys and canonical hash columns without modifying revision, validation, simulation, impact or release facts.
- [x] 8.2 Add unique/index constraints for threshold display/body identities, Job-to-report, calculation/decision kind and source-report FK, report item ordinal/identity, candidate/baseline/policy/threshold history and stable current-context lookup; seed the inactive starter fixture idempotently.
- [x] 8.3 Add repository/SQLite triggers that reject update/delete of threshold/report/item facts and prove report rows reference rather than copy revision manifests, simulation sample/event data, Graph path bodies or release configuration.
- [x] 8.4 Implement short atomic report sealing that verifies Job ownership/input/cancel generation and complete ordered items, inserts one review/items set, records calculation/evidence hashes and transitions Job result once; partial rows MUST remain invisible on rollback.
- [x] 8.5 Implement bounded history/detail reads and read-time freshness/compatibility projections without updating historical rows or hashes; preserve stored reports when referenced Registry implementations are currently missing.
- [x] 8.6 Add migration replay, starter deduplication, immutability, foreign-key, hash corruption, seal rollback, one-Job/one-report, concurrent read and project reopen tests.
- [x] 8.7 Integrate #7 migration preflight and mandatory compatible backup/integrity boundary for existing projects; refuse schema writes when unavailable and prove failure preserves the pre-migration database.

## 9. Risk Job Orchestration, Idempotency, Cancellation, and Recovery

- [x] 9.1 Implement the application admission flow `materialize → validate policy/threshold/runs/cohort → canonicalize/hash → idempotency lookup/create`, returning no Job for deterministic preflight errors.
- [x] 9.2 Implement bounded worker phases `MATERIALIZED`, `COMPARING`, `STRUCTURE_CHECKING`, `SEALING` with #7 queued/running/succeeded/failed/canceled/interrupted states, monotonic events, progress and safe diagnostic errors.
- [x] 9.3 Implement project-scoped idempotency so same key plus canonical input returns the original Job/report and same key plus any changed candidate/baseline/policy/threshold/run/evidence-selection input returns 409 without side effects.
- [x] 9.4 Persist cancellation intent and check it before each scene/Metric/cohort/rule unit and inside seal; repeated cancellation is idempotent and no partial items/report/result hash can be exposed as success.
- [x] 9.5 Implement startup/project-reopen recovery by rematerializing every immutable identity and Registry version, rerunning the entire bounded calculation only on an exact match, and failing with recovery mismatch rather than mixing a changed baseline, threshold, run or implementation.
- [x] 9.6 Reconcile the crash window where report sealing committed before Job success by validating the unique report/input/calculation hash and completing the original Job result link without creating a second report.
- [x] 9.7 Add fault-injection tests before/after every phase and seal statement, active-release/threshold/implementation changes during interruption, cancellation races, storage failure, same-input restart and duplicate worker delivery.

## 10. Immutable Report Retrieval and Risk Gate

- [x] 10.1 Build report summaries/items with all required candidate/baseline/NO_BASELINE, policy/threshold, scene/run/Metric/cohort, values/units/CI or missing reason, signed/relative deltas, statuses, severities, rules, evidence, assumptions, structure, override classification and hashes.
- [x] 10.2 Implement GET read-time projections for current active baseline, policy/threshold/Registry compatibility, simulation availability, optional impact evidence and linked #7 release override audit without mutating stored report facts.
- [x] 10.3 Register a versioned risk VersionContributor and Gate descriptor declaring exact candidate/baseline/policy/threshold/simulation/report/rule inputs, result schema, evidence shape and numeric-only override classification.
- [x] 10.4 Implement Gate evaluation for later releases: PASS only on exact current-baseline, enabled-threshold, matching required inputs and no BLOCK; preserve ordinary/optional WARNING, and return BLOCK/UNAVAILABLE/STALE with item refs for required mismatch, NOT_COMPARABLE, UNAVAILABLE or structural errors.
- [x] 10.5 Implement first-release Gate semantics requiring a `NO_BASELINE` report, all required simulation/Metric/threshold/structure facts and #7 establish-baseline confirmation, without fabricating comparison items or a low-risk result.
- [x] 10.6 Implement numeric decision creation so eligible items remain BLOCK with `overridable=true`, source calculation/item hashes are revalidated and non-empty reason is saved in a new immutable decision report; let #7 validate the decision plus second confirmation and link its release audit, while structural/validation/missing/stale/policy/threshold/backup blocks never expose eligibility.
- [x] 10.7 Add Gate contract tests for exact PASS, ordinary WARNING, optional unavailable, required unavailable/NOT_COMPARABLE, threshold disabled, baseline changed, run/version/report stale, NO_BASELINE confirmed/unconfirmed, eligible numeric override and every non-overridable class.

## 11. HTTP Handlers, Events, and Security Boundaries

- [x] 11.1 Implement generated-DTO application adapters for risk POST/GET, validating headers/tagged unions and returning 202/Location or immutable details with no handler-side calculation, SQL or Gate logic.
- [x] 11.2 Wire risk Jobs into shared Job GET/SSE/cancel with persisted event ordinals, `Last-Event-ID` resume and polling fallback; prove refresh/reconnect never creates a new Job or infers success from progress.
- [x] 11.3 Map all admission, worker, idempotency, cancellation, recovery and not-found failures to frozen RFC 9457 codes, retryability and safe details with request correlation.
- [x] 11.4 Enforce bounded request/result sizes, project ownership, loopback/runtime policies inherited from the application, canonical decimal validation and rejection of client-supplied severity/Gate/override-PASS fields.
- [x] 11.5 Add HTTP integration tests for evaluate and numeric-decision commands, existing/starter/modified threshold selections, exact current baseline, explicit NO_BASELINE, source/hash/item/reason validation, 202 replay/conflict, result links, immutable GET, stale projection, cancellation, SSE resume, malformed/foreign identities and sanitization.

## 12. Risk Review and Release Handoff UI

- [x] 12.1 Add `/risk-reviews` route and `web/src/features/risk-reviews` using only the generated client and TanStack Vue Query server resources; keep report/Gate facts out of Pinia and client calculations.
- [x] 12.2 Build threshold status/selection UI that distinguishes inactive starter, explicit enable, modified starter and existing enabled version, displays source/identity/10%/25%/absolute boundaries, and never labels an unconfigured project safe.
- [x] 12.3 Build candidate/current-release or NO_BASELINE, policy and scene/Metric selection with required/optional roles, exact run identities and preflight reasons; prevent submission of stale/unmatched selections while preserving user inputs.
- [x] 12.4 Build comparison rows that separately display status and BLOCK/WARNING/INFO, candidate/baseline value/unit/CI or missing reason, signed/relative delta, threshold boundary, direction, cohort and assumptions without rendering unavailable/NOT_COMPARABLE as zero.
- [x] 12.5 Build cohort and structural evidence drawers with stable member lists, #6 issue links, new/repeated multiplication evidence, optional deterministic/suspected Graph labels and text alternatives; Graph absence/degradation MUST leave severity display unchanged.
- [x] 12.6 Build Job progress/cancel/retry/history and stale/read-only states for loading, empty, local refresh, NO_BASELINE, threshold unconfigured, UNAVAILABLE, NOT_COMPARABLE, queued/running/canceled/interrupted/failed, SSE fallback and missing implementation.
- [x] 12.7 Build numeric BLOCK reason input that creates an immutable decision report, then hand it to #7 release flow for the separate second confirmation while preserving the server BLOCK; hide/disable controls for non-overridable classes and show decision/release audit links when present.
- [x] 12.8 Add `/versions/:id/diff` risk summary/result link using the same server report/Gate projection; do not duplicate risk or release calculations in the version page.
- [x] 12.9 Add keyboard/focus/aria-live behavior, textual status and boundary alternatives, WCAG 2.1 AA contrast and 1024px desktop layout tests for threshold enable, comparison navigation, evidence expansion, errors, terminal Job changes and confirmation.

## 13. Determinism, Workflow, Capacity, and Release Verification

- [x] 13.1 Run all canonicalization, Registry, threshold, cohort, comparison, structural, repository, Job, HTTP, Gate and Vue unit/integration/property suites with race detection where applicable.
- [x] 13.2 Add a Playwright first-release flow: explicitly enable/modify starter threshold, run required candidate simulations, create `NO_BASELINE` report, confirm establish baseline and verify the resulting release audit retains NO_BASELINE semantics.
- [x] 13.3 Add a Playwright subsequent-release flow: compare against current release, inspect exact 10%/25% and cohort/CI evidence, then adjust configuration or submit an eligible numeric reason/second confirmation while preserving original BLOCK.
- [x] 13.4 Add E2E failure flows for required zero-baseline NOT_COMPARABLE, optional Metric unavailable, stale simulation/threshold/baseline, static formula cycle/unbounded rule, Graph evidence unavailable, Job cancel/restart and non-color keyboard operation.
- [x] 13.5 Run fixed fixtures under shuffled map/SQLite/cohort/Metric order and independent processes, proving identical statuses, item ordering, severity, evidence and calculation/report hashes; prove optional Graph/provider/user text changes do not alter them.
- [x] 13.6 Verify capacity remains bounded at the technical-plan maximum project/revision scale and policy scene/Metric/cohort limits, recording comparison/rule timings, memory, payload sizes, cancellation latency and SQLite lock duration without dropping Metrics or weakening decimal/rule semantics.
- [x] 13.7 Verify migration/rollback, project reopen, missing historical risk implementation, inactive starter, immutable history and release-disabled reasons on supported Windows/local project fixtures.
- [x] 13.8 Run formatting, lint, generated-code/OpenAPI drift, strict OpenSpec validation and all unit/integration/E2E/race suites; record fixture/Registry/tool versions and evidence required for implementation handoff.
