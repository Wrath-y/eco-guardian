## 1. Dependency audit and contract foundation

- [x] 1.1 Verify the applied #5 revision reader exposes immutable, complete, stably ordered entity manifests and config/version identity, and #6 exposes exact FULL validation plus canonical AST/index artifacts; record that trigger/stack safety remains certified by the PASS FULL result identity/hash rather than depending on its transient scanner, and stop rather than create parallel revision or validation models when either required boundary is absent.
- [x] 1.2 Create the transport-neutral `internal/rules/materialization` contract package with `RuleSetV1`, semantic manifest, provenance, typed FormulaBinding/TriggerRule/Modifier/StackRule DTOs, stable diagnostic codes, and a revision-only reader port that does not import storage, HTTP, working state, Graph, AI, or simulation state.
- [x] 1.3 Define v1 stable rule IDs from revision/source entity/field path/ordinal/kind; add validation for domain IDs, canonical JSON-pointer paths, spans, typed enums, canonical decimal strings, duration integers, and non-partial RuleSet invariants.
- [x] 1.4 Implement the domain-separated canonical encoder and SHA-256 materialization hash, including revision/config identity, semantic version manifest, typed graph, canonical AST identity, and provenance while excluding request/run/database IDs, wall-clock values, display fields, and worker scheduling.

## 2. Immutable typed graph construction

- [x] 2.1 Implement known-v1-schema traversal over an immutable revision manifest, retaining declared array order and producing total output order by raw UTF-8 source entity ID, canonical field path, ordinal, then rule kind without map-iteration dependence.
- [x] 2.2 Materialize FormulaBinding values from existing validated FormulaIndex artifacts, including output attribute, canonical AST bytes/hash/version, selector reads, source expression span, and stable source provenance; fail deterministically on missing, duplicate, mismatched, or malformed artifacts.
- [x] 2.3 Materialize TriggerRule values with typed event, condition/target selector, ordered effect targets, resolved entity identities, and declared termination-budget fields; bind the RuleSet to its PASS FULL validation result identity/hash instead of duplicating transient safety analysis, and reject unknown enum values and dangling/wrong-kind references.
- [x] 2.4 Materialize Modifier and StackRule values with only declared v1 typed operation, target/value or FormulaBinding identity, priority, duration, cap, max-stacks, refresh-policy, and owning-rule relationships; reject unsupported known shapes instead of inferring defaults.
- [x] 2.5 Exclude unknown extensions and unsupported future fields from the typed graph while preserving their existing opaque payload behavior; ensure no materializer code evaluates a formula, schedules an event, or mutates a revision.

## 3. Validation admission and derived-artifact lifecycle

- [x] 3.1 Implement an application adapter that resolves exactly one immutable revision and requires a matching PASS FULL validation result with exact config hash and Schema/DSL/AST/Registry/NumericPolicy versions before invoking the pure materializer.
- [x] 3.2 Map missing, stale, blocked, ERROR, version-drift, unavailable-artifact, malformed-provenance, and integrity failures to stable transport-neutral materialization diagnostics containing safe source entity/field-path context where applicable.
- [x] 3.3 Add an optional revision/config/version-keyed derived RuleSet repository or verified in-memory cache only after the pure builder is complete; atomically reject corrupted entries and rebuild them solely from immutable source artifacts without writing revision or validation facts.
- [x] 3.4 Wire the composition root so deterministic consumers can resolve `RuleSetV1` through the new port, and add compile-time dependency checks preventing simulation adapters from accepting raw entity payloads or working-state readers for rule evaluation.

## 4. Determinism, compatibility, and integration tests

- [x] 4.1 Add canonical fixtures covering every v1 rule kind, multiple entities, nested/array rule fields, selector reads, cross-entity references, spans, trigger budgets, modifiers, stack rules, and FULL-validation certification; assert typed graph bytes, rule IDs, provenance, and materialization hash goldens.
- [x] 4.2 Add property/fuzz tests that permute map/database row order and repeat materialization across processes while preserving semantic array order; assert byte-identical RuleSets and hashes with no panics or unbounded traversal.
- [x] 4.3 Add negative contract tests for working-state attempts, missing/stale/blocked/mismatched FULL validation, absent or duplicate AST/index artifacts, malformed canonical quantities/spans, unknown v1 enums, dangling references, and unknown extensions.
- [x] 4.4 Add cache/rebuild integrity tests proving a missing or corrupted derived artifact rebuilds identically and cannot modify immutable revisions, validation results, or historical simulation facts.
- [x] 4.5 Add a simulation-facing contract test proving a stateful evaluator adapter receives only an exact `RuleSetV1` and records its materialization hash in the simulation input fingerprint, without implementing a parallel JSON decoder.
- [x] 4.6 Run focused Go tests, `go test ./...`, strict OpenSpec validation for this change and `reproducible-simulation`, and `git diff --check`; record any Windows capacity/E2E requirement that remains intentionally owned by the simulation change.
