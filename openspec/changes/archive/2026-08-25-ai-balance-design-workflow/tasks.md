## 1. Applied Baseline and External Contract Verification

- [x] 1.1 Verify the applied #5 baseline exposes immutable revision materialization, stable entity/kind/path Schema metadata, expected `entity_version`, the project-scoped serialized writer, canonical blob/reference/tag indexes, and a transaction abstraction capable of one multi-entity revision; stop with a dependency report instead of creating a parallel fact or save model.
- [x] 1.2 Verify the applied #6 baseline exposes exact revision/materialization FULL validation plus reusable Schema/reference/DSL/NumericPolicy evaluators, stable issue identities/evidence and non-overridable structural BLOCK semantics; stop rather than duplicate parser, reference, cycle or numeric rules.
- [x] 1.3 Verify the applied #7 baseline exposes VersionManifest/diff/revision/current-release readers, `JobStore`/`EventStore`/SSE/cancel/idempotency, immutable release audit and post-commit handoff ports; prove the AI module cannot import or invoke release, Gate override or Graph activation implementations.
- [x] 1.4 Verify the applied #8/#9 baseline exposes exact revision-to-Graph Snapshot/content-hash identity and immutable impact/evidence references where available; #12 MUST use #8 identity for retrieval and MUST NOT treat #9 suspected evidence as fact.
- [x] 1.5 Verify the applied #10 baseline exposes pure versioned scenario/engine/evaluator/Metric ports that can evaluate a sealed proposal materialization without inserting a formal simulation run, plus normal post-accept simulation handoffs; stop rather than build a second simulator.
- [x] 1.6 Verify the applied #11 baseline exposes pure versioned comparison/structure/threshold ports that can assess proposal preview results without inserting a formal risk report, plus normal post-accept risk handoffs; stop rather than duplicate risk rules or severity logic.
- [x] 1.7 Replay the read-only `D:/WorkSpace/local-rag` #3 provider/Eco Guardian fixtures and record the exact retrieve request/response, explicit Snapshot/hash, limits, mode/degradation, generation/model, scoring/evidence, retention and stable error contracts consumed by #12; do not modify local-rag files or artifacts.

## 2. AI Module Boundaries and Versioned Domain Contracts

- [x] 2.1 Create `internal/ai/{contract,provider,retrieval,tools,preview,orchestration,application,audit}` boundaries plus adapter packages, and add architecture tests preventing contract/tool/Patch code from importing Gin, SQLite, Vue, concrete Provider/local-rag clients, entity repositories or release/activation workers.
- [x] 2.2 Define validated tagged types for provider/capability state, endpoint classification, frozen base/baseline identity, goals/metrics/constraints, allowed target/path/operation, budget, evidence, attempt/stage/outcome, tool call/result, DraftPatch, preview, freshness and human decision.
- [x] 2.3 Implement domain-separated canonical JSON and SHA-256 codecs for `AIDesignInputV1`, provider/Prompt/Schema/tool/orchestrator manifests, retrieval evidence manifest, tool inputs/results, DraftPatch/diff, preview and audit chain; sort sets/paths by raw UTF-8 and reuse #6 canonical decimals.
- [x] 2.4 Add compile-time immutable `PromptRegistry`, `DraftPatchSchemaRegistry`, `AIToolRegistry` and `AIBudgetPolicyRegistry`, with startup rejection for duplicate identity, version/hash drift, missing hard limit or any mutation/release/credential tool.
- [x] 2.5 Seed and fixture v1 Prompt, DraftPatch, six-tool and bounded budget manifests, including exactly three automatic format-repair rounds and finite provider turn, tool call, search candidate, time, context and output limits; expose resolved limits without allowing unbounded user overrides.
- [x] 2.6 Add property/golden tests proving map/registration/input order, display names, Job IDs and timestamps cannot alter canonical identities, while any model/Prompt/Schema/tool/input/evaluator/evidence version change does.

## 3. OpenAPI, Settings, Capability and Credential Boundary

- [x] 3.1 Extend `api/openapi.yaml` with strict `POST /api/v1/ai-design-jobs`, `GET /api/v1/draft-patches/{id}`, accept/discard request/result schemas, 202 Job links, idempotency, attempt/preview/freshness/evidence projections and RFC 9457 AI error codes; regenerate Go/TypeScript clients and add drift checks.
- [x] 3.2 Extend non-sensitive `GET/PATCH /api/v1/settings` and runtime status schemas with Provider endpoint/model/timeouts, endpoint classification, `credential_present`, structured-output/tool/stream capability, registered versions and limits; never include credential material.
- [x] 3.3 Implement write-only `PUT/DELETE /api/v1/settings/credentials/{provider}` through a platform credential port and Windows Credential Manager adapter, with environment variable fallback read as non-persistent and lower-precedence behavior defined by fixtures.
- [x] 3.4 Implement loopback/local and explicitly configured cloud endpoint validation, OpenAI-compatible capability probing and stable unconfigured/degraded/unavailable diagnostics; AI capability failure MUST leave all non-AI runtime capabilities unchanged.
- [x] 3.5 Implement a centralized secret/sensitive-value redactor for Provider headers/bodies, errors, Problem Details, logs, SSE events, audits and backups; add canary credentials and environment values to prove they never persist or echo.
- [x] 3.6 Add settings/runtime/credential handler tests for missing Provider, incompatible structured output/tools, credential create/replace/delete, cloud disclosure metadata, redacted failures and project backup exclusion.

## 4. Provider Adapter and Structured Attempt Protocol

- [x] 4.1 Define a transport-neutral Provider port accepting a fixed attempt manifest, structured response schema, registered tools, model parameters, timeout and cancel token, and emitting ordered usage/tool/structured-response/warning/error events without exposing SDK objects.
- [x] 4.2 Implement the OpenAI-compatible subset adapter for structured output, tool calls, event streaming, model identity, usage, timeout and best-effort cancellation; classify permanent capability/configuration errors separately from transient timeout/dependency errors.
- [x] 4.3 Resolve endpoint/model/Prompt/Schema/tool/budget configuration exactly once per attempt, persist their identities before invocation and prove concurrent settings changes affect only later attempts.
- [x] 4.4 Enforce bounded request/context/response/event sizes and redact before any persistence or logging; retain the PRD-required original structured response only after validation/redaction and never request/store hidden chain-of-thought.
- [x] 4.5 Implement provider mock fixtures for valid structured Patch, multi-step tool calls, malformed/oversized JSON, timeouts, disconnects, cancellation, late terminal responses, usage accounting and every capability probe outcome.
- [x] 4.6 Add contract tests showing partial stream data is never executable or sealable and only one matching terminal structured response can advance an attempt.

## 5. Frozen Input and Snapshot-Bound Retrieval Evidence

- [x] 5.1 Implement AI Job admission that atomically resolves project/base revision/config/VersionManifest/materialization, target stable IDs/kinds/entity versions/allowed paths, current release or tagged `NO_BASELINE`, goals/metrics/constraints/scenes and expanded v1 budget into canonical `AIDesignInputV1`.
- [x] 5.2 Reject absent/blocked/unmaterializable bases, display-name identities, unknown/duplicate targets, invalid field paths/operations, incompatible scenes/metrics and values over hard limits before creating runnable work or invoking external services.
- [x] 5.3 Implement project-scoped idempotency so the same key plus exact canonical input returns the original Job, while the same key with any changed base/target/version/goal/constraint/scope/budget returns 409 without side effects.
- [x] 5.4 Implement the local-rag retrieval adapter that always sends project namespace, explicit base Snapshot, exact bounded filters, default explicit relationship kind and registered limits to `/v1/graphs/{namespace}/retrieve`.
- [x] 5.5 Validate `resolved_snapshot_version`, stored `content_hash`, filters and selected generations against #8 identity before exposing results; reject mixed identities and preserve `SNAPSHOT_INDEX_NOT_READY/rebuild_required` without implicitly rebuilding.
- [x] 5.6 Canonicalize insert-only evidence refs containing Node/citation, seed/path records, provenance/relationship kind/confidence, BM25/Vector ranks/raw scores, RRF/graph/rerank scores, mode/degraded/warnings and algorithm/model/generation identities; give the Provider only bounded citations plus opaque evidence IDs.
- [x] 5.7 Add cross-repo consumer tests for explicit inactive ready Snapshot, active changes during request, hybrid/BM25-only/Vector-only, rerank degradation, empty evidence, both retrievers unavailable, evicted indexes, inferred opt-in rejection/default explicit, hard limits and Snapshot/hash mismatch.

## 6. Tool Allowlist, DraftPatch and Policy Enforcement

- [x] 6.1 Register only `read_revision_context`, `retrieve_evidence`, `validate_proposal`, `preview_simulation`, `preview_risk` and `search_parameters`, each with immutable name/version/Schema hash, required identities, deterministic/side-effect classification, maximum calls/result size, timeout and cancel behavior; make retrieval a one-shot pre-Provider request derived entirely from frozen input, never a model-controlled query.
- [x] 6.2 Implement Tool PolicyGuard validation for registered tool, current attempt/stage, exact base identity, target/path/operation subset, call/search/time budget and output envelope; reject arbitrary HTTP/SQL/file/process, repository mutation, revision/release, Graph activation and credential requests before execution.
- [x] 6.3 Implement stable ordered tool call/audit envelopes with input/result hashes, implementation versions, evidence refs, timing/usage and policy errors; tool output SHALL be data only and cannot add new tool definitions or permissions.
- [x] 6.4 Implement `DraftPatchV1` strict decoding and canonicalization for base identities, ordered stable-ID targets, expected entity versions, typed `replace` and Schema-allowed collection operations, rationale, assumptions and non-empty per-operation evidence refs.
- [x] 6.5 Reject unknown/duplicate JSON members, identity/status/revision/release/Registry paths, unauthorized extensions or fields, fabricated evidence IDs, invalid typed/decimal/unit values, ambiguous array operations and any target/path outside the user's allowlist.
- [x] 6.6 Build schema-aware original/canonical diff and stable Patch identity/order, retaining the redacted raw structured response as audit only and never executing it directly.
- [x] 6.7 Add fuzz/property tests for Patch JSON/path parsing and malicious tool/Prompt injection, proving no input can expose or invoke a mutation, publish, credential or arbitrary external capability.

## 7. Bounded Repair, Deterministic Search and Advisory Preview

- [x] 7.1 Implement format-repair classification so only JSON/representation/Schema errors can start a new bounded Provider attempt; semantic BLOCK, missing evidence, policy violation and unauthorized scope MUST terminate without repair.
- [x] 7.2 Limit automatic repair to three ordered rounds, passing only stable bounded parse/Schema diagnostics and the redacted response; preserve every attempt manifest/outcome and prevent repairs from changing goal, constraints, evidence, tools or allowed paths.
- [x] 7.3 Build sealed `ProposalMaterializationV1` by applying a validated Patch to an isolated base materialization with a canonical hash, without writing working entities, config revisions, formal validation/simulation/risk results or Gate state.
- [x] 7.4 Adapt #5/#6 Schema, reference, DSL, FULL validation, decimal/unit and structural safety services to the proposal materialization; carry original stable issues/fingerprints/evidence and make every ERROR/BLOCK non-overridable by model text.
- [x] 7.5 Implement versioned bounded grid/constraint search for eligible numeric ranges using registered candidate ordering and #6/#10 evaluators; record every evaluated input/result hash and stop exactly at candidate/time/cancel budgets.
- [x] 7.6 Adapt #10 fixed scenario engine/evaluator/Metric ports and #11 comparison/structure/threshold ports to produce `advisory=true` AI preview payloads with complete versions/hashes/metrics/assumptions/risk evidence, inserting no formal simulation run, risk report or Gate result.
- [x] 7.7 Mark missing required inputs, unavailable evaluator/Metric, budget exhaustion, validation BLOCK or inconsistent preview as not acceptable; never fabricate zero/estimated metrics or reduce deterministic risk severity.
- [x] 7.8 Add integration/golden tests proving preview calls reuse applied evaluator versions, are deterministic under worker/order changes, create no revision/formal report, and cannot be consumed by #7 release Gate.

## 8. Durable AI Job, Attempts, Streaming, Cancellation and Retry

- [x] 8.1 Add #7 Job admission and ordered phases `input_pinned`, `evidence_pinned`, `provider/tool_loop`, `deterministic_preview`, `patch_sealed`, using expected phase/owner/cancel generation transitions and terminal result links.
- [x] 8.2 Persist one immutable manifest/outcome for the initial call, each repair and each explicit retry; enforce at-most-one terminal response and at-most-one Patch seal per attempt while retaining an ordered lineage.
- [x] 8.3 Emit committed SSE events for stages, bounded progress, warnings, tool identities, repair count and terminal outcome, support Last-Event-ID replay and polling fallback, and keep raw tokens/secrets/unredacted Provider bodies out of events.
- [x] 8.4 Implement idempotent cancel that prevents unstarted calls, signals Provider/tools, increments cancel generation and rejects sealing from late responses; map uncertain remote completion to interrupted and record only a redacted `ignored_late_result` event.
- [x] 8.5 Implement restart recovery so deterministic steps can reuse or rerun matching hashes, but any Provider attempt without a durable terminal receipt becomes interrupted and is never automatically invoked again.
- [x] 8.6 Implement user-explicit retry as a new linked Job/attempt that rechecks base/target/Graph/settings identities and preserves the failed lineage; do not silently retry Provider, non-idempotent or permanent capability failures.
- [x] 8.7 Add race/fault-injection tests for cancel before/during/after Provider and tools, SSE disconnect, process crash at every phase/seal boundary, duplicate worker delivery, timeout, late result and explicit retry, proving no duplicate Provider call is created by recovery.

## 9. SQLite Persistence, Audit, Redaction and Retention

- [x] 9.1 Add additive migrations and repositories for logical `ai_design_runs`, `draft_patches` and `evidence_refs`, with physical attempt/tool/audit/decision children as needed, foreign keys, stable ordinals, content hashes, size limits and uniqueness for Job/attempt/Patch/decision/idempotency.
- [x] 9.2 Separate mutable Job phase/checkpoint data from insert-only terminal attempt, evidence, raw-redacted response, canonical Patch, preview and human decision records; prevent update/delete of sealed generation facts with repository APIs and SQLite constraints/triggers.
- [x] 9.3 Implement canonical audit events for provider/model/parameters, Prompt/Schema/tool/orchestrator/input/evaluator versions, goals/constraints/scope, evidence, structured response, calls/results, Patch/diff, preview, assumptions/rationale, usage/errors/cancel/retry and decision.
- [x] 9.4 Apply redaction and bounded payload policy before every DB/log/event write, content-address large allowed blobs, exclude secret/unrelated files/hidden reasoning and add project-capacity fixtures plus the documented AI audit retention behavior.
- [x] 9.5 Implement read-time projections for target/current freshness, current Provider capability and post-accept formal validation/Graph/simulation/risk links without mutating historical generation/audit facts.
- [x] 9.6 Add migration replay/rollback, constraint, crash atomicity, redaction canary, payload limit, historical read and backup tests proving AI records cannot corrupt or become configuration/release facts and credentials never enter the backup.

## 10. Human Accept and Discard Application Commands

- [x] 10.1 Implement strict accept admission with Patch ID/hash, base revision and ordered target expected versions plus Idempotency-Key; require a pending, acceptable, non-canceled Patch and recompute read-time freshness before entering the writer.
- [x] 10.2 Add or reuse a #5 application-service batch command inside the project serialized writer to revalidate Patch allowlist/Schema and atomically update all working entities/blob/reference/tag indexes, create exactly one config revision/VersionManifest and append the accepted decision.
- [x] 10.3 Return 409 `REVISION_CONFLICT` for any base/target version mismatch and stable validation/policy errors for revalidation failures, rolling back every entity/index/revision/decision write and preserving the Patch for read-only comparison/discard.
- [x] 10.4 After commit, invoke only the existing FULL validation and downstream #8/#10/#11 handoffs for the accepted revision; prohibit reuse of advisory preview as formal results and prove accept never calls release or Graph activation.
- [x] 10.5 Implement discard as an append-only, idempotent, mutually exclusive human decision; accepted/discarded/stale/failed Patches cannot be newly applied, and repeated same request returns the original decision/result.
- [x] 10.6 Implement accept idempotency so a lost successful response replays the same accepted revision and never creates a second revision; conflicting key/request/Patch combinations return 409 without side effects.
- [x] 10.7 Add transaction/race tests for valid multi-entity accept, one stale target, concurrent manual edit, duplicate accept, accept-vs-discard, validation failure, crash before/after commit and post-commit handoff retry, asserting one-or-zero complete revision outcomes.

## 11. HTTP Handlers, DraftPatch Resource and Security Checks

- [x] 11.1 Implement thin handlers for AI Job creation and DraftPatch retrieval through application services/generated DTOs, returning exact 202 Job/result URIs, immutable input/Patch/preview identities, attempt lineage, evidence, decisions, freshness and formal-result links.
- [x] 11.2 Implement accept/discard handlers with idempotency, Patch/base/entity preconditions, stable RFC 9457 validation/revision/decision errors and no direct repository, Provider, release or activation access.
- [x] 11.3 Enforce strict JSON, content type, unknown member, request/body/collection limits, loopback origin protections and safe display messages for AI/settings/credential routes; never echo Provider raw errors or user secrets.
- [x] 11.4 Map local-rag and Provider failures to stable retryable/non-retryable AI stage errors while preserving provider request IDs only when safe; keep partial ranking/response data out of failed public results.
- [x] 11.5 Add generated-client/provider-consumer/API contract tests for success, no evidence, retrieve degradation/index-not-ready, three-round limit, unauthorized Patch/tool, Provider timeout/cancel, stale/conflict, accept/discard replay and secret redaction.

## 12. AI Design and Provider Settings User Interface

- [x] 12.1 Add `/ai-design` routes, generated-client query stores and resource projections for selecting base revision, entering goals/metrics/constraints, choosing allowed targets/fields/scenes and showing server-published budgets/capabilities.
- [x] 12.2 Build Provider readiness and settings UI for local/cloud endpoint, model, non-sensitive options, write-only credential set/clear and capability test; display cloud data-disclosure context and never retain the secret in client state after submission.
- [x] 12.3 Build Job stage/progress/repair-count/warning UI with SSE resume and polling fallback, explicit cancel and explicit retry, covering queued/running/succeeded/failed/canceled/interrupted without inferring state from partial model output.
- [x] 12.4 Build candidate review with original/canonical multi-entity diff, per-operation raw evidence links, retrieval mode/generation/model/scores/warnings, AI-labeled rationale/assumptions, tool/version identities and validation/simulation/risk advisory feedback.
- [x] 12.5 Implement server-authoritative accept/discard controls and states for pending/acceptable/stale/accepted/discarded/failed; preserve review input on 409, focus the conflict summary and clearly state that accept creates a revision but does not publish.
- [x] 12.6 Cover Provider unconfigured/incompatible, loading/empty, no evidence, index rebuild required, degraded retrieval, illegal output, repair/search budget limit, retryable/non-retryable failure, cancellation, late result, stale and success states with actionable text.
- [x] 12.7 Add textual alternatives for every diff/evidence/risk/status visual, keyboard navigation and focus management, aria-live terminal updates, WCAG 2.1 AA contrast and 1024px desktop component tests.

## 13. Verification, End-to-End Workflow and Handoff

- [x] 13.1 Add Provider mock, retrieval fixture, Patch/tool policy, redaction and deterministic evaluator test matrices covering every spec scenario and all model/Prompt/Schema/tool/input/evaluator version identities.
- [x] 13.2 Add a Playwright primary flow: configure a Provider, pin a base revision and evidence, generate a multi-entity Patch, inspect diff/evidence/assumptions/validation/simulation/risk preview, accept once, observe exactly one new revision and its newly run formal analyses, then require a separate human release action.
- [x] 13.3 Add E2E failure flows for no Provider/evidence, BM25/Vector degradation, Snapshot/hash mismatch, evicted index, illegal JSON/third repair failure, unauthorized tool/path, deterministic BLOCK, budget exhaustion, cancel/late response, explicit retry, stale target and accept conflict.
- [x] 13.4 Add invariants tests proving Provider/Embedding/Rerank/stream failure cannot change formula/reference/simulation/risk/release outputs; no AI, tool, Job recovery, HTTP or UI code path can directly write a release, activate Graph or mutate an immutable revision.
- [x] 13.5 Run fixed input/evidence/tool fixtures under shuffled map/SQLite order, worker counts, process restarts and repeated deterministic steps, asserting stable canonical identities/audit and deterministic preview while treating Provider text as recorded non-deterministic output.
- [x] 13.6 Run representative capacity/cost-bound fixtures for context, response, audit, tool/search calls, 2,000/10,000 entity projects and cancellation latency; prove all v1 hard limits terminate work and project DB growth stays within the documented bound.
- [x] 13.7 Run formatting, lint, generated-code/OpenAPI drift, strict OpenSpec validation, Go unit/integration/race/fuzz/contract suites, frontend typecheck/unit/accessibility, Playwright and Windows Credential Manager tests; record exact Registry/fixture/tool versions and passing commands for implementation handoff.
