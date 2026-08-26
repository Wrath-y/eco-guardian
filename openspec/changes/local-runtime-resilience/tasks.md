## 1. Applied Baseline and Integration Gates

- [x] 1.1 Verify the applied #5 project manager exposes native selection tokens, the single-active-project lock/close lifecycle, recent-project settings seam, migration preflight, and safe project-open results; stop full integration rather than creating a parallel project model if any boundary is absent.
- [x] 1.2 Verify the applied #7 implementation exposes the one six-state `jobs`/`job_events` repository, ordered event replay, SSE/poll/cancel handlers, Gate Registry, release intents, and worker lifecycle; define narrow compile-time adapters for final names without adding runtime Job tables.
- [x] 1.3 Verify #8–#12 expose their planned Graph provider/client and status, external Task reconciliation, deterministic simulation/risk/impact recovery inputs, AI Provider capability/settings, credential, and no-auto-retry seams; record unavailable descriptors for modules not yet applied.
- [x] 1.4 Vendor or reference the versioned local-rag #4 OpenAPI/consumer fixture manifest read-only and verify `/health`, four-state Task, error/request-ID, restart, and explicit-rebuild contracts before implementing the runtime consumer.
- [x] 1.5 Inspect the applied OpenAPI generation, embedded Web build, migration ledger, settings, logging, and test harnesses and choose one existing owner for each; fail the gate if implementation would duplicate an established source of truth.
- [x] 1.6 Add compile-time architecture tests that prevent `internal/app/runtime` from importing module repositories/handlers directly and prevent business modules from depending on Windows process implementations.

## 2. Build Identity, Application Directories, and Settings

- [x] 2.1 Add shared build information for Eco Guardian SemVer, build/commit identity, supported runtime-status schema, package mode, and embedded asset digests, with deterministic development defaults and release linker injection tests.
- [x] 2.2 Add a platform-neutral application-directory resolver and Windows implementation for `%LOCALAPPDATA%/EcoGuardian/{runtime,logs}`, restrictive directory/file creation, canonical path handling, and explicit unsupported-platform diagnostics.
- [x] 2.3 Define the versioned strict `settings.json` schema for browser auto-open, recent projects, package/Graph selection, local-rag endpoint/executable and timeouts, non-secret AI references from #12, log bounds, and existing backup defaults without accepting credentials or business data.
- [x] 2.4 Implement settings load/default/migration/validation plus temporary-write, flush, atomic replace, and prior-file preservation; add corruption, unknown-version, permissions, concurrent-write, and injected-replace-failure tests.
- [x] 2.5 Validate all endpoint/listener settings as loopback-only where required, reject unauthenticated non-loopback local-rag, bound ports/timeouts/restart/log values, and report field-specific RFC 9457 errors without leaking paths.
- [x] 2.6 Implement only bounded launch overrides needed for packaged startup, document their precedence over defaults/settings in generated help, and prove command-line/environment processing cannot inject secrets into logs or turn on a remote listener.
- [x] 2.7 Extend the existing settings application service and OpenAPI DTO rather than adding a runtime-specific settings endpoint; support atomic apply-now versus reconnect/restart-required results and preserve #12's write-only Credential Manager boundary.
- [x] 2.8 Add tests proving settings, recent projects, logs, and runtime metadata remain outside `project.db`, while project Jobs and every business fact remain inside the selected project and project backups exclude machine state.

## 3. Phased Runtime Coordinator and Single-Entry HTTP Host

- [x] 3.1 Create `internal/app/runtime` lifecycle types, monotonic immutable status snapshots, startup/degradation/stopping phases, observer subscriptions, and a coordinator that receives platform/module ports instead of concrete stores or handlers.
- [x] 3.2 Implement startup ordering for settings, package verification, listener acquisition, HTTP admission, dependency startup, machine recovery, safe recent-project open, capability convergence, and browser launch with cancellation/deadline propagation.
- [x] 3.3 Refactor or create `cmd/eco-guardian` as the single composition root that wires embedded assets, runtime/project/application services, workers, observers, and ordered shutdown without business logic in `main`.
- [x] 3.4 Acquire and retain a TCP4 listener on `127.0.0.1:0` by default, support bounded preferred-port fallback, derive the canonical URL from the bound listener, and reject Host/config paths that could advertise or bind a non-loopback interface.
- [x] 3.5 Serve the embedded Vue assets and `/api/v1` from the same listener/origin, include embedded migrations/OpenAPI/schemas/templates in build identity, and add missing/corrupt embedded-asset startup tests.
- [x] 3.6 Add the Windows default-browser adapter, invoke it only after the server is reachable, and emit the exact copyable loopback URL to console when present and safe logs when launch fails.
- [x] 3.7 Integrate recent-project open through #5 only after machine recovery; handle locked/newer-schema/recovery-required projects by keeping the selection UI available and preserving the project unchanged.
- [x] 3.8 Implement ordered, idempotent application shutdown that stops HTTP mutation admission, coordinates Job safe points, stops workers, handles the owned child, closes stores last, and remains bounded under repeated signals.
- [x] 3.9 Add lifecycle tests for every startup phase failure, port bind race, immediate browser request, concurrent status reads, partial dependency readiness, repeated stop, and proof that Graph failure never prevents the offline host from serving.

## 4. Deterministic Complete and Lightweight Package Assembly

- [x] 4.1 Define a generated `package-manifest.json` schema with package mode, supported OS/architecture, Eco/build identity, embedded digests, and complete-package local-rag/Python/Embedding/Rerank relative paths, versions, sizes, and SHA-256 hashes.
- [x] 4.2 Implement package-manifest loading and verification before executing external assets, with stable diagnostics for missing, corrupt, wrong-architecture, path-escape, duplicate, and unexpected-mode components.
- [x] 4.3 Add a reproducible complete-package assembly pipeline that stages `eco-guardian.exe`, the pinned compatible local-rag distribution, Python runtime, Embedding/Rerank models, defaults, licenses/notices, and manifest without downloading on first launch.
- [x] 4.4 Add a reproducible lightweight-package pipeline containing `eco-guardian.exe`, configuration template, licenses/notices, and manifest while proving it has no bundled Graph/Python/model assets.
- [x] 4.5 Add package inspections proving both executables embed the Web frontend, migrations, OpenAPI, entity schemas, and deterministic DSL/scene/threshold templates and that neither distribution contains an LLM or credential.
- [x] 4.6 Add startup classification that maps each missing complete-package asset to the precise process/Graph/retrieval capability degradation and prevents execution of unverified files while leaving offline-safe features available.
- [x] 4.7 Add artifact reproducibility tests for stable manifests/digests from identical inputs and explicit failures when pinned local-rag/OpenAPI/consumer-fixture versions drift.

## 5. Windows Owned-Process Adapter and Graph Supervisor

- [x] 5.1 Define platform-neutral process, Job Object, command, clock/backoff, ownership, exit, and log-stream ports plus an unsupported-platform adapter that keeps OS details out of capability and domain packages.
- [x] 5.2 Implement Windows suspended hidden child creation with explicit inherited stdout/stderr handles, sanitized environment, working directory, and live process/thread handles; close every handle correctly on all failure paths.
- [x] 5.3 Implement a per-Eco-instance Windows Job Object with kill-on-close, assign the suspended local-rag before resume, and prove descendants are included without attaching any pre-existing process.
- [x] 5.4 Build bundled local-rag commands only from verified manifest assets and validated loopback ports/data locations, pass no secret through logged arguments, and treat local-rag-managed Python sidecars as descendants rather than separately discovered Eco processes.
- [x] 5.5 Implement external-versus-bundled selection: probe an explicitly selected endpoint, classify it as external only after compatible health, start a bundled child on a separate loopback candidate only when package mode permits, and never kill an incompatible occupant.
- [x] 5.6 Implement supervisor states, readiness deadline, stable-ready reset, bounded attempt/window/backoff restart policy, fresh-port retry for bind failures, restart exhaustion, exit-code diagnostics, and monotonic status publication.
- [x] 5.7 Store only non-authoritative process summaries for diagnostics; require a live handle plus current launch generation for stop/terminate so PID/name/port records and PID reuse can never establish ownership.
- [x] 5.8 Implement graceful owned-child stop with deadline and Job Object fallback after HTTP/workers reach safe boundaries; ensure external endpoints receive no stop signal and remain alive after Eco exits.
- [x] 5.9 Wrap bounded child stdout/stderr lines as untrusted component events with stream/launch identity, redaction, truncation, and backpressure handling rather than accepting arbitrary child JSON fields.
- [x] 5.10 Add Windows helper-process tests for success, hidden window, descendant cleanup, immediate exit, crash loop, bind collision, assignment failure, stdout/stderr flood, graceful timeout, parent crash, PID reuse, and external-process survival.

## 6. local-rag Health, Compatibility, and Observation

- [x] 6.1 Reuse #8's generated local-rag client, shared request-ID propagation, typed safe error mapping, and loopback policy; do not add a second health/task HTTP stack or branch on provider messages.
- [x] 6.2 Define operation descriptors for snapshot lifecycle, durable Task polling, activation, core query, Graph/FTS readiness, BM25/Vector/Rerank retrieval modes, and explicit rebuild with their required API/schema/capability/limit inputs.
- [x] 6.3 Implement a pure compatibility evaluator for API `v1`, Graph Snapshot schema `1.0`, capabilities, limits, dependencies, and `ok|degraded|unavailable` plus 200/503 semantics, ignoring unknown additive health fields.
- [x] 6.4 Add the versioned compiled known-fix table that applies minimum patch floors only to identified affected release lines, records observed/required versions, and never rejects a fully compatible service solely because its implementation major differs.
- [x] 6.5 Implement bounded health observations with timeout, short TTL, single-flight refresh, process/provider outcome invalidation, observation generation/time, and safe malformed-contract/timeout/error reasons.
- [x] 6.6 Keep health observation read-only and separate from admission: add tests proving runtime/status polling does not start processes, submit/rebuild Snapshots, create Jobs, invoke models, or replace #8 exact Snapshot inspection.
- [x] 6.7 Map core SQLite/migration/query and required Graph/FTS failures to affected unavailable capabilities; map optional Vector/enabled-Rerank or one-mode retrieval failures to the exact degraded state while preserving deterministic Graph behavior.
- [x] 6.8 Add local-rag #4 consumer tests for healthy/degraded/unavailable health, 200/503, deterministic ordering, safe identities, unknown fields, limits, known-fix rules, request IDs, timeouts, malformed fields, and recovery after re-probe.

## 7. Capability Registry and Runtime Status API

- [x] 7.1 Implement a versioned capability descriptor/registry with stable IDs, required/optional prerequisites, pure evaluators, ordered safe reasons/actions, observation generations, and startup rejection for duplicate IDs, cycles, or conflicting versions.
- [x] 7.2 Register the local editing, validation, revision/candidate, Graph-independent simulation, backup, Graph sync, deterministic impact, retrieval, AI design, and release projections by adapting the applied module-owned availability and Gate results.
- [x] 7.3 Encode the degradation matrix so Graph core/required Graph-FTS loss disables Graph sync/new impact/Graph-dependent AI/release but preserves offline-safe work; Vector/Rerank loss preserves deterministic Graph; and AI Provider loss disables only AI creation/retry.
- [x] 7.4 Integrate #7's Gate Registry/policy result into release availability so missing/unregistered/stale required Gates remain explicit and no runtime degraded mode can relax policy; preserve optional Gate warnings.
- [x] 7.5 Define safe action descriptors for re-probe/reconnect and existing Graph retry, Job cancel, Provider settings, and credential operations, including server-side preconditions and idempotency requirements; reject arbitrary command/process actions.
- [x] 7.6 Extend `api/openapi.yaml` with a versioned `GET /api/v1/runtime/status` DTO containing build/package/listener, phase, safe project/process summaries, dependency observations, capabilities/reasons/actions, recovery summaries, log location, and timestamps.
- [x] 7.7 Implement the runtime status assembler and read-only handler over one consistent immutable snapshot, with RFC 9457 safe errors and no process handles, secrets, arbitrary paths, raw child output, Provider payloads, or business content.
- [x] 7.8 Add reducer/handler tests for every matrix row, multiple simultaneous failures, independent recovery, unregistered modules, release-policy completeness, deterministic ordering/encoding, stale observations, and repeated GET side-effect freedom.

## 8. Unified Job Recovery and Shutdown Coordination

- [x] 8.1 Implement a recovery registry over the existing #7 Job repository with job-kind ownership, legal source states, immutable input/fingerprint requirements, effect checkpoints, automatic-resume policy, and expected-generation transitions.
- [x] 8.2 Implement machine/project recovery coordination in the order: restore journal/project compatibility and migration, release intents/pointers, Graph/external Tasks, deterministic impact/simulation/risk, then remaining queued work; start each normal worker only after its scan commits.
- [x] 8.3 Add the #7 release adapter that resumes or rolls back from durable intent/backup/Graph/pointer facts, blocks unsafe switching, and never infers completion from process or service reachability.
- [x] 8.4 Add the #8 Graph adapter that scans queued/running/interrupted Jobs, polls a saved original local-rag Task ID before any resubmission, inspects exact namespace/version/hash/count/Graph-FTS readiness, and follows the owning idempotent `TASK_NOT_FOUND` path.
- [x] 8.5 Preserve local-rag's exact `queued/running/succeeded/failed` Task states and no-cancel contract; map user stop-waiting/application close to the wrapping Eco Job semantics without creating remote cancellation or a provider `interrupted` state.
- [x] 8.6 Add deterministic impact/simulation/risk adapters that resume/replay the original Job only when complete immutable inputs, implementation fingerprints, checkpoints, and result seals match; otherwise record recovery-required without mixed-version output.
- [x] 8.7 Add the #12 AI adapter that can reuse deterministic sealed steps but marks an in-flight Provider attempt interrupted, rejects late results by cancel/generation, and requires explicit user retry lineage before another model call.
- [x] 8.8 Integrate shutdown with recovery policies: stop new HTTP mutations and claims, persist cancel/interruption intent, wait only for bounded safe effect/database boundaries, then stop dependencies/stores while leaving nonterminal Jobs provably recoverable.
- [x] 8.9 Add Job/event API and UI-state tests proving original Job/result/SSE identities, monotonic event ordinals/progress, idempotent cancel, no duplicate side effects, truthful interrupted/recovery-required states, and terminal-result preservation.
- [x] 8.10 Add crash/failure injection before and after restore/migration/release intent, provider acceptance, external task-ID save, deterministic checkpoint, AI request/receipt, result seal, pointer commit, and shutdown boundaries.

## 9. Structured Logs and Runtime Diagnostics

- [x] 9.1 Initialize the structured logger before package/dependency startup with typed event names/fields for phases, listener, package verification, process lifecycle, health/capability transitions, Job recovery, Task reconciliation, stable codes, and request correlation.
- [x] 9.2 Implement centralized field/value redaction and path normalization that removes credentials, authorization headers, secret-like settings, raw Provider bodies, embeddings, Graph/business text, entity payloads, SQL, stacks from public diagnostics, and user-specific path details.
- [x] 9.3 Implement bounded rotating Eco and child logs under `%LOCALAPPDATA%/EcoGuardian/logs` with validated size/count, flush/close semantics, bounded queue/backpressure behavior, and failure isolation from project transactions.
- [x] 9.4 Correlate HTTP root/attempt request IDs, Eco Job IDs, local-rag Task IDs, process launch generation, and safe provider request IDs across runtime status and logs without using unbounded identifiers as metric labels.
- [x] 9.5 Expose the safe log directory and redacted build/package/config/process/Job/error observations through runtime status, while providing no arbitrary log-file/database read or raw debug-dump API.
- [x] 9.6 Add redaction golden/fuzz tests, malicious child-line tests, rotation/reopen tests, unwritable/disk-full/flood tests, and assertions that logs/runtime status never contain fixture secrets, business payloads, embeddings, SQL, or raw user paths.

## 10. Runtime, Degradation, and Settings User Interface

- [x] 10.1 Generate/update the TypeScript OpenAPI client and create one Pinia runtime store for server status, observation age, capability reasons/actions, reconnect state, and polling cadence; prohibit client-side compatibility or Gate calculation.
- [x] 10.2 Add global service indicators and a degradation banner that separately display Eco, Graph core/FTS, BM25/Vector/Rerank, AI Provider, recovery, and release availability with text plus icons and an explicit list of unaffected capabilities.
- [x] 10.3 Add the `/settings` runtime controls for browser auto-open, supported Graph mode/loopback endpoint/timeouts, non-secret Provider fields from #12, credential-presence links, apply-now/reconnect/restart feedback, and log location.
- [x] 10.4 Implement re-probe/reconnect and existing Graph retry/Job cancel/Provider configuration actions from server descriptors, preserving idempotency keys and disabling controls when preconditions no longer hold.
- [x] 10.5 Integrate persistent Job SSE/`Last-Event-ID` with polling fallback and runtime refresh so reloads, browser disconnects, application restart, and project reopen preserve queued/running/interrupted/reconciling states and never infer ready.
- [x] 10.6 Display the copyable listener URL and log location when browser launch or diagnostics fail, with safe copy feedback and no arbitrary filesystem/browser path submission.
- [x] 10.7 Add keyboard/focus/aria-live behavior and text alternatives for banner transitions, errors, retries, service recovery, and settings validation at the existing 1024px/WCAG 2.1 AA baseline.
- [x] 10.8 Add component tests for loading/healthy/degraded/unavailable/recovering/stale states, every capability matrix row, multiple simultaneous failures, keyboard-only reconnect/retry/settings flows, and secret/path redaction.

## 11. API, Provider Contracts, and Cross-Change Verification

- [x] 11.1 Validate the updated Eco OpenAPI document, generated Go/TypeScript types, runtime/settings Problem Details, action enums, capability/reason schemas, and backward-compatible existing Job/Graph/AI endpoints in CI.
- [x] 11.2 Create versioned Eco runtime consumer/provider fixtures for startup phases, package modes, runtime healthy/degraded/unavailable projections, independent capability recovery, safe actions, settings validation, and redacted diagnostics with digest manifests.
- [x] 11.3 Replay local-rag #4 fixtures for health compatibility, minimum-fix/SemVer diagnostics, snapshot/task/restart, four-state polling, request IDs/errors, and partial retrieval degradation through the actual #8 adapter and runtime reducer.
- [x] 11.4 Add integration tests across #5 project switch rules, #7 release Gate/intent, #8 Graph interrupted reconciliation, #10/#11 deterministic restart, and #12 AI no-auto-retry to prove the runtime composes rather than overrides their contracts.
- [x] 11.5 Add isolation tests that compare validation, revision, simulation, risk, backup, and release facts/hashes before and after Graph, Vector, Rerank, AI, browser, logging, and child-process failures.
- [x] 11.6 Add security tests for loopback-only listeners, Host handling, non-loopback local-rag rejection, traversal-safe package paths, command/environment injection, handle inheritance, settings permissions, credential omission, and sanitized public errors.

## 12. Windows Process, Package, and End-to-End Acceptance

- [x] 12.1 Add Windows process-level tests for Eco listener allocation, preferred-port collision, browser failure, external-compatible/incompatible services, bundled child readiness/crash/restart, Job Object cleanup, graceful timeout, and external survival.
- [x] 12.2 Add complete-package fault cases for missing/corrupt local-rag, Python, Embedding, and Rerank assets and prove each produces the correct bounded process/capability state while offline-safe editing remains reachable.
- [x] 12.3 Add lightweight-package end-to-end tests for no external Graph, incompatible service, compatible degraded service, service recovery, Provider separately unavailable, and proof that no bundled process is started.
- [x] 12.4 Add a full integration flow: launch, open/reopen a project safely, observe Graph startup, interrupt/reconcile an external Task, degrade/recover Vector/Rerank/AI independently, run deterministic work unchanged, and exit with only owned processes cleaned up.
- [x] 12.5 Add application-crash cases at listener, child assignment/readiness, Job recovery, Task acceptance, result seal, and shutdown boundaries; reopen and verify original identities, safe recovery-required outcomes, and no duplicate unsafe effects.
- [ ] 12.6 Build and unpack both staged artifacts in a clean Windows 11 x64 client VM with no development tools or administrator setup; verify loopback-only listeners, embedded UI/migrations/templates, package manifest, logs, project persistence, and exit cleanup. Windows 11 ARM/x64 emulation is supplemental only and does not satisfy this gate.
- [x] 12.7 Inspect staged artifacts and runtime files to prove no LLM, secret, project database, development source dependency, unverified executable, or non-loopback default is included.

## 13. Final Quality Gate and Handoff

- [ ] 13.1 Run the full Go/frontend/generated-contract/package gate plus targeted native Windows race tests on a Windows Server 2025 x64 hosted runner, then run the separate clean Windows 11 x64 client-VM smoke suite; fix every failure. The server result does not replace the client-VM result.
- [ ] 13.2 Run targeted leak/handle/goroutine tests and repeated startup/crash/shutdown loops on Windows Server 2025 x64 to prove listener, process/thread/Job Object, pipe, log, worker, SSE, and SQLite resources close without orphaning owned children.
- [x] 13.3 Review every runtime/status/settings/log/error field against the secret, business-payload, Provider-content, Graph-text, SQL, stack, and path-redaction contract and retain automated regression fixtures.
- [x] 13.4 Verify traceability from every specification scenario to at least one automated test, including port/model/browser failures, compatibility/minimum-fix, capability matrix, Job/Task recovery, ownership, logging, and both Windows package modes.
- [x] 13.5 Run `openspec validate local-runtime-resilience --strict` and confirm the implementation diff is limited to this change's runtime, integration, UI, packaging, and test scope without modifying local-rag artifacts or introducing parallel project/Job/Gate/settings facts.
