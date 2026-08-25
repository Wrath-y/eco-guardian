# Dependency verification resolution

## Status

The task 1.3 dependency failure is resolved. `metric-resource@v1` now declares
the product-approved inclusive canonical ratio target range `[0.8, 1.2]`, and
the #10 simulation result contract seals comparison metadata needed by risk.

## Verified prerequisites

- #6 exposes an exact revision/config/version FULL `ValidationGate`, immutable
  validation runs and issues, revision-scoped compiled AST/formula and typed
  rule materializations, decimal128 `NumericPolicy`, and stable immutable
  evidence for `STATIC_FORMULA_CYCLE`, `EVENT_LOOP_UNBOUNDED`, and
  `STACK_UNBOUNDED`.
- #7 exposes immutable revision, active-release, release-policy and canonical
  diff readers; `VersionManifest`/`VersionContributor`; Gate descriptor/result
  states, evidence, and numeric override classification; the shared durable
  Job/Event/SSE/cancel protocol; project-scoped idempotency; release
  confirmations/audit; and the checksummed migration ledger.
- Focused tests passed: 269 tests across validation, formula, typed-rule
  materialization, versioning, shared Job, simulation contract/Metric, and
  SQLite packages.

## Resolved #10 contract facts

1. `metric.Descriptor` requires one ordered inclusive `ValueRange` whenever
   direction is `target_range`, and rejects range metadata for other
   directions.
2. `metric-resource@v1` declares lower `0.8`, upper `1.2`, bounds `inclusive`.
3. `metric.CanonicalMetric`, immutable `simulation_metric_results` canonical
   JSON, the HTTP projection, OpenAPI and generated TypeScript expose
   `target_range` and nullable `absolute_threshold`.
4. Target-range changes alter the canonical simulation `result_hash`; optional
   absolute thresholds remain explicitly `null` until a Metric Module declares
   a product-approved canonical value.

Focused simulation Metric, HTTP and SQLite suites passed after the change. Risk
continues to own relative WARNING/BLOCK thresholds and any explicitly selected
absolute WARNING/BLOCK boundaries; it consumes rather than reconstructs #10
Metric direction/range metadata.
