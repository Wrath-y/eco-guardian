## Context

See [proposal.md](./proposal.md) for motivation and [the capability spec](./specs/local-runtime-resilience/spec.md) for observable behavior.

The repository is still a planning-only greenfield: there is no `go.mod`, frontend manifest, launcher, runtime package, build pipeline, migration, or test code. Changes #5–#12 are apply-ready but unimplemented. Their artifacts already establish boundaries this change must compose rather than duplicate: #5 owns project selection/locking and recent-project behavior; #7 owns the persistent six-state Eco Guardian Job/event model and release intent; #8 owns Graph projection, the local-rag client and exact Snapshot/Task reconciliation; #10/#11 own deterministic recovery rules; and #12 owns Provider configuration, credentials, and the rule that interrupted AI calls are not automatically repeated.

local-rag is an external sibling repository. Its apply-ready change #4 fixes the provider contract consumed here: `/health` reports API/schema/capability/limit/dependency state with `ok|degraded|unavailable` and 200/503 semantics; external Tasks use only `queued|running|succeeded|failed`, restart under the same ID, and have no cancel endpoint; rebuild is explicit and idempotent; public diagnostics are sanitized. This design reads that contract and vendors/tests its consumer fixtures, but does not change local-rag.

The product constraints are Windows 11 x64, a single `eco-guardian.exe` entry, loopback-only serving, complete and lightweight packages, `%LOCALAPPDATA%/EcoGuardian` for non-business machine state, and `project.db` as the sole project business fact source. Windows Server x64 hosted runners provide continuous Windows API, process, race, handle, and resource verification, but do not replace the clean Windows 11 x64 client-VM release gate. Windows 11 ARM and its x64 emulation are supplemental diagnostics only and never satisfy x64 release evidence.

## Goals / Non-Goals

**Goals:**

- Establish a composition root and lifecycle that can start the local UI quickly, supervise an optional bundled Graph service, and converge to a truthful ready or degraded state.
- Centralize capability computation, compatibility negotiation, process ownership, recovery dispatch, safe settings, and operational diagnostics without taking ownership from business modules.
- Make restart and shutdown behavior explicit enough that every Job/effect can be recovered, reconciled, or reported as requiring action without duplicate side effects.
- Produce reproducible complete/lightweight Windows layouts and test them from the packaged artifact, not only from source trees.

**Non-Goals:**

- Implementing project CRUD, validation, versioning, Graph projection, simulation, risk, AI, backup, or local-rag internals in the runtime layer.
- Adding a service manager, installer, automatic updater, remote operations endpoint, authentication, non-loopback listener, multi-user mode, or macOS/Linux release.
- Persisting a second copy of project Jobs or business data in the application-data directory.
- Discovering and killing processes by PID/name/port, auto-canceling local-rag Tasks, or automatically replaying non-idempotent Provider work.

## Decisions

### 1. Make `cmd/eco-guardian` a thin composition root around a phased runtime coordinator

`cmd/eco-guardian` will parse only bounded launch options, construct platform and module adapters, and call a runtime coordinator under `internal/app/runtime`. The coordinator owns process-level phases such as `loading_settings`, `verifying_package`, `binding_http`, `starting_dependencies`, `recovering`, `opening_recent_project`, `ready`, `degraded`, and `stopping`; it does not contain domain recovery logic.

Startup ordering is:

1. resolve `%LOCALAPPDATA%/EcoGuardian`, create restrictive machine directories, and load/validate versioned settings;
2. verify the executable/package manifest and embedded asset identities;
3. acquire the Eco HTTP listener and start the server with runtime/project-selection routes available;
4. select/probe or start local-rag asynchronously and publish observations;
5. run machine-level recovery hooks, then let #5 open the recent project and run project-level recovery adapters;
6. open the browser only after the listener is serving, and continuously recompute capabilities as dependencies settle.

The runtime status store uses an immutable snapshot plus monotonic generation, so health probes, process callbacks, recovery adapters, and settings changes publish observations through one reducer. HTTP handlers read the snapshot; they never perform startup side effects. A large `main` function with ad hoc goroutines was rejected because ownership, shutdown order, and test injection would become implicit. Blocking the browser until Graph and every business module are healthy was rejected because it prevents the required offline mode.

### 2. Bind Eco Guardian by retaining the operating-system listener, not by probing a free port

The HTTP adapter calls `net.Listen("tcp4", "127.0.0.1:0")` by default and retains the returned listener through server startup. If a future validated local preferred port is present, it attempts that exact loopback bind and then falls back to `:0` within the declared policy. The displayed URL is built from the bound listener address, never from a preselected number or request Host header. This removes the probe-then-bind race for Eco Guardian and guarantees loopback.

Browser launch is a Windows platform port invoked only after a readiness check succeeds. Failure becomes a diagnostic event and console/log URL, not a runtime failure. The server and browser share one origin, avoiding CORS and additional listener configuration.

local-rag does not currently define socket-handle inheritance. For a bundled child, the command builder passes an available loopback candidate and the supervisor treats a bind/start race as a bounded failed attempt, selects a fresh candidate, and starts a new owned child. It never clears the port by terminating its occupant. Designing an undocumented socket-activation protocol for local-rag was rejected; it would require a separate cross-repository contract change.

### 3. Select external or bundled Graph explicitly and model ownership by live handles

Settings expose a small Graph mode and endpoint configuration. Lightweight packages allow only an existing endpoint. Complete packages default to the bundled component but may first consume an explicitly configured compatible endpoint; when no selected compatible service exists they can start the packaged local-rag on a private loopback port. An incompatible occupant remains external and untouched; the bundled fallback uses another port.

`GraphProcessSupervisor` depends on a Windows process adapter, package resolver, command factory, health observer, clock, and log sink. Ownership is established only by the live process handle returned from this instance's create call plus an in-memory launch generation. A persisted process summary is diagnostic only and is never authority to terminate a PID after restart, preventing PID-reuse errors.

Windows creation uses a suspended process, hidden window flags, redirected stdout/stderr handles, and a per-instance Job Object configured with kill-on-close. The child is assigned before resume so local-rag descendants are captured. Graceful stop first asks the owned child to stop through its supported local mechanism, waits to a deadline, and then closes/terminates only through the owned handle/Job Object. External endpoints never enter the Job Object and receive no stop request.

The supervisor tracks `not_selected|external|starting|ready|backoff|exited|restart_exhausted|stopping` and publishes safe exit/restart observations. Restart policy uses a small bounded attempt/window/backoff configuration and resets only after a stable-ready interval. Unlimited restart loops were rejected because missing models or incompatible builds would create a resource loop. Installing a Windows service was rejected because unzip-and-run and no-admin startup are product requirements.

### 4. Keep package identity separate from mutable settings

Both release layouts contain a generated `package-manifest.json` with package mode, supported platform, Eco Guardian version, embedded asset identities, and, for the complete package, pinned local-rag/Python/model relative paths, versions, sizes, and SHA-256 digests. The executable verifies required external assets before execution. Embedded Web/migration/OpenAPI/schema/template assets use build-time digests exposed through build info. Mutable settings select endpoints/preferences but cannot redefine a packaged asset's trusted identity.

The complete layout is assembled from a version-locked input manifest; it does not download models or Python on first launch. The lightweight layout omits those components and defaults to offline until a configured compatible service answers. Both layouts omit an LLM and reuse #12's first-use Provider flow. Download-on-start was rejected because it breaks clean offline startup, package reproducibility, and understandable model-missing failures. Treating arbitrary executable paths as trusted package content was rejected; a user-selected executable can be supported only as an external configuration and never gains owned-package trust without an explicit future contract.

### 5. Extend #8's compatibility evaluator into an operation-specific dependency observation

The runtime reuses the local-rag client and typed health DTO from #8 rather than adding another HTTP client. `GraphCompatibilityEvaluator` consumes:

- HTTP/body status and observation time;
- `api_versions`, `supported_schema_versions`, capabilities, limits, and dependency states;
- provider build identity and an Eco-maintained compatibility table for known minimum fixed releases;
- the operation descriptor requesting snapshot lifecycle, Task polling, activation, core query, FTS, retrieval mode, or rebuild.

The result is `compatible|degraded|incompatible|unknown` plus stable reason codes and safe evidence. API v1/schema 1.0/capabilities/limits are authoritative. SemVer is diagnostic except when the compatibility table names a known affected release line and minimum fixed patch; a major difference is not itself a rejection. The table is compiled/versioned and fixture-tested, not fetched remotely or editable through settings.

Health observations use bounded timeouts and a short TTL to avoid polling storms. Process start and explicit reconnect can invalidate the cache. Every business admission still uses the owning module's exact inspection/recheck; runtime health is not a substitute for #8 Snapshot identity checks or #7 final release Gates. Using only SemVer was rejected because the provider contract explicitly supports capability negotiation. Treating any `degraded` status as fully offline was rejected because Vector/Rerank failures must preserve deterministic Graph work.

### 6. Compute a dependency graph of application capabilities in one registry

Each applied module registers a versioned `CapabilityDescriptor` containing its stable ID, prerequisites, optional prerequisites, safe actions, and a pure evaluator over current observations. Core descriptors cover editing, validation, revision/candidate, simulation, backup, graph sync, deterministic impact, retrieval, AI design, and release. The registry validates duplicate IDs and cycles at startup.

The reducer returns `available|degraded|unavailable|unregistered` plus ordered reason objects, observation generations/timestamps, and action descriptors. Optional failures become warnings; required failures disable the capability. Release consumes #7's Gate Registry projection, so runtime cannot weaken a release policy. Graph sync/impact consume #8's exact distinctions; AI combines retrieval evidence and #12 Provider capability; deterministic simulation does not depend on Graph or AI.

This projection feeds `GET /api/v1/runtime/status`, global UI state, and release/feature admission display. Mutations remain on existing settings, Graph retry/reconnect, credential, and Job endpoints. Action descriptors name a known operation/URI and preconditions; they are not arbitrary commands. A single `online` boolean was rejected because it disables too much and hides partial retrieval modes. Allowing each page to compute dependency state independently was rejected because it would drift from server-side Gate/admission rules.

### 7. Recover Jobs through a registered per-kind policy and a fixed coordinator order

The runtime adds no Job persistence. It uses #7's `jobs`/`job_events` repository and worker boundary and introduces a `RecoveryRegistry` whose adapters are supplied by the owning modules. Each adapter declares:

- Job kinds and states it can inspect;
- immutable input/fingerprint requirements;
- durable effect checkpoints and external identities;
- whether safe automatic resume is permitted;
- project-close and application-shutdown behavior;
- a pure plan followed by an expected-generation state transition.

Project recovery order is chosen to preserve safety: restore journal/project schema and migration checks; release intents and pointer reconciliation; Graph sync/external Task reconciliation; deterministic simulation/risk/impact work; then other queued work. AI recovery only seals already durable terminal material or marks an in-flight Provider attempt interrupted; it never issues another Provider request. The coordinator starts normal workers only after their recovery scan has committed.

For local-rag work, #8's adapter keeps the external four-state Task model. A saved task ID is polled first; Snapshot/result inspection proves namespace/version/hash/count/readiness before the Eco Job completes. `TASK_NOT_FOUND` invokes the owning workflow's documented idempotent inspection path. No runtime cancel endpoint or synthetic local-rag `interrupted` state is added.

Shutdown first stops HTTP mutation admission, persists cancel/interruption intent and waits for bounded safe checkpoints, stops worker claims, then shuts down owned dependencies and stores. Restore/migration/release blockers still prevent project switching as defined by #5, while application exit remains bounded and leaves durable recovery evidence. Globally changing all `running` Jobs to `queued` was rejected because simulation, release, Graph, and AI have different replay rules. Creating resume Jobs was rejected because it breaks original SSE, idempotency, and result identities.

### 8. Store only machine configuration and machine recovery metadata outside projects

`internal/app/runtime/config` owns a versioned strict settings schema at `%LOCALAPPDATA%/EcoGuardian/settings.json`. It uses defaults → existing validated file → bounded explicit launch overrides, validates loopback/network/path/timeouts and package constraints, writes a sibling temporary file with restrictive permissions, flushes it, and atomically replaces the target. Failed validation/write leaves the prior file untouched. Unknown fields are retained only if the versioned decoder can safely round-trip them; otherwise the update fails instead of silently deleting future settings.

The application-data layout is:

```text
%LOCALAPPDATA%/EcoGuardian/
├─ settings.json
├─ runtime/              # restore journal and non-authoritative process summaries
└─ logs/                 # rotating Eco and owned-child operational logs
```

Recent project pointers are non-business settings but project identity, content, and project-scoped Jobs remain in `project.db`. #12 owns AI endpoint/model fields and Credential Manager handles; runtime only consumes their redacted capability projection. Secrets are never accepted by the general settings DTO. Splitting settings across ad hoc module files was rejected because atomic validation and support diagnostics would drift. Moving Jobs to the application directory was rejected because project portability and the existing #7 contract bind them to the project.

### 9. Use one safe structured event pipeline for Eco and child output

The runtime creates the logger before dependency startup. Eco events use typed event names and stable fields: component, phase, state, safe code, process launch generation, Job/task/request IDs, duration, and package/build identities. A redaction layer removes secret-like headers/values, raw bodies, SQL, Graph/business text, embeddings, Provider payloads, and normalizes user-specific paths before JSON encoding. Rotation is bounded by validated size/count settings and uses a non-blocking/bounded sink so log pressure cannot stall Job transactions.

Owned child stdout/stderr are not trusted as already-safe structured data. The supervisor wraps bounded lines as `child_output` with component/stream and applies redaction/truncation; it does not parse arbitrary child JSON into privileged fields. The UI receives a display-safe log directory from runtime status, not arbitrary file-read APIs.

Runtime diagnostics project build/package identity, redacted settings, capability/process observations, Job summaries, and safe error/request IDs through the typed status resource. Raw debug dumps, arbitrary log-file reads, and automatic database attachment were rejected because the tool handles local paths, business configuration, and Provider secrets.

### 10. Keep runtime API and UI as projections over existing resource contracts

OpenAPI gains one read-only runtime status resource and the runtime fields already planned for the non-sensitive settings resource. The status DTO contains a schema version, build/package/listener, startup phase, project summary, process ownership class, dependency observations, capability results/reasons/actions, recovery summaries, log location, and timestamps. It does not expose process handles, secrets, arbitrary executable/project paths, raw errors, or child output.

The Vue app stores this server projection in one runtime store. It uses SSE Job events where a Job exists and bounded runtime polling for machine/service observations. Global indicators and the degradation banner render ordered textual summaries. Reconnect, Graph retry, Provider configuration, credential write, and Job cancel buttons call their existing generated-client endpoints with server-provided preconditions/idempotency behavior. No page decides Graph compatibility, release Gate pass, or Job success locally.

Adding WebSocket-only runtime state was rejected because persistent Job SSE plus polling is already the cross-change contract. Adding process-control endpoints was rejected because the PRD only requires reconnect/manual retry and local ownership is an internal safety boundary.

### 11. Verify pure reducers, real processes, consumer contracts, and packaged artifacts separately

Tests are layered:

- pure tests for settings validation/atomicity, package manifest verification, compatibility/minimum-fix rules, capability reduction, restart policy, shutdown planning, redaction, and Job recovery-plan selection;
- integration tests with temporary application/project directories and fake clocks, process adapters, listeners, local-rag/AI endpoints, Job repositories, and injected failures at every checkpoint;
- Windows process tests with helper executables for hidden child launch, descendants, crash loops, stdout/stderr, PID reuse simulation, Job Object cleanup, external survival, port races, and browser failure;
- provider/Eco consumer contract tests using versioned local-rag #4 health/Task/error/request-ID fixtures plus #8 Snapshot workflows;
- component/UI tests for every capability matrix row, action visibility, SSE/poll recovery, keyboard focus, text alternatives, and copyable URL/log location;
- a Windows Server 2025 x64 CI gate that runs the full Go/frontend/contract suites plus native Windows process, race, handle, and repeated resource-lifecycle tests;
- release tests that unpack complete/lightweight artifacts into a clean Windows 11 x64 client VM and run the executable without source-tree or development-tool dependencies.

Fault tests record deterministic validation/simulation hashes before and after Graph/Vector/Rerank/AI failures to prove isolation. Crash tests cover pre/post child assignment, Job checkpoint, external Task acceptance, release intent, result seal, and shutdown boundaries. Testing only mocked processes was rejected because Windows Job Object and inherited-handle behavior are the core ownership guarantee. Testing only a development build was rejected because missing packaged runtimes/models are a primary acceptance case.

## Risks / Trade-offs

- **[Windows Job Object behavior and child sidecars can vary with process creation flags]** → Assign a suspended child before resume, use dedicated platform tests with descendants, and fail owned startup if assignment cannot be proved.
- **[Bundled local-rag port selection still has a bind race without socket inheritance]** → Use bounded fresh-port attempts, classify bind failures separately, never kill an occupant, and pursue socket activation only through a future cross-repository contract.
- **[Health cache can briefly lag a dependency transition]** → Use short TTLs and invalidation from process/provider outcomes; require owning modules to recheck exact state before work or release.
- **[A central capability registry can become a second business-rule engine]** → Descriptors reference module-owned availability/Gate projections; runtime combines prerequisites but never recomputes validation, Graph readiness, simulation, risk, or release facts.
- **[Automatic deterministic recovery can run stale implementations]** → Require exact complete fingerprints and expected-generation transitions; otherwise preserve interruption and demand a compatible runtime or explicit new Job.
- **[Child logs may contain unexpected sensitive text]** → Treat them as untrusted, truncate/redact before persistence, keep default operational verbosity, and exclude raw output from diagnostics.
- **[Complete packages are large because Python and models are included]** → Keep the lightweight package as an advanced option and make package composition reproducible; do not trade package size for first-run network downloads.
- **[A Windows Server CI image is not the supported Windows 11 client environment]** → Use it for continuous native x64 process/race/resource verification, while retaining a non-elevated clean Windows 11 x64 client VM as the release artifact gate; treat Windows 11 ARM emulation only as supplemental evidence.
- **[Greenfield prerequisite names may differ when #5–#12 are applied]** → Gate implementation on behavior and compile-time ports, adapt once at each existing seam, and stop rather than introduce parallel Job, settings, Graph, or capability models.

## Migration Plan

1. Apply and verify the minimum runtime skeleton with the #5 project/application seams: build info, application-data layout, settings, loopback listener, embedded UI, runtime status, logging, and browser fallback. Keep unregistered capabilities explicitly unavailable.
2. Integrate the applied #7 unified Job/event worker and add the recovery registry without changing Job tables or state values. Add adapters only as their owning #8–#12 modules are present.
3. Integrate #8's generated local-rag client and #4 fixtures, then add compatibility reduction, external/bundled selection, Windows supervisor/Job Object, Task reconciliation, and capability-level UI.
4. Add the complete/lightweight package manifests and deterministic assembly. Run process, contract, UI, fault-injection, and clean-VM suites against the staged packages.
5. Roll forward settings by versioned additive migration with atomic backup/replacement; project schema changes remain owned by their module migrations and mandatory backup policies.
6. Roll back by cleanly stopping the new executable and using a prior binary only when it declares support for the current settings/project schemas. Do not run down migrations or terminate external services. A later roll-forward re-runs machine/project recovery from persisted intents and original Job/Task identities.
