## Purpose

Define the observable contract for starting, supervising, diagnosing, degrading, recovering, and distributing Eco Guardian as a local-first Windows application while preserving offline deterministic work and respecting ownership of external processes and data.

## ADDED Requirements

### Requirement: Single-entry loopback startup
`eco-guardian.exe` SHALL be the only user-facing startup entry for the local application. On startup it SHALL load validated machine settings and embedded defaults, initialize the application and project-independent runtime services, bind the Eco Guardian HTTP server only to `127.0.0.1` on an available runtime-selected port, and expose the embedded Web UI from that same origin. It MUST NOT require a shell, Go, Node.js, Python installed by the user, a database tool, or administrator privileges for normal startup.

After the HTTP host is ready, the runtime SHALL attempt to open the local UI in the default browser when the setting is enabled. A browser-launch failure MUST NOT stop the application; the console when available and the runtime log SHALL show a copyable `http://127.0.0.1:<port>` URL. Recent-project reopening SHALL delegate to the project lifecycle and its lock, migration, recovery, and compatibility checks and MUST NOT bypass an unsafe project-open result.

#### Scenario: Start from a clean machine
- **WHEN** a user launches a valid distribution on a clean supported Windows x64 machine and its required package assets are present
- **THEN** one Eco Guardian process serves the embedded UI on a runtime-selected loopback port and the user can reach it without installing development tools

#### Scenario: Browser launch fails
- **WHEN** the local HTTP host becomes ready but Windows cannot open the default browser
- **THEN** the process remains usable and emits the exact copyable loopback URL to the available console and structured log

#### Scenario: Preferred port is unavailable
- **WHEN** a candidate local port is occupied or loses a bind race during startup
- **THEN** the runtime never terminates the occupying process, selects and binds another available loopback port within a bounded retry budget, or exits with a clear diagnostic if no port can be acquired

#### Scenario: Recent project cannot be reopened safely
- **WHEN** the recent project is locked, has a newer unsupported schema, or requires recovery that cannot be completed
- **THEN** the runtime starts the project selection UI with the safe project diagnostic and does not force-open or mutate that project

### Requirement: Complete and lightweight Windows distributions
The Windows 11 x64 release SHALL provide two unzip-and-run distributions. The complete Graph distribution SHALL contain `eco-guardian.exe`, one pinned compatible local-rag distribution, its Python runtime, the required Embedding and Rerank model assets, and default runtime configuration. The lightweight distribution SHALL contain `eco-guardian.exe` and a configuration template for connecting to an existing compatible local-rag service and an optional user-configured AI Provider.

`eco-guardian.exe` in both distributions SHALL embed the Web frontend, Eco Guardian migrations, OpenAPI description, default entity schemas, and deterministic DSL/scene/threshold templates. Neither distribution SHALL bundle an LLM; first use of AI SHALL remain unavailable until the user configures a compatible local or cloud OpenAI-compatible Provider. Package metadata SHALL identify bundled component versions and detect missing or corrupt required assets before attempting to use them.

#### Scenario: Complete package is unpacked and launched
- **WHEN** a user unpacks the complete Graph distribution on a clean Windows 11 x64 machine and launches `eco-guardian.exe`
- **THEN** editing, deterministic analysis, Graph, and backup integration can initialize from packaged assets while AI remains explicitly unconfigured until a Provider is supplied

#### Scenario: Required model asset is missing
- **WHEN** the complete package is missing or fails verification for a required local-rag, Python, Embedding, or Rerank asset
- **THEN** startup identifies the exact component category as unavailable, does not execute an unverified asset, and exposes the corresponding degraded or unavailable capabilities with a recovery action

#### Scenario: Lightweight package has no external Graph service
- **WHEN** the lightweight distribution starts with no reachable compatible local-rag endpoint
- **THEN** Eco Guardian starts in a truthful offline/degraded state and keeps offline-safe capabilities available instead of pretending that Graph is ready

#### Scenario: Unsupported operating system is used
- **WHEN** a user attempts to use the v1 release artifact on an unsupported architecture or operating system
- **THEN** the distribution fails with an explicit platform diagnostic and does not claim formal support outside Windows 11 x64

### Requirement: Owned child-process supervision
The runtime SHALL distinguish a bundled local-rag process started by the current Eco Guardian instance from an already-running external service. It SHALL supervise, restart within a bounded policy, and stop only a child whose process handle and ownership record were created by this instance. PID matching, port ownership, executable name, or endpoint equality alone MUST NOT establish ownership. An existing external process MUST never be terminated, attached to the instance's kill-on-close group, or silently replaced.

On Windows, each owned local-rag process SHALL start with no visible console window, have standard output and error redirected to bounded structured component logs, and be assigned to an Eco Guardian-owned Job Object that closes owned descendants when the parent exits abnormally. The runtime SHALL apply readiness and shutdown deadlines, record exit codes and restart exhaustion safely, and shut down in the order: stop new Eco HTTP work, persist recoverable Job boundaries, request owned child shutdown, close the Job Object if needed, then release local stores. A local-rag-managed Python sidecar SHALL remain local-rag's responsibility rather than a separately discovered Eco Guardian process.

#### Scenario: Compatible external service is already running
- **WHEN** the configured endpoint answers with a compatible local-rag health contract before Eco Guardian starts a bundled child
- **THEN** Eco Guardian marks the service as external, consumes it without creating a duplicate child, and leaves it running when Eco Guardian exits

#### Scenario: Bundled child starts successfully
- **WHEN** no selected compatible external service exists and the complete package can start its bundled local-rag
- **THEN** the runtime records owned process identity, redirects output, assigns the child to its Windows Job Object, and does not report Graph readiness until health negotiation succeeds

#### Scenario: Owned child crashes repeatedly
- **WHEN** the bundled local-rag exits before readiness or repeatedly crashes after startup
- **THEN** the supervisor performs only the bounded configured restart attempts, records safe exit diagnostics, and then exposes Graph-dependent capabilities as unavailable without ending offline editing

#### Scenario: Eco Guardian exits unexpectedly
- **WHEN** Eco Guardian terminates abnormally after starting a bundled local-rag process
- **THEN** the Windows Job Object reclaims that owned process tree while any external local-rag process remains unaffected

#### Scenario: Endpoint is occupied by an incompatible process
- **WHEN** a configured endpoint or port is served by an incompatible or unrelated process
- **THEN** Eco Guardian does not terminate that process, does not label it owned, and either starts its bundled child on a separate loopback port permitted by its package mode or reports a clear incompatible-service degradation

### Requirement: Health and compatibility negotiation
Before submitting Graph work, Eco Guardian SHALL evaluate local-rag `GET /health` using its advertised API versions, Graph Snapshot schema versions, capabilities, limits, dependency states, and HTTP/status semantics. Compatibility SHALL require API `v1`, Graph Snapshot schema `1.0`, and the capabilities and effective limits needed by the attempted operation. `status: ok` and `status: degraded` SHALL be interpreted with HTTP 200, while `status: unavailable` or an HTTP 503 core failure SHALL make affected core capabilities unavailable.

Service SemVer SHALL be recorded for diagnosis and SHALL enforce an explicitly maintained minimum-fix rule for known affected release lines, but an implementation major difference alone MUST NOT reject a service that fully advertises and satisfies the required v1/schema/capability contract. Unknown additive health fields SHALL be ignored. Health checks SHALL be bounded, side-effect free from Eco Guardian's perspective, use a short-lived observation time, and MUST NOT be treated as permanent readiness; operation admission and recovery SHALL recheck the exact required state.

#### Scenario: Contract-compatible service has a different implementation major
- **WHEN** local-rag advertises API v1, Graph Snapshot schema 1.0, all required capabilities and limits, and is not excluded by a known minimum-fix rule, but its service major differs from Eco Guardian's diagnostic expectation
- **THEN** Eco Guardian accepts the service and records the SemVer difference as diagnostic information rather than rejecting it solely by major number

#### Scenario: Known unfixed build is detected
- **WHEN** a service falls below the maintained minimum fixed version for its affected release line
- **THEN** Eco Guardian marks the impacted capabilities incompatible, shows the observed and required fix versions, and submits no affected work

#### Scenario: Optional retrieval dependency is degraded
- **WHEN** `/health` is HTTP 200/degraded and core Graph query remains usable but Vector or enabled Rerank is unavailable
- **THEN** deterministic Graph capabilities remain available while the precise retrieval/AI degradation and provider identity are surfaced as warnings

#### Scenario: Required Graph contract is missing
- **WHEN** `/health` omits v1, schema 1.0, durable Task polling, or another capability required for a requested Graph operation
- **THEN** Eco Guardian blocks that operation with a stable compatibility reason and does not infer support from the service name or SemVer

#### Scenario: Graph health times out
- **WHEN** health negotiation exceeds its bounded timeout or returns malformed required fields
- **THEN** the runtime reports a safe retryable timeout or non-retryable contract error as appropriate and keeps unrelated local capabilities available

### Requirement: Capability-scoped degradation and recovery
The runtime SHALL compute each user capability from explicit prerequisites rather than one global online flag. At minimum it SHALL distinguish local editing, deterministic validation, revision/candidate browsing, deterministic simulation, backup, Graph snapshot sync, deterministic Graph traversal/impact, retrieval, AI design, and formal release. Disabling one capability MUST include stable machine-readable reasons, user-displayable text, the observations that caused it, and permitted recovery actions; it MUST NOT change deterministic calculation results or stored business facts.

When Graph core or the required Graph/FTS snapshot contract is unavailable, users SHALL still be able to edit, validate, save revisions, create/select candidates, run Graph-independent deterministic simulations, browse existing local history, and use backup capabilities that are otherwise healthy; Graph sync, new impact analysis, Graph-dependent AI work, and formal release SHALL be disabled. Vector/Rerank degradation SHALL preserve deterministic traversal, editing, simulation, and any release Gate whose declared required Graph inputs remain satisfied, while retrieval and AI display their actual partial mode. AI Provider failure SHALL disable only AI creation and MUST NOT disable retrieval or deterministic capabilities. Capability recovery SHALL re-enable each affected action independently after successful re-probe/reconciliation, without requiring an application restart when safe.

#### Scenario: Graph core is unavailable
- **WHEN** local-rag core storage/query or required Graph/FTS snapshot capability is unavailable
- **THEN** editing, validation, revisions/candidates, Graph-independent simulation, and healthy backup remain usable while Graph sync, new impact analysis, Graph-dependent AI, and formal release show explicit disabled reasons

#### Scenario: Vector and Rerank are degraded
- **WHEN** Graph core and deterministic traversal are healthy but Vector and Rerank are unavailable
- **THEN** deterministic Graph traversal and simulation remain enabled, retrieval reports its supported fallback or unavailable mode, AI reflects the resulting evidence limitation, and unrelated capabilities remain unchanged

#### Scenario: AI Provider alone is unavailable
- **WHEN** local-rag and all deterministic modules are healthy but no compatible AI Provider is configured or reachable
- **THEN** only creation or retry of AI design work is disabled, while manual editing, Graph, retrieval, simulation, risk, backup, and eligible release behavior remain available

#### Scenario: One dependency recovers
- **WHEN** a previously unavailable capability passes a fresh compatibility probe and its persisted work has been reconciled
- **THEN** only capabilities depending on that observation are re-enabled and the runtime preserves the states of still-degraded dependencies

#### Scenario: Release requirements remain incomplete
- **WHEN** editing works but the current release policy has an unavailable required Gate or service capability
- **THEN** runtime and version UI keep release disabled with the exact missing Gate/capability list and do not relax the policy because the application is in degraded mode

### Requirement: Unified runtime status and service-state user experience
`GET /api/v1/runtime/status` SHALL return a typed, current projection containing Eco Guardian version/package mode and listener URL; startup phase; active/recent project state without exposing arbitrary paths; owned or external process state; local-rag health/compatibility observation; Graph/FTS/BM25/Vector/Rerank states; AI Provider state; registered application capabilities and release availability; active recovery summaries; degradation reasons; safe actions; observation timestamps; and the local log location. The endpoint MUST be read-only and MUST NOT start a process, create/retry a Job, submit/rebuild a Snapshot, invoke a Provider, or mutate capability state merely because it was read.

The global UI SHALL render the same server projection as persistent service indicators and a degradation banner, with textual state and reason labels in addition to color/icons. It SHALL expose reconnect/re-probe and only those manual retry actions allowed by the server's action descriptors, use existing Graph/Job/settings endpoints for mutations, and restore truthful state through SSE plus polling rather than inventing a second task protocol. A reload MUST NOT display a queued, interrupted, or unreconciled operation as ready.

#### Scenario: Runtime status is read repeatedly
- **WHEN** a client polls runtime status while services are healthy or degraded
- **THEN** it receives consistently typed observations without causing new child processes, Graph work, AI calls, retries, or project mutations

#### Scenario: User inspects a degradation banner
- **WHEN** one or more capabilities are degraded or unavailable
- **THEN** the UI names each affected service/capability, explains what remains usable, provides the log location and only safe next actions, and does not rely on color alone

#### Scenario: Reconnect succeeds
- **WHEN** a keyboard user invokes the advertised re-probe action after a dependency recovers
- **THEN** focus moves to an understandable status summary and the UI refreshes the server-authoritative capability projection without creating duplicate business Jobs

#### Scenario: Page reload occurs during recovery
- **WHEN** the browser reloads or SSE disconnects while a Job or external Task is being reconciled
- **THEN** the UI obtains the persisted Job/runtime state through event replay or polling and continues to show recovery in progress rather than inferring success

### Requirement: Persistent Eco Guardian Job recovery and external Task reconciliation
The local runtime SHALL use the existing persistent Eco Guardian Job contract with exactly `queued`, `running`, `succeeded`, `failed`, `canceled`, and `interrupted`, ordered durable events, SSE replay, and polling fallback. Startup and project-open recovery SHALL dispatch each nonterminal Job to its registered job-kind recovery policy and preserve its original identity, input hash, immutable revision/result references, effect checkpoints, external IDs, warnings, and request correlation. It MUST NOT create a parallel runtime Job table or infer success from process exit, HTTP reachability, or a terminal external Task alone.

Deterministic and idempotent work MAY resume or replay only after its complete persisted input and required implementation fingerprint still match. Release, migration, and restore recovery SHALL follow their existing durable intents and continue to block unsafe project switching. AI work interrupted during or after a Provider request MUST NOT automatically invoke the model again; it SHALL remain interrupted/failed with an explicit user retry action that creates the lineage required by the AI workflow.

local-rag Task states SHALL remain exactly `queued`, `running`, `succeeded`, and `failed`, with no cancellation request. Once an external task ID has been accepted, Eco Guardian cancellation or shutdown SHALL mean stop waiting and mark/reconcile the wrapping Job as appropriate, not remote cancellation. Recovery SHALL poll the original Task and inspect the exact namespace/version/hash/result contract before completing the wrapping Job; a missing Task SHALL follow the owning workflow's idempotent inspection/reconciliation rule.

#### Scenario: Deterministic Job is interrupted
- **WHEN** the application restarts with a queued, running, or interrupted deterministic Job whose complete input, implementation fingerprint, and effect checkpoints still match
- **THEN** its registered recovery policy resumes or safely replays the original Job and preserves one result identity

#### Scenario: Deterministic Job fingerprint changed
- **WHEN** recovery cannot reproduce a Job's required evaluator, projector, policy, or other implementation identity
- **THEN** the Job remains interrupted or ends with a recovery-required diagnostic and no mixed-version result is sealed

#### Scenario: AI Provider call was in flight
- **WHEN** Eco Guardian restarts after an AI attempt entered a Provider request but no terminal receipt was durably sealed
- **THEN** the original attempt is marked interrupted, the model is not called automatically, and only an explicit user retry can create the allowed linked attempt

#### Scenario: External Graph Task was accepted
- **WHEN** restart or project reopen finds an Eco Guardian Job with a saved local-rag task ID
- **THEN** recovery polls that original four-state Task and inspects the exact Snapshot identity before updating the wrapping Job, without submitting duplicate Graph work first

#### Scenario: User cancels after external acceptance
- **WHEN** the user requests cancellation after local-rag has accepted a Task
- **THEN** Eco Guardian stops local waiting and records `interrupted` where the owning workflow specifies, preserves the external task ID, and never sends or claims a remote cancellation

### Requirement: Simple machine configuration and data-directory separation
Machine-wide user settings SHALL be stored under `%LOCALAPPDATA%/EcoGuardian/settings.json` and SHALL be limited to non-business runtime preferences such as recent projects, browser auto-open, package mode, local-rag endpoint/executable and timeout, non-secret AI endpoint/model, log policy, and existing backup defaults. Settings changes SHALL be schema validated and atomically replaced; invalid values SHALL leave the last valid settings intact and return field-specific diagnostics. Secrets SHALL remain in Windows Credential Manager or an explicitly supported non-persistent environment fallback and MUST NOT be returned by settings/runtime APIs.

Runtime diagnostics, logs, and non-business process/recovery metadata SHALL reside under `%LOCALAPPDATA%/EcoGuardian`; project business facts and project-scoped persistent Jobs SHALL remain in the selected `project.db`. The runtime MUST NOT copy entity, revision, simulation, risk, AI evidence, or release facts into machine settings or process-state files. All configured network listeners for Eco Guardian and bundled local-rag SHALL be loopback; v1 SHALL reject a configured unauthenticated non-loopback local-rag endpoint.

#### Scenario: User changes a valid runtime setting
- **WHEN** the user updates browser auto-open, a loopback local-rag endpoint, or a supported timeout through the settings API/UI
- **THEN** the validated non-secret setting is atomically persisted and the runtime reports whether it applied immediately or requires an explicit reconnect/restart

#### Scenario: Invalid settings are submitted
- **WHEN** a settings update contains an invalid endpoint, timeout, package path, or unsupported value
- **THEN** the API returns field-specific Problem Details, preserves the previous valid file, and does not partially reconfigure running services

#### Scenario: Non-loopback unauthenticated Graph endpoint is configured
- **WHEN** a user attempts to configure local-rag at a non-loopback address without a separately defined authenticated contract
- **THEN** Eco Guardian rejects the value and continues using the prior safe configuration or offline state

#### Scenario: Project is backed up
- **WHEN** the existing backup capability packages project business data
- **THEN** machine settings, process ownership metadata, logs, and credentials are excluded from the project backup

### Requirement: Structured logs and safe diagnostics
Eco Guardian and owned child output SHALL produce rotating, bounded logs under `%LOCALAPPDATA%/EcoGuardian/logs`. Structured events SHALL cover startup phases, selected listener address, package/component verification, process ownership/start/exit/restart/shutdown, health and capability transitions, Job recovery decisions, external task reconciliation, safe error codes, and request IDs. Logs MUST NOT contain credentials, authorization headers, raw Provider bodies, embeddings, Graph/business text, full entity payloads, SQL, stack traces exposed as user diagnostics, or unredacted user-specific filesystem paths.

The runtime SHALL expose the log directory and safe application/package versions, redacted configuration state, current runtime/capability observations, process ownership summaries, Job states, and safe error/request IDs through its runtime diagnostics. Log rotation or diagnostic publication failure MUST NOT stop editing or corrupt project data, and the UI SHALL clearly report the diagnostic limitation and original log location.

#### Scenario: Component failure is correlated
- **WHEN** an owned local-rag process exits or a compatibility probe fails during a Graph Job
- **THEN** runtime status and structured logs expose safe component, phase, error code, Job/task identity where applicable, and correlated request IDs without sensitive payloads

#### Scenario: Logs exceed their configured bound
- **WHEN** runtime and child logs reach the configured size or retained-file limit
- **THEN** rotation preserves a bounded recent history without blocking the application or writing logs into `project.db`

#### Scenario: Logging fails
- **WHEN** the log directory is unwritable or rotation fails
- **THEN** the runtime reports a safe diagnostic through the remaining available channel, keeps project operations available where safe, and does not redirect logs into the project database

### Requirement: Windows lifecycle, degradation, and packaging verification
Automated verification SHALL cover the runtime contract at unit, integration, process, contract, UI, and clean-VM levels. Provider/Eco Guardian fixtures SHALL verify local-rag health compatibility, HTTP 200/503 mapping, API/schema/capability negotiation, unknown additive fields, SemVer diagnostic/minimum-fix behavior, four-state Task polling/restart, stable errors/request IDs, and partial retrieval degradation. Process tests SHALL cover port bind races, external-versus-owned identity, hidden child startup, Job Object cleanup, bounded restart, ordered shutdown, browser failure, asset/model absence, log redaction/rotation, and application crash/reopen.

A Windows Server x64 CI gate SHALL run the full Go, frontend, generated-contract, native process, targeted race, handle, and repeated lifecycle/resource suites. A separate clean Windows 11 x64 client-VM acceptance suite SHALL launch both distribution modes without development tools, verify loopback-only listeners, exercise Graph unavailable, Vector/Rerank degraded, AI unavailable, capability-by-capability recovery, persisted Eco Guardian Job recovery, original local-rag Task reconciliation, and exit behavior. The server CI result MUST NOT be treated as the client-VM artifact acceptance result. Windows 11 ARM or x64 emulation MAY provide supplemental diagnostics but MUST NOT satisfy either required x64 gate. Tests MUST prove that dependency failures do not change deterministic validation/simulation results or corrupt working state, immutable revisions, releases, backups, or the project database.

#### Scenario: Clean complete-package smoke test
- **WHEN** the complete distribution is unpacked in a clean supported Windows VM and the primary startup, Graph health, project reopen, and exit flow is exercised
- **THEN** it runs without development tools, listens only on loopback, supervises only its owned child, and leaves no owned process after exit

#### Scenario: Partial services recover independently
- **WHEN** a failure-injection test removes and restores Graph core, Vector, Rerank, and AI Provider capabilities one at a time
- **THEN** runtime/UI capability states, allowed actions, and recovery events follow the specified matrix without altering deterministic outputs

#### Scenario: Crash occurs at each recovery boundary
- **WHEN** the process is terminated around Job checkpoint, external Task acceptance, release intent, or terminal-result sealing boundaries
- **THEN** restart either proves and completes the original operation or reports recovery required, never duplicates an unsafe side effect or guesses success

#### Scenario: External service survives application exit
- **WHEN** the process test connects to a compatible user-started local-rag and then exits Eco Guardian
- **THEN** the external process remains alive while the test still confirms cleanup of every child started by Eco Guardian itself
