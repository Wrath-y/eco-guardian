## Context

v1 domain payloads define FormulaBinding, TriggerRule, Modifier, and StackRule, while the applied validation layer parses formulas into revision-scoped index records and scans rule safety through generic JSON maps. Those records are appropriate for validation but do not give a consumer a frozen typed graph, resolved references, or a single provenance model. The simulation engine currently accepts an event callback only; implementing stateful formulas and effects directly from payload JSON would duplicate #5/#6 semantics and make historical replay depend on incidental JSON traversal.

This change is the narrow bridge between immutable revision materialization and deterministic consumers. It consumes the existing revision manifest, FULL validation result, Schema/DSL/Registry/NumericPolicy versions, and canonical AST indexes. Trigger/stack safety analysis is intentionally not a materializer input: the current safety scanner is transient validation implementation detail, so a matching PASS FULL result certifies those rules without exposing or duplicating the scanner. The materializer neither changes a payload nor creates a revision, and it has no HTTP or UI surface.

## Goals / Non-Goals

**Goals:**

- Produce a complete immutable typed rule set for exactly one config revision and its recorded semantic versions.
- Give every materialized item a stable identity, canonical field provenance, source/target entity identity, ordinal, and canonical bytes/hash.
- Reuse #6 parser, AST, numeric, reference, and safety conclusions; require an exact successful FULL validation gate before materialization.
- Define a transport-neutral port usable by simulation and future deterministic readers without an import of SQLite, HTTP, working state, or Graph/AI modules.
- Make all rejection paths structured, deterministic, and safe to expose as downstream diagnostics.

**Non-Goals:**

- Re-parse arbitrary JSON, reimplement DSL/type/unit validation, execute formulas, or infer unmodelled game semantics.
- Change v1 payload Schema, validation severities, revision identity, OpenAPI DTOs, or add editing endpoints.
- Materialize unknown `extensions`, working drafts, invalid/blocked revisions, or a mutable active-release pointer.
- Deliver the simulation state engine, metrics, release gate behavior, or a general rules runtime.

## Decisions

### 1. Add a rule-materialization package behind a narrow revision port

Introduce `internal/rules/materialization` (with a small transport-neutral contract subpackage if required to avoid import cycles). The public reader takes an explicit immutable revision ID and returns `RuleSetV1`; application/SQLite adapters own manifest retrieval, exact validation lookup, and artifact persistence/rebuild. `internal/simulation` consumes only this contract.

`RuleSetV1` includes the project/revision/config hash and exact Schema/DSL/AST/Registry/NumericPolicy versions, plus ordered `FormulaBinding`, `TriggerRule`, `Modifier`, and `StackRule` values. Each item contains a stable materialized-rule ID derived from `(revision ID, source entity ID, canonical field path, ordinal, rule kind)`, source entity kind/ID, applicable target/reference identities, source span where relevant, canonical body bytes/hash, and validation artifact hashes. Typed fields use domain IDs, enums, canonical decimal strings, integer milliseconds, and the existing AST canonical representation; they never expose `map[string]any` as a behavior input.

The package is selected over adding typed simulation structures because it is shared semantic input, not event-engine state. It also prevents simulation from importing validation's storage details. A direct payload reader is rejected because JSON object iteration and best-effort decoding would create an unversioned second rule model.

### 2. Admit only an exact successful FULL validation result

The adapter first resolves the immutable revision manifest and config hash, then requires the matching FULL validation result and version manifest. It accepts only PASS with exact source/config/Schema/DSL/Registry/NumericPolicy compatibility. Missing, stale, blocked, ERROR, or version-drift results return a stable materialization diagnostic; they never yield a partial RuleSet.

For a valid revision, materialization consumes the parser's canonical AST/formula index and decodes only known v1 structural rule fields from the same immutable manifest. Trigger/stack safety remains certified by the matching FULL result identity/hash rather than an unavailable transient evidence port. The materializer must reject a missing, duplicated, mismatched, or unsupported AST artifact rather than reparsing an expression or treating it as empty. This preserves the existing FULL gate as the sole authority for semantic validity. Re-running validation is not chosen because it would blur the read boundary and add execution cost; consumers first request validation through established flows.

### 3. Canonicalize typed graphs and provenance before hashing

The materializer walks only v1 known fields in the immutable entity set, assigns array ordinals from the payload definition, and sorts materialized output by raw UTF-8 source entity ID, field path, ordinal, then rule kind. Rule arrays retain semantic payload order; all derived collections use this total order. Entity IDs, referenced IDs, selector scope, event, stack operation, priority, caps, and budgets are represented in fixed typed fields.

It writes canonical JSON with a `typed-rule-materialization-v1` domain separator and hashes it with SHA-256. The materialization hash includes source revision/config identity, semantic version manifest, canonical AST hash/bytes, typed graph, and provenance, but excludes request ID, wall clock, worker count, database row ID, and display name. Consumers store this hash in their own input fingerprint rather than assuming that a config hash alone identifies evaluation semantics.

The alternative of hashing the original entity blobs would not demonstrate which known behavior was selected or protect consumers from decoder changes. The alternative of materializing by map iteration is rejected because it is not reproducible.

### 4. Explicitly model source, scope, references, and bounded safety data

Formula bindings refer to an existing immutable AST, output attribute, resolved selector reads, formula span, and output provenance. Trigger rules carry source event, condition/target selector, ordered effect targets, and their declared termination budget fields. Modifiers carry operation, target attribute, value/formula binding reference, priority and duration where the v1 payload declares them. Stack rules carry operation, priority, max stacks, refresh policy, cap, and the owning effect/rule relationship. The RuleSet records the matching FULL validation result identity/hash as its safety certification; it does not attempt to serialize transient validation traversal evidence.

Every cross-entity target must resolve to an active entity in the same revision with the expected kind. An unknown v1 enum, invalid canonical numeric string, missing AST/index item, dangling reference, invalid span, or unsupported shape is a deterministic failure code. Unknown `extensions` remain forward-preserved opaque data and do not enter the graph. Materialization carries FULL-validation certification, but it does not calculate loops or stack bounds itself.

This model gives a simulation adapter enough information to map one rule to state/evaluator actions without source JSON access, while retaining a path back to user-visible configuration. It intentionally does not expose runtime state or allow rule mutation.

### 5. Make artifacts rebuildable and lifecycle-safe

The typed RuleSet is a revision-scoped derived artifact: source payload, revision manifest, and validation artifacts remain facts. An adapter may cache canonical bytes and hash keyed by revision/config/version manifest, but cache misses and corruption rebuild from the same immutable inputs; replacement must be atomic and cannot alter revision rows or stored validation results. A consumer must validate the returned revision/config/version/hash against its request before use.

No independent database migration is required for a first in-process materializer. If a later cache table is introduced, it is additive, stores only canonical artifacts and integrity hashes, and is recoverable by deletion/rebuild. This keeps backup/restore behavior unchanged.

## Risks / Trade-offs

- [Existing payload shapes contain optional or poorly normalized rule fields] → Decode only known v1 structures and reject unsupported known shapes with stable diagnostics; do not invent defaults.
- [Version coupling across authoring, validation, and simulation grows] → Include the full semantic manifest in RuleSet and require exact matches at each boundary; compatibility additions require a new materialization version.
- [A single materialization may become large] → Materialize only one revision, keep values immutable, permit a verified derived cache, and measure fixtures before adding persistence.
- [Consumers accidentally bypass the materializer] → Keep simulation adapters dependent on the contract and add tests that reject raw payload/working-state inputs.
- [Validation artifact retention is incomplete] → Treat missing canonical AST/index artifacts as unavailable; carry the matching FULL-validation result identity/hash for trigger/stack safety rather than depending on transient scanner evidence or rebuilding it with newer semantics.

## Migration Plan

1. Verify #5 immutable revision materialization and #6 exact FULL validation/artifact reads against real code; stop if either boundary cannot supply a versioned immutable source.
2. Land typed DTOs, canonical encoder, diagnostic code registry, and pure fixtures before adding SQLite/application adapters.
3. Add the revision adapter, exact validation admission, and optional rebuildable cache; run deterministic and corruption/missing-artifact tests.
4. Change simulation's stateful rule adapter work to require `RuleSetV1` and record its materialization hash in the simulation input fingerprint; retain existing event-only tests until that integration is complete.
5. Roll back by disabling new consumer registration while retaining any derived cache as unread data. No revision, validation result, or historical simulation hash is rewritten; a future incompatible form uses a new materialization version and compatibility reader.

## Open Questions

- None. The v1 input is limited to applied #5/#6 schemas and validation artifacts; newly introduced rule fields require a separately versioned schema/materialization change.
