## ADDED Requirements

### Requirement: Immutable revision-only typed rule materialization
The system SHALL materialize FormulaBinding, TriggerRule, Modifier, and StackRule only from one explicitly selected immutable config revision and its manifest. It MUST NOT read working entities, a mutable active-release pointer, HTTP request payloads, Graph data, AI output, or current display names while producing the RuleSet. Before returning a RuleSet, the system MUST require an exact successful FULL validation result for the same revision, config hash, and Schema, DSL, Registry, AST, and NumericPolicy versions; missing, stale, blocked, ERROR, or incompatible validation certification MUST return a stable materialization diagnostic and MUST NOT return a partial result.

#### Scenario: Materialize a fully validated revision
- **WHEN** a caller requests a revision whose immutable manifest and matching FULL validation result both pass
- **THEN** the system returns one immutable RuleSet bound to that revision ID, project ID, config hash, and complete semantic version manifest

#### Scenario: Reject a stale or blocked validation result
- **WHEN** a revision lacks a successful matching FULL validation result, or its result contains an ERROR, BLOCK, source/hash mismatch, or semantic-version mismatch
- **THEN** the system returns a stable unavailable or blocked diagnostic and does not expose any typed rules from that revision

#### Scenario: Ignore subsequent working edits
- **WHEN** a RuleSet has been materialized and a user subsequently changes working entities or a display name
- **THEN** the returned RuleSet and a repeat materialization of the unchanged revision remain byte-equivalent

### Requirement: Complete typed v1 rule graph with provenance
The RuleSet SHALL contain typed, immutable FormulaBinding, TriggerRule, Modifier, and StackRule values for every supported v1 known-schema rule field in the revision. Each materialized value MUST include a stable rule identity, source entity ID and kind, canonical field path, payload ordinal, rule kind/version, canonical body hash, and any source expression span or resolved cross-entity identity required by that rule kind. FormulaBinding values MUST reference their validated canonical AST, AST hash, output attribute, and resolved selector reads. TriggerRule, Modifier, and StackRule values MUST expose only their declared typed v1 fields, including event/target/effect identity, operation, priority, cap, duration, and termination-budget fields where applicable. The RuleSet MUST retain the matching FULL validation-result identity and hash as certification for trigger/stack safety.

#### Scenario: Preserve formula provenance
- **WHEN** a valid FormulaBinding appears in a supported entity payload
- **THEN** its typed value references the validated canonical AST and output attribute and retains its source entity, canonical expression field path, ordinal, and span

#### Scenario: Preserve rule safety and reference provenance
- **WHEN** a valid TriggerRule, Modifier, or StackRule references an effect, attribute, target selector, or termination/stack field
- **THEN** its typed value carries the resolved stable identities and source provenance necessary for a deterministic consumer to trace the originating revision field, and the RuleSet identifies the PASS FULL validation result that certified its safety

#### Scenario: Reject incomplete known rule data
- **WHEN** a supported v1 rule field has a missing canonical AST/index artifact, unsupported known shape, invalid typed enum, dangling reference, invalid canonical quantity, or malformed provenance span
- **THEN** materialization fails with a stable diagnostic that identifies the source entity and field path, rather than omitting or defaulting that rule

### Requirement: Canonical ordering and materialization identity
The system SHALL deterministically order RuleSet values by source entity stable ID as raw UTF-8 bytes, canonical field path, payload ordinal, and rule kind; semantic payload arrays SHALL retain their declared order. It MUST derive each stable rule identity from revision ID, source entity ID, canonical field path, ordinal, and rule kind. The system SHALL encode a RuleSet using a versioned canonical representation and SHALL compute a SHA-256 materialization hash over the revision/config identity, complete semantic manifest, typed graph, canonical AST identities, and provenance. Request ID, run ID, database row ID, wall-clock values, worker scheduling, and display-only fields MUST NOT affect that hash.

#### Scenario: Repeat materialization deterministically
- **WHEN** the same valid revision is materialized repeatedly across process restarts or different database row orders
- **THEN** the ordered typed values, canonical bytes, stable rule IDs, and materialization hash are exactly equal

#### Scenario: Preserve semantic array order
- **WHEN** two valid rules share a source field but occupy different payload array positions
- **THEN** their ordinals and relative order match the immutable payload definition and are reflected in their stable rule identities

#### Scenario: Detect semantic input drift
- **WHEN** a caller compares RuleSets whose revision/config identity, validation semantic manifest, canonical AST, typed graph, or provenance differs
- **THEN** at least the materialization hash differs and the caller can identify the bound revision and version manifest from each RuleSet

### Requirement: Reuse validation artifacts without rule execution
The materializer SHALL consume the existing versioned parser/AST artifacts and exact FULL-validation certification produced for the immutable revision, and SHALL decode only known structural v1 rule fields from that manifest. It MUST NOT execute formulas, evaluate random behavior, schedule events, calculate loops, mutate state, or create a second DSL, decimal, unit, reference, or safety implementation. The materialization contract SHALL be transport-neutral and MUST NOT import SQLite, HTTP, Vue, Graph, AI, or a mutable simulation state package.

#### Scenario: Materialize without executing behavior
- **WHEN** a consumer materializes a valid revision containing FormulaBinding and event rules
- **THEN** the system returns typed immutable data without evaluating an expression, emitting an event, mutating a project, or performing I/O other than immutable artifact reads

#### Scenario: Keep validation semantics authoritative
- **WHEN** a formula or rule is invalid under the recorded validation artifacts
- **THEN** the materializer reports the validation/materialization failure and does not re-parse it under a newer implementation or weaken its severity

### Requirement: Forward compatibility and rebuildable derived artifacts
Unknown namespaced `extensions` and fields outside the supported v1 known-schema rule structures SHALL remain opaque and MUST NOT be materialized or treated as validated behavior. A cached RuleSet, if implemented, SHALL be derived data keyed by immutable revision/config/version identity and integrity hash; it MUST be rebuildable from immutable source artifacts, and cache loss or corruption MUST NOT alter a config revision, validation run, or historical consumer result.

#### Scenario: Exclude an unknown extension
- **WHEN** a valid entity contains an unknown namespaced extension with formula-like or rule-like content
- **THEN** the RuleSet excludes that content and does not claim it is executable or validated v1 behavior

#### Scenario: Rebuild a missing derived artifact
- **WHEN** a previously cached RuleSet is missing or fails its integrity check while all immutable source artifacts remain available
- **THEN** the system rebuilds an equivalent canonical RuleSet and leaves the revision and validation records unchanged

#### Scenario: Fail safely when source artifacts cannot be recovered
- **WHEN** a required immutable manifest or validation artifact is unavailable or inconsistent
- **THEN** the system returns a stable materialization-unavailable diagnostic and does not substitute current payload parsing or a partial cached result
