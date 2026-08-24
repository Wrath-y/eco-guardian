## Why

`config-rule-validation` verifies FormulaBinding, TriggerRule, Modifier, and StackRule by scanning generic entity JSON and leaves only validation-oriented indexes. `reproducible-simulation` therefore cannot consume the exact immutable, validated rules from a configuration revision without reparsing JSON or inventing a parallel rule model, which would break the single-source-of-truth and reproducibility guarantees.

## What Changes

- Add a revision-scoped materialization boundary that converts validated v1 entity payloads into immutable typed rule graphs for FormulaBinding, TriggerRule, Modifier, and StackRule.
- Canonicalize ordering, stable rule identities, field provenance, referenced entity identities, and version/hash metadata so a simulation input can prove exactly which rules it evaluated.
- Expose transport-neutral read ports and structured diagnostics for consumers; materialization must never read working state, silently skip unknown fields, or execute formulas.
- Integrate the existing formula parser/AST indexes and exact FULL-validation certification, decoding only known structural rule fields from the immutable manifest and rejecting revision/hash/version mismatches rather than reparsing or weakening validation.
- Add fixtures and contract tests for deterministic materialization, malformed or unsupported payloads, provenance, and missing referenced entities.

## Capabilities

### New Capabilities

- `typed-rule-materialization`: Materialize an immutable configuration revision into deterministic, versioned typed rule DTOs and diagnostics for deterministic consumers.

### Modified Capabilities

- None.

## Impact

- Adds a narrow domain-facing package and revision reader adapter, expected under `internal/rules` (or an equivalently isolated transport-neutral boundary), with SQLite/application composition and fixtures.
- Consumes existing `internal/domain` revision manifests, `internal/validation` formula indexes and exact FULL-validation results, and `internal/formula` AST/evaluator contracts; it does not change their validation or numeric semantics.
- Becomes a hard implementation prerequisite for simulation's stateful rule adapters and its remaining evaluator coverage. It does not add HTTP endpoints, mutable rule editing, Graph behavior, AI behavior, or a generic combat engine.
